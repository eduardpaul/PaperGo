package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	store "papergo/internal/dal/connections"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/workflow"
)

// TriggerMessage is the trigger type, and event type, of MQTT messages that
// start workflow runs.
const TriggerMessage = "connection.message"

// Trigger bounds.
const (
	// defaultInterval is how often a trigger starts a run for one topic at
	// most when it says nothing; MinInterval is the shortest it may say.
	defaultInterval = time.Minute
	minInterval     = time.Second
	maxInterval     = 24 * time.Hour
	// eventPayload is the largest payload an event carries; a larger one is
	// left out and payload_omitted says so.
	eventPayload = 16 << 10
	// pendingEvents bounds the events waiting to be stored; past it new
	// ones are dropped, as a broker flood must not grow the process.
	pendingEvents = 256
	// armedTopics bounds the topics one trigger remembers a run for, as a
	// filter such as # must not grow the process with every topic a broker
	// names.
	armedTopics = 10000
)

// MessageTrigger is a connection.message trigger's options: the messages
// of topics Topic matches, inside an MQTT channel the workflow's author may
// see, at most one run per topic every MinInterval, and only when
// Condition holds.
type MessageTrigger struct {
	Connection  string            `json:"connection"`
	Channel     string            `json:"channel"`
	Topic       string            `json:"topic,omitempty"`
	MinInterval string            `json:"min_interval,omitempty"`
	Condition   *MessageCondition `json:"condition,omitempty"`
}

