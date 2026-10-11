package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
	"testing"
	"time"
)

type sseFrame struct{ kind, id, data string }

func readFrame(t *testing.T, r *bufio.Reader) sseFrame {
	t.Helper()
	var frame sseFrame
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return frame
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			frame.kind = value
		}
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			frame.id = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			frame.data = value
		}
	}
}

func TestLiveStreamDeliveryReconnectAndCredentialRevocation(t *testing.T) {
	_, s, _ := setup(t)
	previousInterval := livePollInterval
	livePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { livePollInterval = previousInterval })
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second}
	handler := a.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "L"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateRESTToken(ctx, "alice", identity.RESTTokenInput{Name: "Stream", Scopes: []string{"workspace.read", "list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	open := func(credential, cursor string) *http.Response {
		req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+credential)
		if cursor != "" {
			req.Header.Set("Last-Event-ID", cursor)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			t.Fatal(res.StatusCode, string(body))
		}
		return res
	}
	stream := open(token.Secret, "")
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	connected := readFrame(t, reader)
	if connected.kind != "connected" || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal(connected, stream.Header)
	}
	// An open stream leaves the ordinary request slot available. Read-deadline
	// exemption is asserted directly by TestQueriesReceiveTheReadDeadline.
	res := request(handler, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	name := "Edited"
	if _, err = s.Update(ctx, "alice", item.ID, item.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	event := readFrame(t, reader)
	if event.kind != "item" || !strings.Contains(event.data, `"name":"Edited"`) {
		t.Fatal(event)
	}
	replay := open(testToken, connected.id)
	defer replay.Body.Close()
	replayReader := bufio.NewReader(replay.Body)
	readFrame(t, replayReader)
	event = readFrame(t, replayReader)
	if event.kind != "item" || !strings.Contains(event.data, item.ID) {
		t.Fatal(event)
	}
	if err = s.Directory().RevokeRESTToken(ctx, "alice", token.Token.ID); err != nil {
		t.Fatal(err)
	}
	for {
		event = readFrame(t, reader)
		if event.kind == "reauthenticate" {
			break
		}
	}
	// Frame data is valid JSON; credential details never appear.
	if !json.Valid([]byte(event.data)) || strings.Contains(event.data, token.Secret) {
		t.Fatal(event)
	}
}
