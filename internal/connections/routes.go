package connections

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	store "papergo/internal/dal/connections"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/integration"
	"papergo/internal/solutions"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

// Config is what the feature is built from.
type Config struct {
	DMS     *dms.Service
	Secrets *integration.Secrets
	// Workflows serves connection.message triggers; without it messages
	// start no runs.
	Workflows *workflow.Service
	// Networks are the non-public networks connections may reach
	// (connections.networks).
	Networks []netip.Prefix
	Log      *slog.Logger
}

// New builds the feature's service and its runtime, which its maintenance
// runs.
func New(c Config) *Service {
	s := &Service{DMS: c.DMS, Secrets: c.Secrets, Workflows: c.Workflows}
	s.runtime = newRuntime(s, c.Networks, c.Log)
	return s
}

// Register makes d run the operations of controls workflows ask for, and
// report the hosts connections reach among the trusted hosts' uses.
func (s *Service) Register(d *dms.Service) error {
	d.AddHostReporter("connections", s.hostUses)
	d.AddPackagePart(s.part())
	solutions.RegisterPart(s.solutionPart())
	if err := workflow.RegisterTriggerType(s.triggerType()); err != nil {
		return err
	}
	return s.register(d)
}

// Start stores the events connection.message triggers raise, until Close.
func (s *Service) Start(context.Context) error {
	s.runtime.Start()
	return nil
}

// Maintenance holds the enabled connections open and in step with what is
// stored.
func (s *Service) Maintenance() worker.Maintenance { return s.runtime.Maintenance() }

// Close closes the connections, after the workers stopped.
func (s *Service) Close() { s.runtime.Close() }

// Module serves the connections routes and their live subscriptions.
func (s *Service) Module() httpapi.Module { return module{s} }

type module struct{ s *Service }

func (module) Name() string { return "connections" }

// Feature names connections in /auth/config, so the web app offers them.
func (module) Feature() string { return "connections" }

func (module) SubscriptionSource() string { return "connections" }

func (m module) Subscribe(ctx context.Context, in httpapi.Request, id string, spec json.RawMessage, deliver httpapi.Deliver) (httpapi.Subscription, error) {
	return m.s.subscribe(ctx, in, id, spec, deliver)
}

func (m module) Routes() []httpapi.Route {
	byKey := "/v1/workspaces/{id}/connections/{key}"
	return []httpapi.Route{
		{Method: http.MethodGet, Pattern: "/v1/connections", OperationID: "listConnections", Handle: handle(m.list)},
		{Method: http.MethodPost, Pattern: "/v1/connections", OperationID: "createConnection", Handle: handle(m.create)},
		{Method: http.MethodGet, Pattern: "/v1/connections/{id}", OperationID: "getConnection", Handle: handle(m.get)},
		{Method: http.MethodPut, Pattern: "/v1/connections/{id}", OperationID: "replaceConnection", Handle: handle(m.replace)},
		{Method: http.MethodDelete, Pattern: "/v1/connections/{id}", OperationID: "deleteConnection", Handle: handle(m.delete)},
		{Method: http.MethodPost, Pattern: "/v1/connections/{id}/test", OperationID: "testConnection", Handle: handle(m.test)},
		{Method: http.MethodPost, Pattern: "/v1/connections/{id}/signing-secret", OperationID: "renewConnectionSigningSecret", Handle: handle(m.renew)},
		{Method: http.MethodGet, Pattern: "/v1/connections/{id}/calls", OperationID: "listConnectionCalls", Handle: handle(m.calls)},
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/connections", OperationID: "listWorkspaceConnections", Handle: handle(m.uses)},
		{Method: http.MethodGet, Pattern: byKey, OperationID: "getWorkspaceConnection", Handle: handle(m.use)},
		{Method: http.MethodPost, Pattern: byKey + "/channels/{channel}/publish", OperationID: "publishToConnection", Handle: handle(m.publish)},
		{Method: http.MethodGet, Pattern: byKey + "/channels/{channel}/fetch", OperationID: "fetchFromConnection", Handle: handle(m.fetch)},
		{Method: http.MethodPost, Pattern: byKey + "/channels/{channel}/call", OperationID: "callConnection", Handle: handle(m.call)},
	}
}

