package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dms"
)

// echoSource delivers one frame naming each subscription's spec, and loses
// access when lost is set.
type echoSource struct {
	lost   *atomic.Bool
	closed *atomic.Int32
}

func (echoSource) Name() string               { return "echo" }
func (echoSource) Routes() []Route            { return nil }
func (echoSource) SubscriptionSource() string { return "echo" }

func (e echoSource) Subscribe(ctx context.Context, in Request, id string, spec json.RawMessage, deliver Deliver) (Subscription, error) {
	var body struct {
		Refuse bool `json:"refuse"`
	}
	_ = json.Unmarshal(spec, &body)
	if body.Refuse {
		return nil, dms.ErrForbidden
	}
	if err := deliver(ctx, LiveFrame{Event: "echo", Data: map[string]any{"subscription": id, "subject": in.Subject, "spec": spec}}); err != nil {
		return nil, err
	}
	return echoSubscription(e), nil
}

type echoSubscription echoSource

func (e echoSubscription) Check(context.Context) error {
	if e.lost.Load() {
		return dms.ErrForbidden
	}
	return nil
}

func (e echoSubscription) Close() { e.closed.Add(1) }

// A stream's caller subscribes it by the ID its connected frame names; the
// module's frames reach that stream, and a subscription whose access is
// lost ends with an access frame.
func TestLiveSubscriptions(t *testing.T) {
	_, s, _ := setup(t)
	previous := subscriptionCheck
	subscriptionCheck = 50 * time.Millisecond
	t.Cleanup(func() { subscriptionCheck = previous })
	source := echoSource{lost: &atomic.Bool{}, closed: &atomic.Int32{}}
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 4, RequestTimeout: 5 * time.Second, Modules: []Module{source}}
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Body.Close() })
	reader := bufio.NewReader(stream.Body)
	var connected struct {
		Stream string `json:"stream"`
	}
	if err = json.Unmarshal([]byte(readFrame(t, reader).data), &connected); err != nil || connected.Stream == "" {
		t.Fatalf("the connected frame names no stream: %v", err)
	}
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	subscriptions := "/v1/events/" + connected.Stream + "/subscriptions"
	if status, _ := call("POST", "/v1/events/nonexistent/subscriptions", `{"source":"echo"}`); status != 404 {
		t.Fatalf("another stream: %d", status)
	}
	if status, _ := call("POST", subscriptions, `{"source":"nobody"}`); status != 422 {
		t.Fatalf("an unknown source: %d", status)
	}
	if status, _ := call("POST", subscriptions, `{"source":"echo","refuse":true}`); status != 403 {
		t.Fatalf("a refused subscription: %d", status)
	}
	status, created := call("POST", subscriptions, `{"source":"echo","x":1}`)
	if status != 201 || created["id"] == "" {
		t.Fatalf("subscribe: %d %v", status, created)
	}
	id := created["id"].(string)
	next := func(kind string) map[string]any {
		t.Helper()
		for {
			f := readFrame(t, reader)
			if f.kind == "keepalive" {
				continue
			}
			if f.kind != kind {
				t.Fatalf("frame %s %s, want %s", f.kind, f.data, kind)
			}
			var data map[string]any
			_ = json.Unmarshal([]byte(f.data), &data)
			return data
		}
	}
	if echo := next("echo"); echo["subscription"] != id || echo["subject"] != "alice" {
		t.Fatalf("echo frame %v", echo)
	}
	status, second := call("POST", subscriptions, `{"source":"echo"}`)
	if status != 201 {
		t.Fatal(status)
	}
	next("echo")
	if status, _ = call("DELETE", subscriptions+"/"+second["id"].(string), ""); status != 204 {
		t.Fatalf("unsubscribe: %d", status)
	}
	if status, _ = call("DELETE", subscriptions+"/"+second["id"].(string), ""); status != 404 {
		t.Fatalf("unsubscribe twice: %d", status)
	}
	source.lost.Store(true)
	if lost := next("access"); lost["subscription"] != id {
		t.Fatalf("access frame %v", lost)
	}
	if source.closed.Load() != 2 {
		t.Fatalf("%d subscriptions closed, want 2", source.closed.Load())
	}
}
