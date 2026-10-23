package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"papergo/internal/auth"
	"papergo/internal/comments"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/notify"
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
	token, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Stream", Scopes: []string{"workspace.read", "item.read"}})
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
	if err = s.Directory().RevokeAccessToken(ctx, "alice", token.Token.ID); err != nil {
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

// A notification reaches only its recipient's streams, with their unread
// count, as it arrives, is read and is deleted, wherever it was delivered.
func TestLiveNotificationsReachTheirRecipient(t *testing.T) {
	_, s, _ := setup(t)
	previousInterval := livePollInterval
	livePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { livePollInterval = previousInterval })
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// One notification arrives before the stream opens, as on another device.
	early, _, err := s.DAL.Notifications().Create(ctx, notifications.Notification{Recipient: "alice", Type: notifications.TypeMessage, Title: "Earlier", Source: notifications.SourceUser, Sender: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	readFrame(t, reader)
	send := func(recipient, title string) string {
		t.Helper()
		n, _, err := s.DAL.Notifications().Create(ctx, notifications.Notification{Recipient: recipient, Type: notifications.TypeMessage, Title: title, Source: notifications.SourceUser, Sender: "carol"})
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	send("bob", "For Bob")
	mine := send("alice", "For Alice")
	next := func() map[string]any {
		t.Helper()
		for {
			frame := readFrame(t, reader)
			if frame.kind == "keepalive" {
				continue
			}
			if frame.kind != "notification" {
				t.Fatalf("unexpected frame: %+v", frame)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(frame.data), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	created := next()
	if created["id"] != mine || created["action"] != "created" || created["unread"] != float64(2) || !strings.Contains(fmt.Sprint(created["notification"]), "For Alice") {
		t.Fatalf("created frame: %v", created)
	}
	if _, err = (&notify.Service{DMS: s}).MarkRead(ctx, "alice", []string{mine}, nil); err != nil {
		t.Fatal(err)
	}
	if read := next(); read["action"] != "updated" || read["unread"] != float64(1) {
		t.Fatalf("read frame: %v", read)
	}
	if err = (&notify.Service{DMS: s}).Delete(ctx, "alice", mine); err != nil {
		t.Fatal(err)
	}
	if deleted := next(); deleted["id"] != mine || deleted["action"] != "deleted" || deleted["notification"] != nil {
		t.Fatalf("deleted frame: %v", deleted)
	}
	// Deleting one the stream never delivered still reaches it, with the
	// new count.
	if err = (&notify.Service{DMS: s}).Delete(ctx, "alice", early.ID); err != nil {
		t.Fatal(err)
	}
	if deleted := next(); deleted["id"] != early.ID || deleted["action"] != "deleted" || deleted["unread"] != float64(0) {
		t.Fatalf("deleted frame of an earlier notification: %v", deleted)
	}
}

// A comment frame tells people who may read the discussion what changed.
func TestLiveCommentFrames(t *testing.T) {
	_, s, _ := setup(t)
	previousInterval := livePollInterval
	livePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { livePollInterval = previousInterval })
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	mine, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.Create(ctx, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Theirs"})
	if err != nil {
		t.Fatal(err)
	}
	item := func(subject, workspace string) string {
		t.Helper()
		l, err := s.Create(ctx, subject, workspace, dms.CreateResource{Kind: "list", Name: "L"})
		if err != nil {
			t.Fatal(err)
		}
		i, err := s.Create(ctx, subject, l.ID, dms.CreateResource{Kind: "item", Name: "I"})
		if err != nil {
			t.Fatal(err)
		}
		return i.ID
	}
	hidden, shown := item("bob", theirs.ID), item("alice", mine.ID)
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	readFrame(t, reader)
	svc := &comments.Service{DMS: s, Enabled: true}
	if _, err = svc.Create(ctx, "bob", "", hidden, comments.Input{Body: "Private"}); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Create(ctx, "alice", "", shown, comments.Input{Body: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		frame := readFrame(t, reader)
		if frame.kind == "keepalive" || frame.kind == "item" {
			continue
		}
		if frame.kind != "comment" || !strings.Contains(frame.data, c.ID) || !strings.Contains(frame.data, shown) || !strings.Contains(frame.data, `"action":"created"`) {
			t.Fatalf("comment frame: %+v", frame)
		}
		break
	}
}