// handle answers a busy connection or a caller over their rate with
// Retry-After.
func handle(fn func(http.ResponseWriter, *http.Request, httpapi.Request) error) func(http.ResponseWriter, *http.Request, httpapi.Request) error {
	return func(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
		err := fn(w, r, in)
		var b *busy
		if errors.As(err, &b) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(max(b.after.Seconds(), 1)))))
			status := http.StatusServiceUnavailable
			if b.code == "connection_rate_limited" {
				status = http.StatusTooManyRequests
			}
			return &httpapi.Problem{Status: status, Code: b.code, Detail: b.detail}
		}
		return err
	}
}

// accepted answers a run's control, which an operation performs after the
// run's step commits: 202 with the operation at Location, and the answer's
// own shape naming it as operation_id, so a script's typed client reads it
// and awaits it with ctx.wait. A replayed Idempotency-Key answers 200.
func accepted(w http.ResponseWriter, r *http.Request, op *dms.OperationView, body any) {
	w.Header().Set("Location", "/v1/operations/"+op.ID)
	status := http.StatusAccepted
	if op.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		status = http.StatusOK
	}
	httpapi.Respond(w, r, status, body)
}

// fromApp reports whether an app's frame sent the request through its
// page, which marks what it forwards; apps send less.
func fromApp(r *http.Request) bool { return r.Header.Get("PaperGo-App") != "" }

func (m module) list(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	workspace := r.URL.Query().Get("workspace")
	if workspace == "" {
		return dms.Invalid("workspace names the workspace whose connections to list")
	}
	out, err := m.s.List(r.Context(), in.Subject, workspace)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (m module) create(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body Save
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := m.s.Create(r.Context(), in.DMS, in.Subject, body)
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/v1/connections/"+out.ID)
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusCreated, out)
	return nil
}

