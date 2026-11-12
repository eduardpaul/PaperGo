package connections

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// Operation kinds: a control a workflow run asks for, performed after the
// run's transaction commits, so no writer waits on a broker or a service.
const (
	KindPublish = "connection.publish"
	KindCall    = "connection.call"
)

// requestTimeout bounds waiting for a broker's acknowledgement.
const requestTimeout = 10 * time.Second

// Publish is a message to publish through an MQTT channel: Payload, a
// JSON value sent as JSON, or as its text when it is a string;
// PayloadText, sent as it is; or PayloadBase64, the bytes it encodes.
type Publish struct {
	Topic         string          `json:"topic"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	PayloadText   *string         `json:"payload_text,omitempty"`
	PayloadBase64 string          `json:"payload_base64,omitempty"`
	// QoS is 0 or 1, by default 1.
	QoS    *int `json:"qos,omitempty"`
	Retain bool `json:"retain,omitempty"`
}

// Published is what a publish answers: the request ID receivers see as
// the papergo-request user property.
type Published struct {
	// Operation is the operation that publishes a workflow run's message,
	// after the run's step commits; Request is then its ID.
	Operation string `json:"operation_id,omitempty"`
	Request   string `json:"request,omitempty"`
	Topic     string `json:"topic"`
	QoS       int    `json:"qos"`
	Retain    bool   `json:"retain"`
}

// publishJob is a checked publish, as an operation keeps it.
type publishJob struct {
	WorkspaceID string `json:"workspace_id"`
	Key         string `json:"key"`
	Channel     string `json:"channel"`
	Topic       string `json:"topic"`
	Payload     []byte `json:"payload"`
	QoS         int    `json:"qos"`
	Retain      bool   `json:"retain"`
	Run         string `json:"run,omitempty"`
}

// callJob is a checked call, as an operation keeps it.
type callJob struct {
	WorkspaceID string `json:"workspace_id"`
	Key         string `json:"key"`
	Channel     string `json:"channel"`
	Call        Call   `json:"call"`
	App         bool   `json:"app,omitempty"`
	Run         string `json:"run,omitempty"`
}

// checkPublish checks a publish against its channel, for a caller who
// sends at most limit bytes.
func checkPublish(ch Channel, in Publish, limit int) (publishJob, error) {
	if err := checkTopic(in.Topic); err != nil {
		return publishJob{}, dms.Invalid("topic: " + err.Error())
	}
	if !topicMatches(ch.Topics, in.Topic) {
		return publishJob{}, dms.Invalid(fmt.Sprintf("topic %s is outside channel %s (%s)", in.Topic, ch.Key, ch.Topics))
	}
	qos := 1
	if in.QoS != nil {
		qos = *in.QoS
	}
	if qos != 0 && qos != 1 {
		return publishJob{}, dms.Invalid("qos is 0 or 1")
	}
	if in.Retain && !ch.Retain {
		return publishJob{}, dms.Invalid("channel " + ch.Key + " does not let publishes be retained")
	}
	given := 0
	for _, set := range []bool{len(in.Payload) > 0 && string(in.Payload) != "null", in.PayloadText != nil, in.PayloadBase64 != ""} {
		if set {
			given++
		}
	}
	if given > 1 {
		return publishJob{}, dms.Invalid("a publish has one of payload, payload_text and payload_base64")
	}
	var payload []byte
	var text string
	switch {
	case in.PayloadText != nil:
		payload = []byte(*in.PayloadText)
	case in.PayloadBase64 != "":
		var err error
		if payload, err = base64.StdEncoding.DecodeString(in.PayloadBase64); err != nil {
			return publishJob{}, dms.Invalid("payload_base64 is not base64")
		}
	case len(in.Payload) == 0 || string(in.Payload) == "null":
	case json.Unmarshal(in.Payload, &text) == nil:
		payload = []byte(text)
	default:
		payload = in.Payload
	}
	if len(payload) > limit {
		return publishJob{}, dms.Invalid(fmt.Sprintf("a payload has at most %d KiB", limit>>10))
	}
	return publishJob{Topic: in.Topic, Payload: payload, QoS: qos, Retain: in.Retain}, nil
}

// recheckPublish checks a publish an operation keeps against its channel
// as it is now.
func recheckPublish(t target, job publishJob) error {
	if t.conn.Kind != KindMQTT {
		return dms.Invalid("an HTTP channel is called, not published to")
	}
	if !topicMatches(t.channel.Topics, job.Topic) {
		return dms.Invalid(fmt.Sprintf("topic %s is outside channel %s (%s)", job.Topic, t.channel.Key, t.channel.Topics))
	}
	if job.Retain && !t.channel.Retain {
		return dms.Invalid("channel " + t.channel.Key + " does not let publishes be retained")
	}
	return nil
}

// requireEnabled refuses using a disabled connection.
func requireEnabled(t target) error {
	if !t.conn.Enabled {
		return &httpapi.Problem{Status: 503, Code: "connection_disabled", Detail: "connection " + t.conn.Key + " is disabled"}
	}
	return nil
}

// controlTarget returns the channel subject controls now.
func (s *Service) controlTarget(ctx context.Context, d *dms.Service, subject, workspaceID, key, channelKey string) (target, error) {
	t, err := dms.Read(ctx, d, func(t *dms.Service) (target, error) {
		return authorize(ctx, t, subject, workspaceID, key, channelKey, UseControl)
	})
	if err != nil {
		return target{}, err
	}
	return t, requireEnabled(t)
}

// sendPublish publishes a checked message and records it in the audit.
func (s *Service) sendPublish(ctx context.Context, d *dms.Service, subject string, t target, job publishJob, request string) (Published, error) {
	e, err := s.runtime.mqttOf(t.conn)
	if err != nil {
		return Published{}, err
	}
	pctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err = e.publish(pctx, job.Topic, job.Payload, byte(job.QoS), job.Retain, request); err != nil {
		return Published{}, err
	}
	details := map[string]any{"channel": t.channel.Key, "topic": job.Topic, "qos": job.QoS, "retain": job.Retain, "request": request}
	if job.Run != "" {
		details["run_id"] = job.Run
	}
	// The message is out; recording it must not fail the publish because the
	// caller went away.
	actx := context.WithoutCancel(ctx)
	err = d.Write(actx, func(tx *dms.Service) error { return audit(actx, tx, subject, "connection.publish", t.conn, details) })
	return Published{Request: request, Topic: job.Topic, QoS: job.QoS, Retain: job.Retain}, err
}

// sendCall makes a checked call. A control call is recorded in the audit.
func (s *Service) sendCall(ctx context.Context, d *dms.Service, subject string, t target, p prepared, request, run string) (Envelope, error) {
	e, err := s.runtime.httpOf(t.conn)
	if err != nil {
		return Envelope{}, err
	}
	answer, err := e.do(ctx, t.channel, p, subject, request)
	if MethodUse(p.method) != UseControl {
		return answer, err
	}
	details := map[string]any{"channel": t.channel.Key, "method": p.method, "path": strings.SplitN(p.path.Raw, "?", 2)[0], "request": request}
	if err != nil {
		details["error"] = err.Error()
	} else {
		details["status"] = answer.Status
	}
	if run != "" {
		details["run_id"] = run
	}
	actx := context.WithoutCancel(ctx)
	if e := d.Write(actx, func(tx *dms.Service) error { return audit(actx, tx, subject, "connection.call", t.conn, details) }); e != nil && err == nil {
		err = e
	}
	return answer, err
}

// register makes s run the operations of controls workflow runs ask for.
func (s *Service) register(d *dms.Service) error {
	err := d.RegisterOperationHandler(KindPublish, dms.OperationHandler{Scope: ControlScope, Deadline: time.Hour, MaxAttempts: 10,
		Validate: func(ctx context.Context, t *dms.Service, subject, containerID string, payload json.RawMessage) error {
			var job publishJob
			if err := json.Unmarshal(payload, &job); err != nil || job.WorkspaceID != containerID {
				return dms.Invalid("a publish names its workspace, connection and channel")
			}
			_, err := authorize(ctx, t, subject, job.WorkspaceID, job.Key, job.Channel, UseControl)
			return err
		},
		Execute: func(ctx context.Context, d *dms.Service, op *operations.Operation) error {
			var job publishJob
			if err := json.Unmarshal(op.Payload, &job); err != nil {
				return err
			}
			t, err := s.controlTarget(ctx, d, op.CreatedBy, job.WorkspaceID, job.Key, job.Channel)
			if err != nil {
				return retryable(err)
			}
			// The channel may have changed since the run asked: the message
			// must still lie inside it.
			if err = recheckPublish(t, job); err != nil {
				return err
			}
			out, err := s.sendPublish(ctx, d, op.CreatedBy, t, job, op.ID)
			if err != nil {
				return retryable(err)
			}
			return finish(ctx, d, op, out)
		}})
	if err != nil {
		return err
	}
	return d.RegisterOperationHandler(KindCall, dms.OperationHandler{Scope: ControlScope, Deadline: time.Hour, MaxAttempts: 10,
		Validate: func(ctx context.Context, t *dms.Service, subject, containerID string, payload json.RawMessage) error {
			var job callJob
			if err := json.Unmarshal(payload, &job); err != nil || job.WorkspaceID != containerID {
				return dms.Invalid("a call names its workspace, connection and channel")
			}
			_, err := authorize(ctx, t, subject, job.WorkspaceID, job.Key, job.Channel, UseControl)
			return err
		},
		Execute: func(ctx context.Context, d *dms.Service, op *operations.Operation) error {
			var job callJob
			if err := json.Unmarshal(op.Payload, &job); err != nil {
				return err
			}
			t, err := s.controlTarget(ctx, d, op.CreatedBy, job.WorkspaceID, job.Key, job.Channel)
			if err != nil {
				return retryable(err)
			}
			limit := MaxBody
			if job.App {
				limit = MaxAppBody
			}
			p, err := prepareCall(t.channel, job.Call, limit)
			if err != nil {
				return err
			}
			if p.headers.Get("Idempotency-Key") == "" {
				p.headers.Set("Idempotency-Key", op.ID)
			}
			answer, err := s.sendCall(ctx, d, op.CreatedBy, t, p, op.ID, job.Run)
			if err != nil {
				return retryable(err)
			}
			return finish(ctx, d, op, answer)
		}})
}

// retryable defers an operation whose connection is busy or not
// connected; other failures end it.
func retryable(err error) error {
	var b *busy
	if errors.As(err, &b) {
		return dms.Retry(err, max(b.after, 5*time.Second))
	}
	var p *httpapi.Problem
	if errors.As(err, &p) && (p.Status == http.StatusBadGateway || p.Status == http.StatusGatewayTimeout || p.Code == "connection_disabled") {
		return dms.Retry(err, 30*time.Second)
	}
	return err
}

func finish(ctx context.Context, d *dms.Service, op *operations.Operation, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return d.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		op.Status, op.Result, op.CompletedAt = operations.StatusSucceeded, raw, &completed
		return t.SaveOperation(ctx, op)
	})
}
