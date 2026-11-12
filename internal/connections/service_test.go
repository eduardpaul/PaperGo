package connections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/eclipse/paho.golang/paho"

	audits "papergo/internal/dal/audit"
	store "papergo/internal/dal/connections"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/integration"
	"papergo/internal/solutions"
)

// home is a workspace alice owns, bob is a member of and carol visits,
// with a Facilities list only alice writes, and the feature serving it.
type home struct {
	s          *dms.Service
	svc        *Service
	ws, list   string
	ctx        context.Context
	connection View
}

func setup(t *testing.T, target string, channels []Channel) *home {
	t.Helper()
	ctx := context.Background()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	secrets, err := integration.NewSecrets(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(Config{DMS: s, Secrets: secrets, Networks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Log: slog.Default()})
	if err = svc.Register(s); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Facilities"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", list.ID, list.Version, dms.Permissions{Grants: []dms.Permission{{PrincipalType: "workspace_owner", PrincipalID: ws.ID, Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	for subject, role := range map[string]string{"bob": "member", "carol": "visitor"} {
		if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: subject, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	directory := identity.Directory{DB: db.SQL}
	content := []string{"workspace.read", "item.read", "item.write", "list.manage"}
	for subject, scopes := range map[string][]string{"alice": append([]string{ManageScope, ControlScope, "solution.manage", "app.manage"}, content...), "bob": append([]string{ControlScope}, content...), "carol": {"workspace.read", "item.read"}} {
		role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Role of " + subject, Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: subject}, false); err != nil {
			t.Fatal(err)
		}
	}
	h := &home{s: s, svc: svc, ws: ws.ID, list: list.ID, ctx: ctx}
	if target == "" {
		return h
	}
	kind := KindMQTT
	if strings.HasPrefix(target, "http") {
		kind = KindHTTP
	}
	h.connection, err = svc.Create(ctx, s, "alice", Save{WorkspaceID: ws.ID, Kind: kind, Name: "Home broker", Target: target, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return h
}

// reconcile brings the runtime in step, as its maintenance does.
func (h *home) reconcile(t *testing.T) {
	t.Helper()
	if err := h.svc.runtime.reconcile(h.ctx); err != nil {
		t.Fatal(err)
	}
}

func problemCode(err error) string {
	var p *httpapi.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func may(v UseView, channel string) []Use {
	for _, ch := range v.Channels {
		if ch.Key == channel {
			return ch.May
		}
	}
	return nil
}

var homeChannels = []Channel{
	{Key: "sensors", Topics: "home/+/sensor/#", Uses: []Use{UseSee}},
	{Key: "lights", Topics: "home/+/light/set", Uses: []Use{UseControl}},
	{Key: "heating", Topics: "boiler/#", Uses: []Use{UseSee, UseControl}},
}

// Members see and control what a channel leaves open to the workspace,
// visitors nothing; a channel borrowing its control from a list lets only
// those who write the list control it, and owners always.
func TestChannelAccessIsBorrowed(t *testing.T) {
	h := setup(t, "", nil)
	channels := slices.Clone(homeChannels)
	channels[2].Access = map[Use]*Access{UseControl: {Level: LevelWrite, On: h.list}}
	if _, err := h.svc.Create(h.ctx, h.s, "bob", Save{WorkspaceID: h.ws, Kind: KindMQTT, Name: "Broker", Target: "mqtt://127.0.0.1:1", Channels: channels}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("a member created a connection: %v", err)
	}
	overlapping := append(slices.Clone(channels), Channel{Key: "all", Topics: "home/#", Uses: []Use{UseControl}})
	if _, err := h.svc.Create(h.ctx, h.s, "alice", Save{WorkspaceID: h.ws, Kind: KindMQTT, Name: "Broker", Target: "mqtt://127.0.0.1:1", Channels: overlapping}); err == nil || !strings.Contains(err.Error(), "lights and all") {
		t.Fatalf("overlapping control channels were saved: %v", err)
	}
	created, err := h.svc.Create(h.ctx, h.s, "alice", Save{WorkspaceID: h.ws, Kind: KindMQTT, Name: "Broker", Target: "mqtt://127.0.0.1:1", Credentials: &Credentials{Username: "papergo", Password: "secret"}, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	if created.Credentials.Password != "" || created.Credentials.Username != "papergo" || created.Key != "broker" {
		t.Fatalf("view: %+v", created)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("the view shows the password")
	}
	for subject, want := range map[string]map[string][]Use{
		"alice": {"sensors": {UseSee}, "lights": {UseControl}, "heating": {UseSee, UseControl}},
		"bob":   {"sensors": {UseSee}, "lights": {UseControl}, "heating": {UseSee}},
		"carol": {"sensors": {}, "lights": {}, "heating": {}},
	} {
		v, err := h.svc.Use(h.ctx, subject, h.ws, "broker")
		if err != nil {
			t.Fatal(subject, err)
		}
		for channel, uses := range want {
			if got := may(v, channel); !slices.Equal(got, uses) {
				t.Errorf("%s may %v through %s, want %v", subject, got, channel, uses)
			}
		}
	}
	if _, err = h.svc.Use(h.ctx, "mallory", h.ws, "broker"); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("an outsider read a connection: %v", err)
	}
	if _, err = h.svc.List(h.ctx, "bob", h.ws); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("a member listed the managed view: %v", err)
	}
	if _, err = h.svc.controlTarget(h.ctx, h.s, "bob", h.ws, "broker", "heating"); problemCode(err) != "connection_forbidden" {
		t.Fatalf("bob controlled the heating: %v", err)
	}
	if _, err = h.svc.controlTarget(h.ctx, h.s, "alice", h.ws, "broker", "heating"); err != nil {
		t.Fatal(err)
	}

	// Replacing keeps the credentials; deleting the list a channel borrows
	// from refuses its use and says so.
	replaced, err := h.svc.Replace(h.ctx, h.s, "alice", created.ID, created.Version, Save{Name: "Broker", Target: "mqtt://127.0.0.1:2", Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := h.s.DAL.Connections().Find(h.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := h.svc.open(stored)
	if err != nil || sealed.Credentials.Password != "secret" || replaced.Target != "mqtt://127.0.0.1:2" {
		t.Fatalf("replace lost the credentials: %+v %v", sealed, err)
	}
	if _, err = h.svc.Replace(h.ctx, h.s, "alice", created.ID, created.Version, Save{Name: "Broker", Target: "mqtt://127.0.0.1:2", Channels: channels}); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatalf("a stale replace: %v", err)
	}
	list, err := h.s.DAL.Resources().Resource(h.ctx, h.list)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.Delete(h.ctx, "alice", h.list, list.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.controlTarget(h.ctx, h.s, "bob", h.ws, "broker", "heating"); problemCode(err) != "connection_access_missing" {
		t.Fatalf("a channel borrowing from a deleted list: %v", err)
	}
	if err = h.svc.Delete(h.ctx, h.s, "alice", created.ID, replaced.Version); err != nil {
		t.Fatal(err)
	}
}

// broker is the MQTT broker tests reach: PAPERGO_TEST_MQTT_URL, an MQTT 5
// broker on loopback that lets anyone publish and subscribe.
func broker(t *testing.T) string {
	target := os.Getenv("PAPERGO_TEST_MQTT_URL")
	if target == "" {
		t.Skip("PAPERGO_TEST_MQTT_URL names no MQTT broker")
	}
	return target
}

// frames collects what a subscription delivers.
type frames struct {
	mu  sync.Mutex
	got []MessagesFrame
}

func (f *frames) deliver(_ context.Context, frame httpapi.LiveFrame) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, frame.Data.(MessagesFrame))
	return nil
}

func (f *frames) payloads(topic string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, frame := range f.got {
		for _, m := range frame.Messages {
			if m.Topic == topic {
				out = append(out, string(m.Payload))
			}
		}
	}
	return out
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting: " + what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rawClient is a client of the test broker of its own, as a device is.
func rawClient(t *testing.T, target string, received chan<- *paho.Publish) *paho.Client {
	t.Helper()
	u, _ := url.Parse(target)
	r := &Runtime{networks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	conn, err := r.dialMQTT(context.Background(), u, "")
	if err != nil {
		t.Fatal(err)
	}
	c := paho.NewClient(paho.ClientConfig{ClientID: "device-" + t.Name(), Conn: conn, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		if received != nil {
			received <- p.Packet
		}
		return true, nil
	}}})
	if _, err = c.Connect(context.Background(), &paho.Connect{ClientID: "device-" + t.Name(), CleanStart: true, KeepAlive: 30}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Disconnect(&paho.Disconnect{}) })
	return c
}

// A connection stays subscribed to its see channels: a subscription starts
// with each topic's last message and hears new ones; a member's publish
// reaches the broker with its request ID, and is audited.
func TestMQTTSeeAndControl(t *testing.T) {
	target := broker(t)
	h := setup(t, target, homeChannels)
	h.reconcile(t)
	eventually(t, "the connection to connect", func() bool { return h.svc.runtime.Status(mustFind(t, h)).State == StateConnected })
	if result, err := h.svc.Test(h.ctx, "alice", h.connection.ID); err != nil || !result.OK {
		t.Fatalf("test: %+v %v", result, err)
	}
	received := make(chan *paho.Publish, 10)
	device := rawClient(t, target, received)
	if _, err := device.Subscribe(h.ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "home/+/light/set", QoS: 1}}}); err != nil {
		t.Fatal(err)
	}
	// A reading published before anyone subscribes is the value a
	// dashboard opens with.
	if _, err := device.Publish(h.ctx, &paho.Publish{Topic: "home/living/sensor/temperature", QoS: 1, Payload: []byte(`21.5`)}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the hub to keep the reading", func() bool {
		l := h.svc.runtime.hubFor(h.connection.ID).listen("home/living/sensor/temperature")
		defer h.svc.runtime.hubFor(h.connection.ID).remove(l)
		m, _ := l.take()
		return len(m) == 1
	})
	got := &frames{}
	spec, _ := json.Marshal(SubscriptionSpec{Source: "connections", Workspace: h.ws, Key: h.connection.Key, Channel: "sensors", Filter: "home/+/sensor/temperature"})
	sub, err := h.svc.subscribe(h.ctx, httpapi.Request{Subject: "bob", DMS: h.s}, "s1", spec, got.deliver)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	eventually(t, "the current value", func() bool { return slices.Equal(got.payloads("home/living/sensor/temperature"), []string{"21.5"}) })
	if _, err = device.Publish(h.ctx, &paho.Publish{Topic: "home/living/sensor/temperature", QoS: 1, Payload: []byte(`{"c":22}`)}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the new value", func() bool { return len(got.payloads("home/living/sensor/temperature")) == 2 })
	if p := got.payloads("home/living/sensor/temperature"); p[1] != `{"c":22}` {
		t.Fatalf("payloads %v", p)
	}
	if err = sub.Check(h.ctx); err != nil {
		t.Fatal(err)
	}

	// Carol may not hear the sensors; a filter outside the channel is
	// refused.
	if _, err = h.svc.subscribe(h.ctx, httpapi.Request{Subject: "carol", DMS: h.s}, "s2", spec, got.deliver); problemCode(err) != "connection_forbidden" {
		t.Fatalf("a visitor subscribed: %v", err)
	}
	outside, _ := json.Marshal(SubscriptionSpec{Workspace: h.ws, Key: h.connection.Key, Channel: "sensors", Filter: "home/#"})
	if _, err = h.svc.subscribe(h.ctx, httpapi.Request{Subject: "bob", DMS: h.s}, "s3", outside, got.deliver); err == nil {
		t.Fatal("a filter wider than its channel")
	}

	tg, err := h.svc.controlTarget(h.ctx, h.s, "bob", h.ws, h.connection.Key, "lights")
	if err != nil {
		t.Fatal(err)
	}
	job, err := checkPublish(tg.channel, Publish{Topic: "home/kitchen/light/set", Payload: json.RawMessage(`{"on":true}`)}, MaxAppPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = checkPublish(tg.channel, Publish{Topic: "home/kitchen/sensor/x", Payload: json.RawMessage(`1`)}, MaxPayload); err == nil {
		t.Fatal("a topic outside the channel")
	}
	if _, err = checkPublish(tg.channel, Publish{Topic: "home/kitchen/light/set", Retain: true}, MaxPayload); err == nil {
		t.Fatal("a retained publish on a channel that does not retain")
	}
	out, err := h.svc.sendPublish(h.ctx, h.s, "bob", tg, job, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-received:
		if p.Topic != "home/kitchen/light/set" || string(p.Payload) != `{"on":true}` || p.Properties == nil || len(p.Properties.User) == 0 || p.Properties.User[0].Value != out.Request {
			t.Fatalf("the device got %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the device got nothing")
	}
	events, err := h.s.DAL.Audit().List(h.ctx, audits.Filter{WorkspaceID: h.ws, Action: "connection.publish"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(events, func(e audits.Event) bool { return e.Subject == "bob" && e.Details["topic"] == "home/kitchen/light/set" }) {
		t.Fatal("the publish was not audited")
	}

	// Disabling closes the session; a subscription ends its checks then.
	v, _ := h.svc.Get(h.ctx, "alice", h.connection.ID)
	off := false
	if _, err = h.svc.Replace(h.ctx, h.s, "alice", v.ID, v.Version, Save{Name: v.Name, Target: v.Target, Enabled: &off, Channels: v.Channels}); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	if _, err = h.svc.controlTarget(h.ctx, h.s, "bob", h.ws, h.connection.Key, "lights"); problemCode(err) != "connection_disabled" {
		t.Fatalf("control of a disabled connection: %v", err)
	}
}

func mustFind(t *testing.T, h *home) store.Connection {
	t.Helper()
	c, err := h.s.DAL.Connections().Find(h.ctx, h.connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// An HTTP channel's calls carry the connection's credential and signature,
// stay inside their path pattern, are answered in an envelope and, for a
// cached GET, from the cache.
func TestHTTPCalls(t *testing.T) {
	var mu sync.Mutex
	var hits []*http.Request
	var bodies []string
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits, bodies = append(hits, r), append(bodies, string(b))
		mu.Unlock()
		switch r.URL.Path {
		case "/api/status":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "session=x")
			_, _ = w.Write([]byte(`{"home":true}`))
		case "/api/heating/set":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("no"))
		case "/api/moved":
			http.Redirect(w, r, "http://elsewhere.example/", http.StatusFound)
		case "/api/cut":
			// Fewer bytes than announced: the answer stops after its headers.
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("ab"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer service.Close()
	channels := []Channel{{Key: "status", Path: "/status", Methods: []string{"GET"}, Cache: "30s"}, {Key: "heating", Path: "/heating/**", Methods: []string{"GET", "POST"}}, {Key: "moved", Path: "/moved", Methods: []string{"GET"}}, {Key: "cut", Path: "/cut", Methods: []string{"GET"}}}
	h := setup(t, "", nil)
	v, err := h.svc.Create(h.ctx, h.s, "alice", Save{WorkspaceID: h.ws, Kind: KindHTTP, Name: "Node-RED", Target: service.URL + "/api", Sign: true,
		Credentials: &Credentials{Type: CredentialBasic, Username: "papergo", Password: "pw"}, Headers: map[string]string{"X-Site": "home"}, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	if v.SigningSecret == "" {
		t.Fatal("no signing secret shown on creation")
	}
	h.connection = v
	h.reconcile(t)
	tg, err := dms.Read(h.ctx, h.s, func(tx *dms.Service) (target, error) {
		return authorize(h.ctx, tx, "bob", h.ws, v.Key, "status", UseSee)
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepareCall(tg.channel, Call{Path: "/status"}, MaxBody)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		answer, err := h.svc.sendCall(h.ctx, h.s, "bob", tg, p, "r1", "")
		if err != nil {
			t.Fatal(err)
		}
		if answer.Status != 200 || string(answer.Body) != `{"home":true}` || answer.Headers["set-cookie"] != "" || answer.Headers["content-type"] != "application/json" {
			t.Fatalf("%+v", answer)
		}
	}
	mu.Lock()
	first := hits[0]
	mu.Unlock()
	if len(hits) != 1 {
		t.Fatalf("a cached GET reached the service %d times", len(hits))
	}
	user, password, _ := first.BasicAuth()
	if user != "papergo" || password != "pw" || first.Header.Get("X-Site") != "home" || first.Header.Get("PaperGo-Subject") != "bob" || first.Header.Get("PaperGo-Workspace") != h.ws {
		t.Fatalf("headers %v", first.Header)
	}
	stamp, signature, _ := strings.Cut(strings.TrimPrefix(first.Header.Get("PaperGo-Signature"), "t="), ",v1=")
	at, _ := strconv.ParseInt(stamp, 10, 64)
	if signature != sign(v.SigningSecret, at, "bob", h.ws, nil) {
		t.Fatal("the signature does not verify")
	}

	heating, err := h.svc.controlTarget(h.ctx, h.s, "bob", h.ws, v.Key, "heating")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Call{{Method: "POST", Path: "/status"}, {Method: "POST", Path: "/heating/../status"}, {Method: "DELETE", Path: "/heating/x"}, {Method: "POST", Path: "/heating/x", Headers: map[string]string{"Authorization": "x"}}} {
		if _, err = prepareCall(heating.channel, bad, MaxBody); problemCode(err) != "connection_path_invalid" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	p, err = prepareCall(heating.channel, Call{Method: "POST", Path: "/heating/set?zone=1", Body: json.RawMessage(`{"target":21}`)}, MaxAppBody)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := h.svc.sendCall(h.ctx, h.s, "bob", heating, p, "r2", "")
	if err != nil || answer.Status != 403 || string(answer.Body) != `"no"` {
		t.Fatalf("the endpoint's refusal is its answer: %+v %v", answer, err)
	}
	mu.Lock()
	if bodies[1] != `{"target":21}` || hits[1].URL.RawQuery != "zone=1" || hits[1].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("sent %q %v", bodies[1], hits[1].URL)
	}
	mu.Unlock()
	moved, _ := dms.Read(h.ctx, h.s, func(tx *dms.Service) (target, error) {
		return authorize(h.ctx, tx, "bob", h.ws, v.Key, "moved", UseSee)
	})
	p, _ = prepareCall(moved.channel, Call{Path: "/moved"}, MaxBody)
	if _, err = h.svc.sendCall(h.ctx, h.s, "bob", moved, p, "r3", ""); problemCode(err) != "connection_failed" {
		t.Fatalf("a redirect: %v", err)
	}
	calls, err := h.svc.Calls(h.ctx, "alice", v.ID)
	if err != nil || len(calls) != 4 || calls[0].Path != "/moved" || !calls[2].Cached || calls[1].Path != "/heating/set" {
		t.Fatalf("calls %+v %v", calls, err)
	}
	// An answer that stops after its headers is the service's failure, a
	// problem like one that never came.
	cut, _ := dms.Read(h.ctx, h.s, func(tx *dms.Service) (target, error) {
		return authorize(h.ctx, tx, "bob", h.ws, v.Key, "cut", UseSee)
	})
	p, _ = prepareCall(cut.channel, Call{Path: "/cut"}, MaxBody)
	if _, err = h.svc.sendCall(h.ctx, h.s, "bob", cut, p, "r4", ""); problemCode(err) != "connection_failed" {
		t.Fatalf("an answer cut short: %v", err)
	}
	if result, err := h.svc.Test(h.ctx, "alice", v.ID); err != nil || !result.OK {
		t.Fatalf("test %+v %v", result, err)
	}
	// An HTTP target outside the operator's networks is refused.
	elsewhere := &Runtime{svc: h.svc, networks: nil}
	u, _ := url.Parse(service.URL)
	if err = elsewhere.testHTTP(h.ctx, mustFind(t, h), sealed{Target: service.URL}, u); err == nil || !strings.Contains(explain(err), "connections.networks") {
		t.Fatalf("loopback outside the networks: %v", err)
	}
}

// Packages carry a connection's key, kind, settings and channels, never its
// target or credentials; an imported connection is disabled until a
// manager gives it a target again.
func TestConnectionsTravelWithoutSecrets(t *testing.T) {
	h := setup(t, "", nil)
	channels := slices.Clone(homeChannels)
	channels[2].Access = map[Use]*Access{UseControl: {Level: LevelWrite, On: h.list}}
	if _, err := h.svc.Create(h.ctx, h.s, "alice", Save{WorkspaceID: h.ws, Kind: KindMQTT, Name: "Home", Target: "mqtt://127.0.0.1:1883", Credentials: &Credentials{Username: "papergo", Password: "secret"}, Channels: channels}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Create(h.ctx, h.s, "alice", Save{WorkspaceID: h.ws, Kind: KindHTTP, Name: "Node-RED", Target: "http://127.0.0.1:1880", Credentials: &Credentials{Type: CredentialBearer, Token: "token"}, Sign: true, Channels: []Channel{{Key: "status", Path: "/status", Methods: []string{"GET"}}}}); err != nil {
		t.Fatal(err)
	}
	part := h.svc.part()
	raw, err := part.Export(h.ctx, h.s, h.ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret", "token", "127.0.0.1", "papergo"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the package carries %q: %s", secret, raw)
		}
	}
	copyWS, err := h.s.Create(h.ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Copy"})
	if err != nil {
		t.Fatal(err)
	}
	copyList, err := h.s.Create(h.ctx, "alice", copyWS.ID, dms.CreateResource{Kind: "list", Name: "Facilities"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{h.ws: copyWS.ID, h.list: copyList.ID}
	remapped, err := part.Remap(raw, func(id string) (string, bool) { v, ok := ids[id]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Remap(raw, func(id string) (string, bool) { return id, id == h.ws }); err == nil {
		t.Fatal("a list the package does not carry was kept")
	}
	var warnings []string
	if err = h.s.Write(h.ctx, func(tx *dms.Service) error {
		warnings, err = part.Import(h.ctx, tx, "alice", remapped)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings %v", warnings)
	}
	list, err := h.svc.List(h.ctx, "alice", copyWS.ID)
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	for _, c := range list {
		if c.Enabled || c.Target != "" || c.Credentials.Username != "" || c.Status.State != StateDisabled {
			t.Fatalf("an imported connection: %+v", c)
		}
		if c.Kind == KindMQTT && c.Channels[2].Access[UseControl].On != copyList.ID {
			t.Fatalf("access borrowed from %s", c.Channels[2].Access[UseControl].On)
		}
		if c.Kind == KindHTTP && !c.Sign {
			t.Fatal("settings were lost")
		}
	}
	mqtt := list[0]
	if mqtt.Kind != KindMQTT {
		mqtt = list[1]
	}
	on := true
	if _, err = h.svc.Replace(h.ctx, h.s, "alice", mqtt.ID, mqtt.Version, Save{Name: mqtt.Name, Target: "mqtt://127.0.0.1:1884", Enabled: &on, Channels: mqtt.Channels}); err != nil {
		t.Fatal(err)
	}
	// An organization package carries the recycle bin: a connection of a
	// workspace in it is left out, and the import goes on.
	gone, err := h.s.Create(h.ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Gone"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.Delete(h.ctx, "alice", gone.ID, gone.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = h.s.Write(h.ctx, func(tx *dms.Service) error {
		warnings, err = part.Import(h.ctx, tx, "alice", marshal([]portable{{WorkspaceID: gone.ID, Key: "home", Name: "Home", Kind: KindMQTT, Channels: []Channel{}}}))
		return err
	}); err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "recycle bin") {
		t.Fatalf("a connection of a deleted workspace: %v %v", warnings, err)
	}
}

// A solution declares its connections: installing it creates them
// disabled, without an address, with access borrowed from its collections;
// a later version changes their channels and keeps what a manager set.
func TestSolutionsDeclareConnections(t *testing.T) {
	h := setup(t, "", nil)
	manifest := func(version, topics string) fstest.MapFS {
		return fstest.MapFS{"solution.json": {Data: []byte(`{"key": "home", "name": "Home", "version": "` + version + `",
  "fields": [{"key": "home_room", "label": "Room", "type": "text"}],
  "content_types": [{"key": "home_device", "name": "Device", "fields": ["home_room"]}],
  "collections": [{"key": "facilities", "kind": "list", "name": "Facilities", "content_types": ["home_device"]}],
  "parts": {"connections": [{"key": "broker", "name": "Home broker", "kind": "mqtt", "channels": [
    {"key": "heating", "topics": "` + topics + `", "uses": ["see", "control"], "access": {"control": {"level": "write", "on": "$collection:facilities"}}}
  ]}]}}`)}}
	}
	sol, problems := solutions.Load(manifest("1.0.0", "boiler/#"))
	if len(problems) > 0 {
		t.Fatalf("problems:\n%s", problems)
	}
	if bad, _ := solutions.Load(manifest("1.0.0", "$SYS/#")); len(solutions.Check(h.ctx, bad)) == 0 {
		t.Fatal("a channel on the broker's own topics was not refused")
	}
	installer := &solutions.Service{DMS: h.s, PaperGo: "v0.4.0"}
	result, err := installer.Apply(h.ctx, "alice", h.ws, sol, solutions.Options{})
	if err != nil || !result.Applied {
		t.Fatalf("%+v %v", result, err)
	}
	list, err := h.svc.List(h.ctx, "alice", h.ws)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	c := list[0]
	if c.Key != "broker" || c.Enabled || c.Target != "" || c.Channels[0].Access[UseControl].On == "" || strings.HasPrefix(c.Channels[0].Access[UseControl].On, "$") {
		t.Fatalf("installed %+v", c)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "Give connection broker its address") {
		t.Fatalf("warnings %v", result.Warnings)
	}
	// The manager gives it an address; the next version changes its topics
	// and keeps the address.
	on := true
	if _, err = h.svc.Replace(h.ctx, h.s, "alice", c.ID, c.Version, Save{Name: c.Name, Target: "mqtt://127.0.0.1:1883", Enabled: &on, Channels: c.Channels}); err != nil {
		t.Fatal(err)
	}
	next, _ := solutions.Load(manifest("1.1.0", "heating/#"))
	if result, err = installer.Apply(h.ctx, "alice", h.ws, next, solutions.Options{}); err != nil || !result.Applied {
		t.Fatalf("%+v %v", result, err)
	}
	updated, err := h.svc.Get(h.ctx, "alice", c.ID)
	if err != nil || updated.Channels[0].Topics != "heating/#" || updated.Target != "mqtt://127.0.0.1:1883" || !updated.Enabled {
		t.Fatalf("updated %+v %v", updated, err)
	}
}

// The Home sample is a solution with no problems: it installs its
// connections disabled, its app and its workflow, whose connection.message
// trigger checks against the connection it creates, and installing it
// again changes nothing.
func TestHomeSampleInstalls(t *testing.T) {
	h := setup(t, "", nil)
	sol, problems := solutions.Load(os.DirFS("../../samples/home"))
	if problems = append(problems, solutions.Check(h.ctx, sol)...); len(problems) > 0 {
		t.Fatalf("problems:\n%s", problems)
	}
	installer := &solutions.Service{DMS: h.s, PaperGo: "v0.4.0"}
	result, err := installer.Apply(h.ctx, "alice", h.ws, sol, solutions.Options{})
	if err != nil || !result.Applied {
		t.Fatalf("%+v %v", result, err)
	}
	list, err := h.svc.List(h.ctx, "alice", h.ws)
	if err != nil || len(list) != 2 {
		t.Fatal(list, err)
	}
	again, err := installer.Apply(h.ctx, "alice", h.ws, sol, solutions.Options{})
	if err != nil || !again.Applied || len(again.Changes) != 0 {
		t.Fatalf("applied again: %+v %v", again, err)
	}
}
