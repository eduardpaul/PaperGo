package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"papergo/internal/dal/directory"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/notify"
	"papergo/internal/tasks"
)

func TestNotificationRoutes(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Contracts"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Lease"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []dms.WorkspaceMember{{Subject: "bob", Role: "member"}, {Subject: "dave", Role: "visitor"}} {
		if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	// carol is in the group but not the workspace.
	group, err := s.DAL.Directory().CreateGroup(ctx, "legal", "Legal", "", directory.SourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"bob", "carol"} {
		if err = s.DAL.Directory().SetMember(ctx, group.ID, directory.Principal{Type: directory.PrincipalUser, ID: member}, false); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(subject string) []notify.View {
		t.Helper()
		page, err := (&notify.Service{DMS: s}).Inbox(ctx, subject, notify.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		return page.Data
	}
	const json_ = "application/json"

	// Assigning a task tells its assignees, not the person who assigned it,
	// and a group's members only when they can review it.
	w := request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"item_id": "`+item.ID+`", "title": "Check the lease", "assignees": ["alice", "group:`+group.ID+`"]}`, testToken, "", json_)
	if w.Code != 201 {
		t.Fatalf("create task: %d %s", w.Code, w.Body)
	}
	var task tasks.View
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	got := inbox("bob")
	if len(got) != 1 || got[0].Type != notifications.TypeTaskAssigned || got[0].TaskID == nil || *got[0].TaskID != task.ID || got[0].Title != "Check the lease" || *got[0].Sender != "alice" {
		t.Fatalf("bob was not told of the task: %+v", got)
	}
	if len(inbox("alice")) != 0 || len(inbox("carol")) != 0 {
		t.Fatalf("the assigner or someone who cannot review was told: %+v %+v", inbox("alice"), inbox("carol"))
	}
	// Reassigning tells only the people it adds.
	if w = request(h, "POST", "/v1/tasks/"+task.ID+"/reassign", `{"assignees": ["group:`+group.ID+`", "dave"]}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("a visitor cannot read the draft: %d %s", w.Code, w.Body)
	}
	// Answering tells the creator of a person's task.
	if _, err = (&tasks.Service{DMS: s}).Respond(ctx, "bob", task.ID, tasks.Response{Outcome: "done", Comment: "Signed copy is fine"}); err != nil {
		t.Fatal(err)
	}
	got = inbox("alice")
	if len(got) != 1 || got[0].Type != notifications.TypeTaskCompleted || got[0].Body != "bob: Done\n\nSigned copy is fine" {
		t.Fatalf("alice was not told of the answer: %+v", got)
	}
	if len(inbox("bob")) != 1 {
		t.Fatalf("the person who answered was told: %+v", inbox("bob"))
	}

	// Sending reaches the people who can read the item and counts the others.
	send := func(body string) (int, notify.Result, string) {
		w := request(h, "POST", "/v1/workspaces/"+ws.ID+"/notifications", body, testToken, "", json_)
		var out notify.Result
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Body.String()
	}
	message := `{"recipients": ["group:` + group.ID + `", "alice"], "title": "Lease renewed", "body": "Through 2031", "item_id": "` + item.ID + `", "dedup_key": "lease-renewed"}`
	if code, out, body := send(message); code != 200 || out != (notify.Result{Sent: 2, Skipped: 1}) {
		t.Fatalf("send: %d %s", code, body)
	}
	if code, out, body := send(message); code != 200 || out != (notify.Result{Skipped: 1, Deduplicated: 2}) {
		t.Fatalf("send again: %d %s", code, body)
	}
	for body, want := range map[string]string{
		`{"recipients": ["nobody"], "title": "Hi"}`:                          "unknown recipients",
		`{"recipients": ["bob"], "title": " "}`:                              "title",
		`{"title": "Hi"}`:                                                    "recipients",
		`{"recipients": ["bob"], "title": "Hi", "item_id": "` + ws.ID + `"}`: "item_id",
	} {
		if code, _, got := send(body); code != 422 || !strings.Contains(got, want) {
			t.Fatalf("send %s: %d %s", body, code, got)
		}
	}
	if code, _, _ := send(`{"recipients": ["bob"], "title": "Hi", "extra": 1}`); code != 400 {
		t.Fatalf("an unknown field was accepted: %d", code)
	}
	bob := inbox("bob")
	if len(bob) != 2 || bob[0].Type != notifications.TypeMessage || *bob[0].ItemID != item.ID || bob[0].Source != notifications.SourceUser || bob[0].Body != "Through 2031" {
		t.Fatalf("bob's message: %+v", bob)
	}

	// The caller's inbox: newest first, filtered, counted, read and deleted.
	if w = request(h, "GET", "/v1/me/notifications", "", testToken, "", ""); w.Code != 200 || strings.Index(w.Body.String(), "Lease renewed") > strings.Index(w.Body.String(), "Check the lease") {
		t.Fatalf("inbox: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications?type=task.completed&$top=1", "", testToken, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "Lease renewed") || !strings.Contains(w.Body.String(), `"@odata.nextLink":null`) {
		t.Fatalf("by type: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications?$top=1", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "%24skiptoken=") {
		t.Fatalf("paged: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications?type=other", "", testToken, "", ""); w.Code != 422 {
		t.Fatalf("unknown type: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications/unread-count", "", testToken, "", ""); w.Code != 200 || w.Body.String() != "{\"count\":2}\n" {
		t.Fatalf("unread: %d %q", w.Code, w.Body)
	}
	mine := inbox("alice")
	if w = request(h, "POST", "/v1/me/notifications/read", `{"ids": ["`+mine[0].ID+`", "`+bob[0].ID+`"]}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"marked":1`) {
		t.Fatalf("mark read: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications?unread=true", "", testToken, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), mine[0].ID) || !strings.Contains(w.Body.String(), mine[1].ID) {
		t.Fatalf("unread list: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/me/notifications/read", `{}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"marked":1`) {
		t.Fatalf("mark all read: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/me/notifications/read", `{"ids": ["x"], "before": "2030-01-01T00:00:00Z"}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("ids and before: %d %s", w.Code, w.Body)
	}
	if inbox("bob")[0].ReadAt != nil {
		t.Fatal("marking read reached someone else's notification")
	}
	if w = request(h, "DELETE", "/v1/me/notifications/"+bob[0].ID, "", testToken, "", ""); w.Code != 404 {
		t.Fatalf("deleted someone else's notification: %d", w.Code)
	}
	if w = request(h, "DELETE", "/v1/me/notifications/"+mine[0].ID, "", testToken, "", ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if len(inbox("alice")) != 1 {
		t.Fatalf("deleted notification remains: %+v", inbox("alice"))
	}
}

// Inboxes keep to what a request may read, sending needs write access,
// and pages, keys and long answers hold up.
func TestNotificationLimits(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	create := func(subject, parent, kind, name string, publishing bool) string {
		t.Helper()
		r, err := s.Create(ctx, subject, parent, dms.CreateResource{Kind: kind, Name: name, PublishingEnabled: publishing})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	ws := create("alice", "", "workspace", "Ops", false)
	open := create("alice", ws, "list", "Open", false)
	drafts := create("alice", ws, "list", "Drafts", true)
	shared := create("alice", open, "item", "Shared", false)
	draft := create("alice", drafts, "item", "Draft", false)
	other := create("alice", "", "workspace", "Other", false)
	secret := create("alice", create("alice", other, "list", "Secret", false), "item", "Merger", false)
	for _, m := range []dms.WorkspaceMember{{Subject: "dave", Role: "visitor"}, {Subject: "bob", Role: "member"}} {
		if err := s.SetWorkspaceMember(ctx, "alice", ws, m); err != nil {
			t.Fatal(err)
		}
	}
	const json_ = "application/json"
	send := func(token, workspace, body string) (int, string) {
		w := request(h, "POST", "/v1/workspaces/"+workspace+"/notifications", body, token, "", json_)
		return w.Code, w.Body.String()
	}
	issue := func(body string) string {
		t.Helper()
		w := request(h, "POST", "/v1/identity/me/tokens", body, testToken, "", json_)
		var out struct {
			Secret string `json:"secret"`
		}
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return out.Secret
	}

	// A visitor reads only published revisions: a draft is no news to them.
	if code, body := send(testToken, ws, `{"recipients": ["dave"], "title": "Draft ready", "item_id": "`+draft+`"}`); code != 200 || !strings.Contains(body, `"skipped":1`) {
		t.Fatalf("draft to a visitor: %d %s", code, body)
	}
	if code, body := send(testToken, ws, `{"recipients": ["dave"], "title": "Shared ready", "item_id": "`+shared+`"}`); code != 200 || !strings.Contains(body, `"sent":1`) {
		t.Fatalf("published item to a visitor: %d %s", code, body)
	}

	// A read-only token neither sends messages nor creates tasks.
	reader := issue(`{"name":"Reader","scopes":["workspace.read","item.read"]}`)
	if code, body := send(reader, ws, `{"recipients": ["bob"], "title": "Hi"}`); code != 403 {
		t.Fatalf("read-only send: %d %s", code, body)
	}
	if w := request(h, "POST", "/v1/workspaces/"+ws+"/tasks", `{"title": "Hi", "assignees": ["bob"]}`, reader, "", json_); w.Code != 403 {
		t.Fatalf("read-only task: %d %s", w.Code, w.Body)
	}
	if w := request(h, "POST", "/v1/me/notifications/read", `{}`, reader, "", json_); w.Code != 403 {
		t.Fatalf("read-only mark read: %d %s", w.Code, w.Body)
	}
	if w := request(h, "DELETE", "/v1/me/notifications/missing", "", reader, "", ""); w.Code != 403 {
		t.Fatalf("read-only delete: %d %s", w.Code, w.Body)
	}

	// A dedup key belongs to its workspace and sender: the same key in
	// another workspace sends again.
	if code, body := send(testToken, ws, `{"recipients": ["alice"], "title": "Once", "dedup_key": "weekly"}`); code != 200 || !strings.Contains(body, `"sent":1`) {
		t.Fatalf("first keyed send: %d %s", code, body)
	}
	if code, body := send(testToken, ws, `{"recipients": ["alice"], "title": "Once", "dedup_key": "weekly"}`); code != 200 || !strings.Contains(body, `"deduplicated":1`) {
		t.Fatalf("repeated keyed send: %d %s", code, body)
	}
	if code, body := send(testToken, other, `{"recipients": ["alice"], "title": "Elsewhere", "dedup_key": "weekly"}`); code != 200 || !strings.Contains(body, `"sent":1`) {
		t.Fatalf("keyed send in another workspace: %d %s", code, body)
	}

	// A message's dedup key cannot suppress a task's answer.
	w := request(h, "POST", "/v1/workspaces/"+ws+"/tasks", `{"title": "Sign", "assignees": ["bob"]}`, testToken, "", json_)
	var task tasks.View
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &task) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if code, body := send(testToken, ws, `{"recipients": ["alice"], "title": "Decoy", "dedup_key": "task:`+task.ID+`:completed"}`); code != 200 || !strings.Contains(body, `"sent":1`) {
		t.Fatalf("decoy: %d %s", code, body)
	}
	// The longest comment still completes the task, its notice shortened.
	if _, err := (&tasks.Service{DMS: s}).Respond(ctx, "bob", task.ID, tasks.Response{Outcome: "done", Comment: strings.Repeat("x", 4000)}); err != nil {
		t.Fatal(err)
	}
	completed, err := (&notify.Service{DMS: s}).Inbox(ctx, "alice", notify.Filter{Type: notifications.TypeTaskCompleted})
	if err != nil || len(completed.Data) != 1 || len([]rune(completed.Data[0].Body)) != notifications.MaxBody {
		t.Fatalf("answer notice: %+v %v", completed.Data, err)
	}

	// A page continues after its last notification, whatever was read
	// since, so nothing unread is skipped.
	for _, title := range []string{"One", "Two", "Three"} {
		if code, body := send(testToken, ws, `{"recipients": ["alice"], "title": "`+title+`"}`); code != 200 {
			t.Fatal(code, body)
		}
	}
	page := func(path string) (titles []string, next string) {
		t.Helper()
		w := request(h, "GET", path, "", testToken, "", "")
		var out struct {
			Value []notify.View `json:"value"`
			Next  *string       `json:"@odata.nextLink"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, n := range out.Value {
			titles = append(titles, n.Title)
		}
		if out.Next != nil {
			next = strings.TrimPrefix(*out.Next, "http://example.com")
		}
		return titles, next
	}
	first, next := page("/v1/me/notifications?unread=true&$top=1")
	if len(first) != 1 || first[0] != "Three" || next == "" {
		t.Fatalf("first page: %v %q", first, next)
	}
	mine, _ := (&notify.Service{DMS: s}).Inbox(ctx, "alice", notify.Filter{Limit: 1})
	if _, err = (&notify.Service{DMS: s}).MarkRead(ctx, "alice", []string{mine.Data[0].ID}, nil); err != nil {
		t.Fatal(err)
	}
	if second, _ := page(next); len(second) != 1 || second[0] != "Two" {
		t.Fatalf("second page skipped an unread notification: %v", second)
	}
	if w := request(h, "GET", "/v1/me/notifications?$skiptoken=nonsense", "", testToken, "", ""); w.Code != 422 {
		t.Fatalf("bad cursor: %d", w.Code)
	}

	// A token limited to a place sees only notifications about it, counts
	// and marks only those, and cannot delete the others.
	if code, body := send(testToken, other, `{"recipients": ["alice"], "title": "Merger talks", "body": "Confidential", "item_id": "`+secret+`"}`); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := send(testToken, ws, `{"recipients": ["alice"], "title": "About the shared item", "item_id": "`+shared+`"}`); code != 200 {
		t.Fatal(code, body)
	}
	place := issue(`{"name":"Open list","scopes":["workspace.read","item.read","item.write"],"resources":["` + open + `"]}`)
	w = request(h, "GET", "/v1/me/notifications", "", place, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Merger") || strings.Contains(w.Body.String(), "Decoy") || !strings.Contains(w.Body.String(), "About the shared item") {
		t.Fatalf("limited inbox: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/me/notifications/unread-count", "", place, "", ""); w.Body.String() != "{\"count\":1}\n" {
		t.Fatalf("limited count: %s", w.Body)
	}
	all, _ := (&notify.Service{DMS: s}).Inbox(ctx, "alice", notify.Filter{})
	var merger string
	for _, n := range all.Data {
		if n.Title == "Merger talks" {
			merger = n.ID
		}
	}
	if w = request(h, "DELETE", "/v1/me/notifications/"+merger, "", place, "", ""); w.Code != 404 {
		t.Fatalf("limited delete: %d", w.Code)
	}
	if w = request(h, "POST", "/v1/me/notifications/read", `{}`, place, "", json_); w.Body.String() != "{\"marked\":1}\n" {
		t.Fatalf("limited mark all: %s", w.Body)
	}
	if n, _ := (&notify.Service{DMS: s}).Unread(ctx, "alice"); n == 0 {
		t.Fatal("a limited token marked notifications it cannot see")
	}
}
