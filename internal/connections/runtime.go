package connections

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/netip"
	"net/url"
	"sync"
	"time"

	store "papergo/internal/dal/connections"
	"papergo/internal/dal/dberr"
	"papergo/internal/egress"
	"papergo/internal/hosts"
	"papergo/internal/httpapi"
	"papergo/internal/worker"
)

// States of a connection. An MQTT connection is connecting, connected or
// failed; an HTTP connection is ok or failing after a call, and unknown
// before one. A disabled connection is disabled.
const (
	StateDisabled   = "disabled"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateFailed     = "failed"
	StateOK         = "ok"
	StateFailing    = "failing"
	StateUnknown    = "unknown"
)

// Status is how a connection is doing.
type Status struct {
	State       string     `json:"state"`
	LastError   string     `json:"last_error,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	// CheckedAt is when an HTTP connection last answered or failed.
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// Working reports whether callers can use the connection now.
func (s Status) Working() bool {
	return s.State == StateConnected || s.State == StateOK || s.State == StateUnknown
}

// TestResult is what a test of a connection found.
type TestResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Took is how long it took, in milliseconds.
	Took int64 `json:"took_ms"`
}

// endpoint is a running connection of one kind.
type endpoint interface {
	status() Status
	close()
}

// running is an endpoint and the stored connection it runs.
type running struct {
	conn store.Connection
	ep   endpoint
}

// Runtime holds the process's enabled connections open and in step with
// what is stored: a write wakes it at once, and every reconcileEvery it
// compares all of them with the database.
type Runtime struct {
	svc      *Service
	networks []netip.Prefix
	log      *slog.Logger
	limits   *limits

	mu      sync.Mutex
	running map[string]*running
	hubs    map[string]*hub
	// triggers are the connection.message triggers messages fire, and
	// cancel ends what stores their events.
	triggers *triggers
	cancel   context.CancelFunc
	wake     chan struct{}
}

// reconcileEvery is how often the runtime compares its connections with
// the database when no write wakes it.
var reconcileEvery = 30 * time.Second

func newRuntime(svc *Service, networks []netip.Prefix, log *slog.Logger) *Runtime {
	return &Runtime{svc: svc, networks: networks, log: log, limits: newLimits(), running: map[string]*running{}, hubs: map[string]*hub{}, triggers: newTriggers(), wake: make(chan struct{}, 1)}
}

// policy is what a connection may reach. Without TLS it reaches only the
// operator's networks; with TLS also the organization's trusted hosts.
func (r *Runtime) policy(tlsOn bool) egress.Policy {
	return egress.Policy{Networks: r.networks, RequirePrivate: !tlsOn, Trusted: func(host string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		list, err := r.svc.DMS.TrustedHosts(ctx)
		return err == nil && hosts.Trusted(list.Hosts, host)
	}}
}

// tlsConfig trusts the system's roots and the connection's CA for host.
func tlsConfig(ca, host string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if ca != "" && !pool.AppendCertsFromPEM([]byte(ca)) {
		return nil, errors.New("tls.ca holds no certificate PaperGo can read")
	}
	return &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}, nil
}

// Status is how the stored connection c is doing.
func (r *Runtime) Status(c store.Connection) Status {
	if !c.Enabled {
		return Status{State: StateDisabled}
	}
	r.mu.Lock()
	run := r.running[c.ID]
	r.mu.Unlock()
	if run == nil {
		if c.Kind == KindHTTP {
			return Status{State: StateUnknown}
		}
		return Status{State: StateConnecting}
	}
	return run.ep.status()
}

// Reload brings the connections in step with what is stored, soon: a
// write wakes the reconciliation.
func (r *Runtime) Reload(string) {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Maintenance keeps the connections in step with what is stored: every
// reconcileEvery, and at once after a write.
func (r *Runtime) Maintenance() worker.Maintenance {
	return worker.Maintenance{Name: "connections", Every: reconcileEvery, Wake: r.wake, Run: func(ctx context.Context, now time.Time) error {
		r.limits.sweep(now)
		return r.reconcile(ctx)
	}}
}

// Start stores the events of connection.message triggers until Close.
func (r *Runtime) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.raise(ctx)
}

// Close closes every running connection.
func (r *Runtime) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.closeAll()
}

func (r *Runtime) closeAll() {
	r.mu.Lock()
	all := r.running
	r.running = map[string]*running{}
	r.mu.Unlock()
	for _, run := range all {
		run.ep.close()
	}
}

// live reports whether c should run: enabled, in a live workspace.
func (r *Runtime) live(ctx context.Context, c store.Connection) (bool, error) {
	if !c.Enabled {
		return false, nil
	}
	ws, err := r.svc.DMS.DAL.Resources().Resource(ctx, c.WorkspaceID)
	if dberr.IsNotFound(err) {
		return false, nil
	}
	return err == nil && ws.DeletedAt == nil, err
}

// stop closes connection id's endpoint. A deleted connection's hub goes
// too; a disabled one keeps it for its listeners.
func (r *Runtime) stop(id string, deleted bool) {
	r.mu.Lock()
	run := r.running[id]
	delete(r.running, id)
	if deleted {
		delete(r.hubs, id)
	}
	r.mu.Unlock()
	if run != nil {
		run.ep.close()
	}
}

// apply runs c unless it should not run, replacing an endpoint of an older
// version.
func (r *Runtime) apply(ctx context.Context, c store.Connection) {
	should, err := r.live(ctx, c)
	if err != nil {
		r.log.Warn("connection workspace could not be read", "connection", c.ID, "error", err)
		return
	}
	r.mu.Lock()
	current := r.running[c.ID]
	r.mu.Unlock()
	if !should {
		r.stop(c.ID, false)
		return
	}
	if current != nil && current.conn.Version == c.Version {
		return
	}
	// The previous version goes first: a broker takes a second session of
	// the same client ID over from the first, which would reconnect.
	if current != nil {
		current.ep.close()
	}
	var ep endpoint
	v, err := r.svc.open(c)
	if err != nil {
		r.log.Error("connection configuration cannot be decrypted", "connection", c.ID, "error", err)
		ep = failed{errors.New("its configuration cannot be decrypted with this server's key")}
	} else if ep, err = r.start(c, v); err != nil {
		r.log.Warn("connection could not start", "connection", c.ID, "error", err)
		ep = failed{err}
	}
	r.mu.Lock()
	r.running[c.ID] = &running{conn: c, ep: ep}
	r.mu.Unlock()
}

// reconcile brings every connection in step with what is stored.
func (r *Runtime) reconcile(ctx context.Context) error {
	// Triggers that cannot be read keep the last ones read; connections
	// are kept in step regardless.
	if err := r.loadTriggers(ctx); err != nil {
		r.log.Warn("connection.message triggers could not be read", "error", err)
	}
	rows, err := r.svc.DMS.DAL.Connections().List(ctx, store.Filter{})
	if err != nil {
		return err
	}
	stored := map[string]bool{}
	for _, c := range rows {
		stored[c.ID] = true
		r.apply(ctx, c)
	}
	r.mu.Lock()
	var gone []string
	for id := range r.running {
		if !stored[id] {
			gone = append(gone, id)
		}
	}
	// A hub outlives its sessions, not its connection: one a subscription
	// made for a connection that never ran goes too.
	for id := range r.hubs {
		if _, run := r.running[id]; !run && !stored[id] {
			delete(r.hubs, id)
		}
	}
	r.mu.Unlock()
	for _, id := range gone {
		r.stop(id, true)
	}
	return nil
}

// start opens the endpoint of a stored connection.
func (r *Runtime) start(c store.Connection, v sealed) (endpoint, error) {
	u, err := url.Parse(v.Target)
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case KindMQTT:
		return r.startMQTT(c, v, u)
	case KindHTTP:
		return r.startHTTP(c, v, u)
	}
	return nil, errors.New("unknown kind " + string(c.Kind))
}

// mqttOf returns the running MQTT endpoint of c.
func (r *Runtime) mqttOf(c store.Connection) (*mqttEndpoint, error) {
	r.mu.Lock()
	run := r.running[c.ID]
	r.mu.Unlock()
	if run != nil {
		if ep, ok := run.ep.(*mqttEndpoint); ok {
			return ep, nil
		}
		if f, ok := run.ep.(failed); ok {
			return nil, connectionFailed(f.err.Error())
		}
	}
	return nil, notReady("connection " + c.Key + " is still connecting")
}

// httpOf returns the running HTTP endpoint of c.
func (r *Runtime) httpOf(c store.Connection) (*httpEndpoint, error) {
	r.mu.Lock()
	run := r.running[c.ID]
	r.mu.Unlock()
	if run != nil {
		if ep, ok := run.ep.(*httpEndpoint); ok {
			return ep, nil
		}
		if f, ok := run.ep.(failed); ok {
			return nil, connectionFailed(f.err.Error())
		}
	}
	return nil, notReady("connection " + c.Key + " is starting")
}

// hostUses counts the connections that reach each public host by name.
// Addresses on the operator's networks never need trust.
func (s *Service) hostUses(ctx context.Context) (map[string]int, error) {
	rows, err := s.DMS.DAL.Connections().List(ctx, store.Filter{})
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, c := range rows {
		v, err := s.open(c)
		if err != nil {
			continue
		}
		u, err := url.Parse(v.Target)
		if err != nil || !schemes[c.Kind][u.Scheme] {
			continue
		}
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !egress.Public(ip) {
			continue
		}
		if host := hosts.Key(u.Hostname()); host != "" {
			out[host]++
		}
	}
	return out, nil
}

// Test connects or calls once with c's configuration, apart from the
// running endpoint.
func (r *Runtime) Test(ctx context.Context, c store.Connection, v sealed) TestResult {
	start := time.Now()
	u, err := url.Parse(v.Target)
	if err == nil {
		switch c.Kind {
		case KindMQTT:
			err = r.testMQTT(ctx, c, v, u)
		case KindHTTP:
			err = r.testHTTP(ctx, c, v, u)
		}
	}
	took := time.Since(start).Milliseconds()
	if err != nil {
		return TestResult{Detail: explain(err), Took: took}
	}
	detail := "Connected and signed in."
	if c.Kind == KindHTTP {
		detail = "The service answered."
	}
	return TestResult{OK: true, Detail: detail, Took: took}
}

// explain words a connection failure so it says what to do.
func explain(err error) string {
	var network *egress.NetworkError
	switch {
	case errors.As(err, &network):
		return network.Error() + "."
	case errors.Is(err, egress.ErrNeedsTLS):
		return "This address is public, so PaperGo reaches it only over TLS: use mqtts, wss or https."
	case errors.Is(err, egress.ErrUnreachable):
		return "PaperGo never connects to link-local, metadata, multicast or unspecified addresses."
	}
	return err.Error()
}

// failed is a connection that could not start, which says why.
type failed struct{ err error }

func (f failed) status() Status { return Status{State: StateFailed, LastError: explain(f.err)} }
func (failed) close()           {}

// Problems of the endpoint.
func connectionFailed(detail string) error {
	return &httpapi.Problem{Status: 502, Code: "connection_failed", Detail: detail}
}

func connectionTimeout(detail string) error {
	return &httpapi.Problem{Status: 504, Code: "connection_timeout", Detail: detail}
}

// busy refuses a request for now: the connection is starting or its calls
// are all in use, or the caller is over their rate. The caller tries again
// after.
type busy struct {
	code, detail string
	after        time.Duration
}

func (b *busy) Error() string { return b.detail }

func notReady(detail string) error {
	return &busy{code: "connection_busy", detail: detail, after: time.Second}
}
