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
	commentstore "papergo/internal/dal/comments"
	"papergo/internal/dal/live"
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
	// A removal of an item the stream showed says where the item was, as it
	// can no longer send the item.
	edited, err := s.Get(ctx, "alice", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "alice", item.ID, edited.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	for {
		event = readFrame(t, replayReader)
		if event.kind == "item" {
			break
		}
	}
	var removal map[string]any
	if err = json.Unmarshal([]byte(event.data), &removal); err != nil {
		t.Fatal(err)
	}
	if removal["id"] != item.ID || removal["workspace_id"] != w.ID || removal["container_id"] != l.ID || removal["@removed"] == nil {
		t.Fatal("a removal frame", event.data)
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
	// Marking everything read is one change of the whole inbox, whatever
	// it holds, told in one frame without an id.
	for _, title := range []string{"One", "Two", "Three"} {
		send("alice", title)
	}
	for range 3 {
		if created := next(); created["action"] != "created" {
			t.Fatalf("created frame: %v", created)
		}
	}
	if marked, err := (&notify.Service{DMS: s}).MarkRead(ctx, "alice", nil, nil); err != nil || marked != 3 {
		t.Fatalf("marked %d, %v", marked, err)
	}
	if all := next(); all["id"] != nil || all["action"] != "updated" || all["unread"] != float64(0) {
		t.Fatalf("frame of the whole inbox: %v", all)
	}
}

// liveModule turns the changes of its kind into frames, leaving out updates.
type liveModule struct {
	name string
	kind live.Kind
}

func (m liveModule) Name() string        { return m.name }
func (liveModule) Routes() []Route       { return nil }
func (m liveModule) LiveKind() live.Kind { return m.kind }
func (m liveModule) LiveBatch(_ *http.Request, in Request) func(live.Change) (*LiveFrame, error) {
	return func(change live.Change) (*LiveFrame, error) {
		if change.Action == live.ActionUpdated {
			return nil, nil
		}
		return &LiveFrame{Event: "module", Data: map[string]any{"id": change.EntityID, "subject": in.Subject}}, nil
	}
}

// liveStream opens the live stream of an API with modules, and creates what
// a discussion needs to change.
func liveStream(t *testing.T, s *dms.Service, modules ...Module) (*bufio.Reader, string) {
	t.Helper()
	previousInterval := livePollInterval
	livePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { livePollInterval = previousInterval })
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second, Modules: modules}
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "L"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "I"})
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
	t.Cleanup(func() { stream.Body.Close() })
	reader := bufio.NewReader(stream.Body)
	readFrame(t, reader)
	return reader, item.ID
}

// A module's changes reach the stream as its frames, for the stream's
// caller, unless the module leaves them out.
func TestLiveModuleFrames(t *testing.T) {
	_, s, _ := setup(t)
	reader, item := liveStream(t, s, liveModule{"discussions", live.KindComment})
	ctx := t.Context()
	repo := s.DAL.Comments()
	first, err := repo.Create(ctx, commentstore.Comment{ItemID: item, Author: "alice", Source: commentstore.SourceUser, Body: "One"})
	if err != nil {
		t.Fatal(err)
	}
	next := func() sseFrame {
		t.Helper()
		for {
			if frame := readFrame(t, reader); frame.kind != "keepalive" && frame.kind != "item" {
				return frame
			}
		}
	}
	if frame := next(); frame.kind != "module" || !strings.Contains(frame.data, first.ID) || !strings.Contains(frame.data, `"subject":"alice"`) {
		t.Fatalf("module frame: %+v", frame)
	}
	if _, _, err = repo.Edit(ctx, first.ID, first.Version, "Edited", nil); err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(ctx, commentstore.Comment{ItemID: item, Author: "alice", Source: commentstore.SourceUser, Body: "Two"})
	if err != nil {
		t.Fatal(err)
	}
	if frame := next(); frame.kind != "module" || !strings.Contains(frame.data, second.ID) {
		t.Fatalf("the frame after one the module left out: %+v", frame)
	}
}

// A change of a kind no module serves reaches nobody, as when the feature
// that serves it is off.
func TestLiveKindWithoutModule(t *testing.T) {
	_, s, _ := setup(t)
	reader, item := liveStream(t, s)
	ctx := t.Context()
	if _, err := s.DAL.Comments().Create(ctx, commentstore.Comment{ItemID: item, Author: "alice", Source: commentstore.SourceUser, Body: "Unheard"}); err != nil {
		t.Fatal(err)
	}
	// The stream reads on past the change, and only an item's frame comes.
	current, err := s.Get(ctx, "alice", item)
	if err != nil {
		t.Fatal(err)
	}
	later, err := s.Create(ctx, "alice", *current.ContainerID, dms.CreateResource{Kind: "item", Name: "Later"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		frame := readFrame(t, reader)
		if frame.kind == "comment" || frame.kind == "module" {
			t.Fatalf("a kind no module serves reached the stream: %+v", frame)
		}
		if frame.kind == "item" && strings.Contains(frame.data, later.ID) {
			return
		}
	}
}

// Two modules cannot serve one kind of live change.
func TestLiveKindsAreServedOnce(t *testing.T) {
	_, s, _ := setup(t)
	a := &API{DMS: s, Logger: slog.Default(), Modules: []Module{liveModule{"one", live.KindComment}, liveModule{"two", live.KindComment}}}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "live kind comment") {
		t.Fatalf("Validate = %v", err)
	}
}

// A stream opened with an access token hears only of deletions of
// notifications it showed, so it learns nothing of those it may not see.
func TestLiveNotificationDeletionsKeepToAToken(t *testing.T) {
	_, s, _ := setup(t)
	previousInterval := livePollInterval
	livePollInterval = 20 * time.Millisecond
	t.Cleanup(func() { livePollInterval = previousInterval })
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Stream", Scopes: []string{"workspace.read", "item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	send := func(workspace, title string) string {
		t.Helper()
		n, _, err := s.DAL.Notifications().Create(ctx, notifications.Notification{Recipient: "alice", WorkspaceID: workspace, Type: notifications.TypeMessage, Title: title, Source: notifications.SourceUser, Sender: "carol"})
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	// The token may not see a notification about no workspace.
	hidden := send("", "Hidden")
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Secret)
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	readFrame(t, reader)
	if err = s.Write(ctx, func(t *dms.Service) error { return t.DAL.Notifications().Delete(ctx, "alice", hidden) }); err != nil {
		t.Fatal(err)
	}
	shown := send(ws.ID, "Shown")
	next := func() sseFrame {
		t.Helper()
		for {
			if frame := readFrame(t, reader); frame.kind != "keepalive" {
				return frame
			}
		}
	}
	if frame := next(); frame.kind != "notification" || !strings.Contains(frame.data, shown) || strings.Contains(frame.data, hidden) {
		t.Fatalf("the token heard of a hidden deletion: %+v", frame)
	}
	if err = s.Write(ctx, func(t *dms.Service) error { return t.DAL.Notifications().Delete(ctx, "alice", shown) }); err != nil {
		t.Fatal(err)
	}
	if frame := next(); frame.kind != "notification" || !strings.Contains(frame.data, shown) || !strings.Contains(frame.data, `"action":"deleted"`) {
		t.Fatalf("the token missed the deletion of what it showed: %+v", frame)
	}
}