func (m module) get(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.Get(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (m module) replace(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	var body Save
	if !in.Decode(w, r, &body) {
		return nil
	}
	out, err := m.s.Replace(r.Context(), in.DMS, in.Subject, r.PathValue("id"), version, body)
	if err != nil {
		return err
	}
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (m module) delete(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	version, ok := in.IfMatch(w, r)
	if !ok {
		return nil
	}
	if err := m.s.Delete(r.Context(), in.DMS, in.Subject, r.PathValue("id"), version); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m module) test(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.Test(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (m module) renew(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.RotateSigningSecret(r.Context(), in.DMS, in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.ETag(w, out.Version)
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

func (m module) calls(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.Calls(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (m module) uses(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.Uses(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, map[string]any{"value": out})
	return nil
}

func (m module) use(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.s.Use(r.Context(), in.Subject, r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// publish sends a message through an MQTT channel. A workflow run's
// publish is an operation performed after the run's step commits.
func (m module) publish(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body Publish
	if !in.Decode(w, r, &body) {
		return nil
	}
	ctx := r.Context()
	t, err := m.s.controlTarget(ctx, in.DMS, in.Subject, r.PathValue("id"), r.PathValue("key"), r.PathValue("channel"))
	if err != nil {
		return err
	}
	if t.conn.Kind != KindMQTT {
		return dms.Invalid("an HTTP channel is called, not published to")
	}
	limit := MaxPayload
	if fromApp(r) {
		limit = MaxAppPayload
	}
	job, err := checkPublish(t.channel, body, limit)
	if err != nil {
		return err
	}
	if err = m.s.runtime.limit(in.Subject, t.conn.ID, t.channel.Key, UseControl); err != nil {
		return err
	}
	job.WorkspaceID, job.Key, job.Channel, job.Run = t.conn.WorkspaceID, t.conn.Key, t.channel.Key, in.Run
	if in.Run != "" {
		op, err := in.DMS.EnqueueOperation(ctx, in.Subject, t.conn.WorkspaceID, KindPublish, marshal(job))
		if err != nil {
			return err
		}
		accepted(w, r, op, Published{Operation: op.ID, Topic: job.Topic, QoS: job.QoS, Retain: job.Retain})
		return nil
	}
	out, err := m.s.sendPublish(ctx, in.DMS, in.Subject, t, job, newSecret()[:32])
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// fetch reads through an HTTP channel: GET, or HEAD with method=HEAD. It
// changes nothing, so a workflow treats it as a read.
func (m module) fetch(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	method := http.MethodGet
	if q.Get("method") == http.MethodHead {
		method = http.MethodHead
	} else if q.Get("method") != "" && q.Get("method") != http.MethodGet {
		return pathInvalid("fetch reads with GET or HEAD; call changes")
	}
	t, err := dms.Read(ctx, in.DMS, func(tx *dms.Service) (target, error) {
		return authorize(ctx, tx, in.Subject, r.PathValue("id"), r.PathValue("key"), r.PathValue("channel"), UseSee)
	})
	if err != nil {
		return err
	}
	if err = requireEnabled(t); err != nil {
		return err
	}
	if t.conn.Kind != KindHTTP {
		return dms.Invalid("an MQTT channel is subscribed to, not fetched")
	}
	call := Call{Method: method, Path: q.Get("path")}
	if accept := q.Get("accept"); accept != "" {
		call.Headers = map[string]string{"Accept": accept}
	}
	p, err := prepareCall(t.channel, call, MaxBody)
	if err != nil {
		return err
	}
	e, err := m.s.runtime.httpOf(t.conn)
	if err != nil {
		return err
	}
	ttl := t.channel.cacheFor()
	if _, hit := e.cached(cacheKey(t.channel, p), time.Now()); ttl == 0 || !hit || method != http.MethodGet {
		if err = m.s.runtime.limit(in.Subject, t.conn.ID, t.channel.Key, UseSee); err != nil {
			return err
		}
	}
	answer, err := m.s.sendCall(ctx, in.DMS, in.Subject, t, p, newSecret()[:32], in.Run)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, answer)
	return nil
}

// call calls an HTTP channel with any method. A workflow run's call is an
// operation performed after the run's step commits.
func (m module) call(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var body Call
	if !in.Decode(w, r, &body) {
		return nil
	}
	ctx := r.Context()
	method := strings.ToUpper(body.Method)
	if method == "" {
		method = http.MethodGet
	}
	use := MethodUse(method)
	t, err := dms.Read(ctx, in.DMS, func(tx *dms.Service) (target, error) {
		return authorize(ctx, tx, in.Subject, r.PathValue("id"), r.PathValue("key"), r.PathValue("channel"), use)
	})
	if err != nil {
		return err
	}
	if err = requireEnabled(t); err != nil {
		return err
	}
	if t.conn.Kind != KindHTTP {
		return dms.Invalid("an MQTT channel is published to, not called")
	}
	// A run's request here is inside its step's write transaction, where
	// nothing waits on a service: a read goes through fetch instead.
	if in.Run != "" && use == UseSee {
		return dms.Invalid("a run reads with fetch; its call takes POST, PUT, PATCH or DELETE")
	}
	limit := MaxBody
	if fromApp(r) {
		limit = MaxAppBody
	}
	p, err := prepareCall(t.channel, body, limit)
	if err != nil {
		return err
	}
	if err = m.s.runtime.limit(in.Subject, t.conn.ID, t.channel.Key, use); err != nil {
		return err
	}
	// A run's call is an operation, performed after its step commits.
	if in.Run != "" {
		job := callJob{WorkspaceID: t.conn.WorkspaceID, Key: t.conn.Key, Channel: t.channel.Key, Call: body, App: fromApp(r), Run: in.Run}
		op, err := in.DMS.EnqueueOperation(ctx, in.Subject, t.conn.WorkspaceID, KindCall, marshal(job))
		if err != nil {
			return err
		}
		accepted(w, r, op, Envelope{Operation: op.ID})
		return nil
	}
	request := newSecret()[:32]
	if key := r.Header.Get("Idempotency-Key"); key != "" && p.headers.Get("Idempotency-Key") == "" {
		p.headers.Set("Idempotency-Key", key)
	}
	answer, err := m.s.sendCall(ctx, in.DMS, in.Subject, t, p, request, in.Run)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, answer)
	return nil
}

// Calls returns an HTTP connection's recent calls to its managers.
func (s *Service) Calls(ctx context.Context, subject, id string) ([]CallRecord, error) {
	c, err := dms.Read(ctx, s.DMS, func(t *dms.Service) (store.Connection, error) { return managed(ctx, t, subject, id) })
	if err != nil {
		return nil, err
	}
	if c.Kind != KindHTTP {
		return nil, dms.Invalid("only HTTP connections keep a call log; an MQTT connection's monitor subscribes to its channels")
	}
	e, err := s.runtime.httpOf(c)
	if err != nil {
		return []CallRecord{}, nil
	}
	return e.recent(), nil
}
