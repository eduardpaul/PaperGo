package scriptrun

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"

	"papergo/internal/connections"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/integration"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

// A connection.message trigger starts a run for an MQTT message that meets
// its condition, at most once per topic in its interval; the run sees the
// message and publishes through the connection as an operation it awaits.
func TestConnectionMessagesStartRuns(t *testing.T) {
	target := os.Getenv("PAPERGO_TEST_MQTT_URL")
	if target == "" {
		t.Skip("PAPERGO_TEST_MQTT_URL names no MQTT broker")
	}
	var svc *connections.Service
	r := run(t, func(s *dms.Service, _ storage.Store) httpapi.Module {
		secrets, err := integration.NewSecrets(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		svc = connections.New(connections.Config{DMS: s, Secrets: secrets, Workflows: &workflow.Service{DMS: s}, Networks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err := svc.Register(s); err != nil {
			t.Fatal(err)
		}
		_ = svc.Start(context.Background())
		t.Cleanup(svc.Close)
		return svc.Module()
	})
	ctx := t.Context()
	directory := identity.Directory{DB: r.s.Directory().DB}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Home", Scopes: []string{connections.ManageScope, connections.ControlScope, "workspace.read", "item.read", "item.write", "list.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Create(ctx, r.s, "alice", connections.Save{WorkspaceID: r.ws, Kind: connections.KindMQTT, Key: "home", Name: "Home", Target: target, Channels: []connections.Channel{
		{Key: "sensors", Topics: "home/+/sensor/#", Uses: []connections.Use{connections.UseSee}},
		{Key: "lights", Topics: "home/+/light/set", Uses: []connections.Use{connections.UseControl}},
	}}); err != nil {
		t.Fatal(err)
	}
	// A trigger on another connection's key is refused when it is saved.
	if _, err = r.wf.Create(ctx, "alice", r.ws, workflow.Save{Name: "Nowhere", Source: `import { workflow } from "@papergo/workflow";
export default workflow({ triggers: [{ type: "connection.message", options: { connection: "garage", channel: "sensors" } }], async run() {} });`}); err == nil || !strings.Contains(err.Error(), "garage") {
		t.Fatalf("a trigger on a missing connection: %v", err)
	}
	w := r.workflow(t, "Too warm", `import { json, workflow } from "@papergo/workflow";
export default workflow({
  triggers: [{ type: "connection.message", options: { connection: "home", channel: "sensors", topic: "home/+/sensor/temperature", min_interval: "1h", condition: { path: "c", op: "gt", value: 25 } } }],
  async run(ctx) {
    const data = ctx.run.event.data as { topic: string; payload: { c: number } };
    const light = data.topic.split("/")[1];
    const published = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).connections.byKey("home").channels.byChannel("lights").publish.post({ topic: "home/" + light + "/light/set", payload: json({ on: true, because: data.payload.c }) });
    const done = await ctx.wait(published!.operationId!);
    return { done: done.status };
  },
});`)
	maintenance := svc.Maintenance()
	if err = maintenance.Run(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	received := make(chan *paho.Publish, 10)
	u, _ := url.Parse(target)
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
	if strings.HasPrefix(target, "ws") {
		t.Skip("the device in this test speaks MQTT over TCP")
	}
	if err != nil {
		t.Fatal(err)
	}
	device := paho.NewClient(paho.ClientConfig{ClientID: "device-trigger", Conn: conn, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
		received <- p.Packet
		return true, nil
	}}})
	if _, err = device.Connect(ctx, &paho.Connect{ClientID: "device-trigger", CleanStart: true, KeepAlive: 30}); err != nil {
		t.Fatal(err)
	}
	defer device.Disconnect(&paho.Disconnect{})
	if _, err = device.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "home/+/light/set", QoS: 1}}}); err != nil {
		t.Fatal(err)
	}
	// Wait for the connection's own subscription before publishing.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		v, err := svc.Use(ctx, "alice", r.ws, "home")
		if err == nil && v.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the connection did not connect")
		}
	}
	time.Sleep(300 * time.Millisecond)
	for _, reading := range []string{`{"c":20}`, `{"c":30}`, `{"c":31}`} {
		if _, err = device.Publish(ctx, &paho.Publish{Topic: "home/living/sensor/temperature", QoS: 1, Payload: []byte(reading)}); err != nil {
			t.Fatal(err)
		}
	}
	var runID string
	for deadline := time.Now().Add(15 * time.Second); runID == ""; time.Sleep(50 * time.Millisecond) {
		rows, err := r.s.DAL.Runs().List(ctx, runstore.Filter{WorkflowID: w.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 1 {
			t.Fatalf("%d runs; the interval allows one", len(rows))
		}
		if len(rows) == 1 {
			runID = rows[0].ID
		}
		if time.Now().After(deadline) {
			t.Fatal("no run started")
		}
	}
	finished := r.await(t, runID, runstore.StatusCompleted)
	if !strings.Contains(string(finished.Result), "succeeded") {
		t.Fatalf("result %s", finished.Result)
	}
	select {
	case p := <-received:
		if p.Topic != "home/living/light/set" || string(p.Payload) != `{"on":true,"because":30}` {
			t.Fatalf("the light got %s %s", p.Topic, p.Payload)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the light got nothing")
	}
	rows, _ := r.s.DAL.Runs().List(ctx, runstore.Filter{WorkflowID: w.ID})
	if len(rows) != 1 {
		t.Fatalf("%d runs; the interval allows one", len(rows))
	}
}

// A run's call is an operation it awaits; a GET or HEAD through call is
// refused, as the request is inside the step's write transaction.
func TestRunCallsControlOnly(t *testing.T) {
	var hits atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer service.Close()
	var svc *connections.Service
	r := run(t, func(s *dms.Service, _ storage.Store) httpapi.Module {
		secrets, err := integration.NewSecrets(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		svc = connections.New(connections.Config{DMS: s, Secrets: secrets, Workflows: &workflow.Service{DMS: s}, Networks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err := svc.Register(s); err != nil {
			t.Fatal(err)
		}
		_ = svc.Start(context.Background())
		t.Cleanup(svc.Close)
		return svc.Module()
	})
	ctx := t.Context()
	directory := identity.Directory{DB: r.s.Directory().DB}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Home", Scopes: []string{connections.ManageScope, connections.ControlScope, "workspace.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Create(ctx, r.s, "alice", connections.Save{WorkspaceID: r.ws, Kind: connections.KindHTTP, Key: "nodered", Name: "Node-RED", Target: service.URL, Channels: []connections.Channel{
		{Key: "heating", Path: "/heating/**", Methods: []string{"GET", "POST"}},
	}}); err != nil {
		t.Fatal(err)
	}
	maintenance := svc.Maintenance()
	if err = maintenance.Run(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	script := func(method string) string {
		return `import { workflow } from "@papergo/workflow";
export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    const called = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).connections.byKey("nodered").channels.byChannel("heating").call.post({ method: "` + method + `", path: "/heating/set" });
    const done = await ctx.wait(called!.operationId!);
    return { done: done.status };
  },
});`
	}
	failed := r.await(t, r.start(t, r.workflow(t, "Reads through call", script("GET")), nil), runstore.StatusFailed)
	if !strings.Contains(failed.Error, "a run reads with fetch") {
		t.Fatalf("a run's GET through call failed with %q", failed.Error)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the service was called %d times for a refused call", n)
	}
	finished := r.await(t, r.start(t, r.workflow(t, "Sets heating", script("POST")), nil), runstore.StatusCompleted)
	if !strings.Contains(string(finished.Result), "succeeded") || hits.Load() != 1 {
		t.Fatalf("result %s after %d calls", finished.Result, hits.Load())
	}
}