// MessageCondition tests a message's payload, or the value at Path in a
// JSON payload (dot-separated keys). changed holds when the value differs
// from the last one that started a run for the topic.
type MessageCondition struct {
	Path  string `json:"path,omitempty"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// Condition operators.
const (
	OpEq      = "eq"
	OpNe      = "ne"
	OpGt      = "gt"
	OpGte     = "gte"
	OpLt      = "lt"
	OpLte     = "lte"
	OpChanged = "changed"
)

func (m MessageTrigger) interval() time.Duration {
	d, err := time.ParseDuration(m.MinInterval)
	if err != nil || d == 0 {
		return defaultInterval
	}
	return d
}

func parseMessageTrigger(t workflow.Trigger) (MessageTrigger, error) {
	var m MessageTrigger
	if err := workflow.TriggerOptions(t, &m); err != nil {
		return m, err
	}
	if t.CollectionID != "" {
		return m, errors.New("connection.message triggers take no collection_id")
	}
	if !keyPattern.MatchString(m.Connection) || !keyPattern.MatchString(m.Channel) {
		return m, errors.New("options name the connection's and the channel's keys")
	}
	if m.Topic != "" {
		if err := checkFilter(m.Topic); err != nil {
			return m, fmt.Errorf("options.topic: %w", err)
		}
	}
	if m.MinInterval != "" {
		d, err := time.ParseDuration(m.MinInterval)
		if err != nil || d < minInterval || d > maxInterval {
			return m, errors.New("options.min_interval is a duration from 1s to 24h, such as 5m")
		}
	}
	if c := m.Condition; c != nil {
		switch c.Op {
		case OpEq, OpNe, OpChanged:
		case OpGt, OpGte, OpLt, OpLte:
			if _, ok := number(c.Value); !ok {
				return m, errors.New("options.condition: gt, gte, lt and lte compare with a number")
			}
		default:
			return m, errors.New("options.condition.op is eq, ne, gt, gte, lt, lte or changed")
		}
		if len(c.Path) > 200 {
			return m, errors.New("options.condition.path is at most 200 characters")
		}
	}
	return m, nil
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	}
	return 0, false
}

// value is what a condition tests of a message: the decoded payload, or the
// value at path inside it.
func conditionValue(m Message, path string) (any, bool) {
	var v any
	if m.Encoding != "" || json.Unmarshal(m.Payload, &v) != nil {
		return nil, false
	}
	if path == "" {
		return v, true
	}
	for _, key := range strings.Split(path, ".") {
		object, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = object[key]; !ok {
			return nil, false
		}
	}
	return v, true
}

func holds(c MessageCondition, value any, previous any, seen bool) bool {
	switch c.Op {
	case OpChanged:
		return !seen || string(marshal(value)) != string(marshal(previous))
	case OpEq:
		return string(marshal(value)) == string(marshal(c.Value))
	case OpNe:
		return string(marshal(value)) != string(marshal(c.Value))
	}
	got, ok := number(value)
	want, _ := number(c.Value)
	if !ok {
		return false
	}
	switch c.Op {
	case OpGt:
		return got > want
	case OpGte:
		return got >= want
	case OpLt:
		return got < want
	case OpLte:
		return got <= want
	}
	return false
}

// triggerType is connection.message for the workflow engine.
func (s *Service) triggerType() workflow.TriggerType {
	return workflow.TriggerType{
		Name: TriggerMessage,
		Validate: func(t workflow.Trigger) error {
			_, err := parseMessageTrigger(t)
			return err
		},
		Check: func(ctx context.Context, t *dms.Service, author, workspaceID string, tr workflow.Trigger) error {
			m, err := parseMessageTrigger(tr)
			if err != nil {
				return dms.Invalid(err.Error())
			}
			target, err := authorize(ctx, t, author, workspaceID, m.Connection, m.Channel, UseSee)
			if refused(err) {
				return dms.Invalid("the workflow's author cannot see channel " + m.Channel + " of connection " + m.Connection + ": " + err.Error())
			}
			if err != nil {
				return err
			}
			if target.conn.Kind != KindMQTT {
				return dms.Invalid("connection " + m.Connection + " is not an MQTT broker; HTTP services start runs through the REST API")
			}
			if m.Topic != "" && !filterCovers(target.channel.Topics, m.Topic) {
				return dms.Invalid(fmt.Sprintf("options.topic %s is not inside channel %s (%s)", m.Topic, m.Channel, target.channel.Topics))
			}
			return nil
		},
		Accepts: func(ctx context.Context, t *dms.Service, match workflow.TriggerMatch, e dms.Event) (bool, error) {
			workflowID, _ := e.Data["workflow_id"].(string)
			position, _ := e.Data["trigger"].(float64)
			if n, ok := e.Data["trigger"].(int); ok {
				position = float64(n)
			}
			if workflowID != match.WorkflowID || int(position) != match.Position {
				return false, nil
			}
			m, err := parseMessageTrigger(match.Trigger)
			if err != nil {
				return false, nil
			}
			// The run acts as its author, who must still see the channel.
			_, err = authorize(ctx, t, match.Author, match.WorkspaceID, m.Connection, m.Channel, UseSee)
			if refused(err) {
				return false, nil
			}
			return err == nil, err
		},
	}
}

// refused reports whether err says only that a use is not allowed or names
// nothing, as opposed to a failure to find out.
func refused(err error) bool {
	var problem *httpapi.Problem
	return err != nil && (denied(err) == nil || errors.As(err, &problem))
}

// armed is a trigger the runtime fires: its workflow trigger, options and
// what it saw of each topic.
type armed struct {
	match   workflow.TriggerMatch
	options MessageTrigger
	mu      sync.Mutex
	fired   map[string]time.Time
	last    map[string]any
}

// triggers are the connection.message triggers the runtime fires, by
// workspace, read again with each reconciliation.
type triggers struct {
	mu    sync.Mutex
	armed map[string][]*armed
	queue chan dms.Event
}

func newTriggers() *triggers {
	return &triggers{armed: map[string][]*armed{}, queue: make(chan dms.Event, pendingEvents)}
}

// loadTriggers reads the workflows' connection.message triggers, keeping
// what each already saw.
func (r *Runtime) loadTriggers(ctx context.Context) error {
	if r.svc.Workflows == nil {
		return nil
	}
	matches, err := r.svc.Workflows.ModuleTriggers(ctx, TriggerMessage)
	if err != nil {
		return err
	}
	r.triggers.mu.Lock()
	defer r.triggers.mu.Unlock()
	previous := map[string]*armed{}
	for _, list := range r.triggers.armed {
		for _, a := range list {
			previous[fmt.Sprintf("%s/%d", a.match.WorkflowID, a.match.Position)] = a
		}
	}
	next := map[string][]*armed{}
	for _, m := range matches {
		options, err := parseMessageTrigger(m.Trigger)
		if err != nil {
			continue
		}
		a := &armed{match: m, options: options, fired: map[string]time.Time{}, last: map[string]any{}}
		if p := previous[fmt.Sprintf("%s/%d", m.WorkflowID, m.Position)]; p != nil {
			a.fired, a.last = p.fired, p.last
		}
		next[m.WorkspaceID] = append(next[m.WorkspaceID], a)
	}
	r.triggers.armed = next
	return nil
}

// fire raises connection.message events for the triggers a message of
// connection c's channel starts. Retained messages are what the broker
// kept, not news, and start nothing.
func (r *Runtime) fire(c store.Connection, channels []Channel, m Message) {
	if m.Retained {
		return
	}
	r.triggers.mu.Lock()
	list := r.triggers.armed[c.WorkspaceID]
	r.triggers.mu.Unlock()
	for _, a := range list {
		o := a.options
		if o.Connection != c.Key {
			continue
		}
		var ch *Channel
		for i := range channels {
			if channels[i].Key == o.Channel && channels[i].Can(UseSee) {
				ch = &channels[i]
			}
		}
		if ch == nil || !topicMatches(ch.Topics, m.Topic) || o.Topic != "" && !topicMatches(o.Topic, m.Topic) {
			continue
		}
		if !a.passes(m, time.Now()) {
			continue
		}
		data := map[string]any{"workflow_id": a.match.WorkflowID, "trigger": a.match.Position, "connection": c.Key, "channel": o.Channel, "topic": m.Topic, "at": m.At}
		if len(m.Payload) <= eventPayload {
			data["payload"] = json.RawMessage(m.Payload)
			if m.Encoding != "" {
				data["encoding"] = m.Encoding
			}
		} else {
			data["payload_omitted"] = true
		}
		select {
		case r.triggers.queue <- dms.Event{Type: TriggerMessage, WorkspaceID: c.WorkspaceID, Data: data}:
		default:
			r.log.Warn("connection.message events are arriving faster than they are stored; dropping one", "connection", c.ID, "topic", m.Topic)
		}
	}
}

// passes applies a trigger's condition and interval to a message of a
// topic, and remembers what it saw.
func (a *armed) passes(m Message, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c := a.options.Condition; c != nil {
		value, ok := conditionValue(m, c.Path)
		if !ok {
			return false
		}
		previous, seen := a.last[m.Topic]
		if !holds(*c, value, previous, seen) {
			return false
		}
		// A change the interval holds back is not yet seen: it starts a run
		// once the interval has passed, unless the value went back.
		if a.throttled(m.Topic, now) {
			return false
		}
		a.room(m.Topic, now)
		// Only changed compares with the value that last started a run.
		if c.Op == OpChanged {
			a.last[m.Topic] = value
		}
	} else if a.throttled(m.Topic, now) {
		return false
	} else {
		a.room(m.Topic, now)
	}
	a.fired[m.Topic] = now
	return true
}

// room keeps what the trigger remembers within armedTopics before topic is
// added: it forgets the topics whose interval has passed, and all of them
// when that is not enough, as a restart would.
func (a *armed) room(topic string, now time.Time) {
	if _, known := a.fired[topic]; known || len(a.fired) < armedTopics {
		return
	}
	interval := a.options.interval()
	for t, last := range a.fired {
		if now.Sub(last) >= interval {
			delete(a.fired, t)
			delete(a.last, t)
		}
	}
	if len(a.fired) >= armedTopics {
		clear(a.fired)
		clear(a.last)
	}
}

// throttled reports whether the trigger started a run for topic within its
// interval.
func (a *armed) throttled(topic string, now time.Time) bool {
	last, ok := a.fired[topic]
	return ok && now.Sub(last) < a.options.interval()
}

// raise stores the events fire queued, a few at a time, until ctx ends.
func (r *Runtime) raise(ctx context.Context) {
	for {
		var batch []dms.Event
		select {
		case <-ctx.Done():
			return
		case e := <-r.triggers.queue:
			batch = append(batch, e)
		}
		for len(batch) < 50 {
			select {
			case e := <-r.triggers.queue:
				batch = append(batch, e)
				continue
			default:
			}
			break
		}
		err := r.svc.DMS.Write(ctx, func(t *dms.Service) error {
			for _, e := range batch {
				t.Raise(ctx, e.Type, dms.SystemSubject, e.WorkspaceID, nil, "", e.Data)
			}
			return nil
		})
		if err != nil && ctx.Err() == nil {
			r.log.Warn("connection.message events could not be stored", "events", len(batch), "error", err)
		}
	}
}
