package comments

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	directorydomain "papergo/internal/dal/directory"
	"papergo/internal/dal/notifications"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/notify"
	"papergo/internal/storage"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const testToken = "0123456789abcdef0123456789abcdef"

// setup is the API with comments, acting as alice, and its service.
func setup(t *testing.T) (http.Handler, *dms.Service) {
	t.Helper()
	db := daltest.Database(t)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.Default(), Ready: db.SQL.PingContext, MaxUpload: 32, Modules: []httpapi.Module{Module()}}
	return a.Handler(), s
}

func request(h http.Handler, method, path, body, token, match, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type sseFrame struct{ kind, data string }

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
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			frame.data = value
		}
	}
}

func TestCommentRoutes(t *testing.T) {
	h, s := setup(t)
	ctx := t.Context()
	create := func(parent, kind, name string) string {
		t.Helper()
		r, err := s.Create(ctx, "alice", parent, dms.CreateResource{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	ws := create("", "workspace", "Legal")
	list := create(ws, "list", "Contracts")
	item := create(list, "item", "Lease")
	for _, m := range []dms.WorkspaceMember{{Subject: "bob", Role: "member"}, {Subject: "dave", Role: "visitor"}} {
		if err := s.SetWorkspaceMember(ctx, "alice", ws, m); err != nil {
			t.Fatal(err)
		}
	}
	const json_ = "application/json"
	call := func(method, path, body, match string, want int) string {
		t.Helper()
		w := request(h, method, path, body, testToken, match, json_)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	svc := &Service{DMS: s}
	inbox := func(subject string, typ notifications.Type) []notify.View {
		t.Helper()
		page, err := (&notify.Service{DMS: s}).Inbox(ctx, subject, notify.Filter{Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		return page.Data
	}

	// A thread mentioning a collaborator and a visitor: only the
	// collaborator, who reads drafts, is told.
	var root View
	body := call("POST", "/v1/items/"+item+"/comments", `{"body": "Is clause 4 signed, <@user:bob>? cc <@user:dave>"}`, "", 201)
	if err := json.Unmarshal([]byte(body), &root); err != nil || len(root.Mentions) != 2 || root.Author.Subject != "alice" || !root.CanEdit || !root.CanResolve {
		t.Fatalf("root: %s %v", body, err)
	}
	mentioned := inbox("bob", notifications.TypeCommentMentioned)
	if len(mentioned) != 1 || mentioned[0].Title != "alice mentioned you on Lease" || mentioned[0].Body != "Is clause 4 signed, @bob? cc @dave" || *mentioned[0].CommentID != root.ID || *mentioned[0].ItemID != item {
		t.Fatalf("bob's mention: %+v", mentioned)
	}
	if len(inbox("dave", "")) != 0 || len(inbox("alice", "")) != 0 {
		t.Fatal("a visitor or the author was told")
	}
	call("POST", "/v1/items/"+item+"/comments", `{"body": "Hi <@user:nobody>"}`, "", 422)
	call("POST", "/v1/items/"+item+"/comments", `{"body": "  "}`, "", 422)
	call("POST", "/v1/items/"+list+"/comments", `{"body": "On a list"}`, "", 422)

	// Replies tell the thread's participants, folding a burst into one.
	for i, text := range []string{"Yes, on page 3", "And initialled"} {
		if _, err := svc.Create(ctx, "bob", "", item, Input{Body: text, ThreadID: root.ID}); err != nil {
			t.Fatal(i, err)
		}
	}
	replied := inbox("alice", notifications.TypeCommentReplied)
	if len(replied) != 1 || replied[0].Count != 2 || replied[0].Body != "And initialled" || replied[0].Title != "bob replied on Lease" {
		t.Fatalf("alice's replies: %+v", replied)
	}
	if len(inbox("bob", notifications.TypeCommentReplied)) != 0 {
		t.Fatal("the person who replied was told of their own reply")
	}

	// The thread with its replies; a visitor cannot read collaborators'
	// discussion, and is suggested as someone who cannot see it.
	body = call("GET", "/v1/items/"+item+"/comments", "", "", 200)
	var page struct {
		Value   []Thread `json:"value"`
		Mode    string   `json:"mode"`
		From    string   `json:"mode_from"`
		CanPost bool     `json:"can_post"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Value) != 1 || len(page.Value[0].Replies) != 2 || page.Mode != "collaborators" || page.From != "default" || !page.CanPost {
		t.Fatalf("threads: %s %v", body, err)
	}
	if visitor, err := svc.Threads(ctx, "dave", item, Query{Status: StatusAll}); err != nil || visitor.CanRead || len(visitor.Data) != 0 {
		t.Fatalf("a visitor read collaborators' discussion: %+v %v", visitor, err)
	}
	body = call("GET", "/v1/items/"+item+"/mentionable?q=da", "", "", 200)
	if !strings.Contains(body, `"subject":"dave"`) || !strings.Contains(body, `"can_see":false`) || strings.Contains(body, `"subject":"bob"`) {
		t.Fatalf("mentionable: %s", body)
	}
	// People the list is shared with through a group are suggested;
	// people the item is not shared with never are.
	for _, id := range []string{"erin", "frank"} {
		if _, err := s.DAL.Directory().CreateUser(ctx, id, id, "", true, false); err != nil {
			t.Fatal(err)
		}
	}
	reviewers, err := s.DAL.Directory().CreateGroup(ctx, "reviewers", "Reviewers", "", directorydomain.SourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DAL.Directory().SetMember(ctx, reviewers.ID, directorydomain.Principal{Type: directorydomain.PrincipalUser, ID: "erin"}, false); err != nil {
		t.Fatal(err)
	}
	shared, err := s.Get(ctx, "alice", list)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", list, shared.Version, dms.Permissions{CopyInherited: true, Grants: []dms.Permission{{PrincipalType: "group", PrincipalID: reviewers.ID, Action: "read", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if body = call("GET", "/v1/items/"+item+"/mentionable", "", "", 200); !strings.Contains(body, `"subject":"erin"`) || strings.Contains(body, `"subject":"frank"`) {
		t.Fatalf("mentionable through a group: %s", body)
	}
	if body = call("GET", "/v1/items/"+item+"/mentionable?q=frank", "", "", 200); body != "{\"value\":[]}\n" {
		t.Fatalf("someone the item is not shared with: %s", body)
	}

	// Someone who leaves stays mentioned: the author still edits the
	// comment, but nobody mentions them anew.
	var note View
	if err = json.Unmarshal([]byte(call("POST", "/v1/items/"+item+"/comments", `{"body": "Ask <@user:frank>"}`, "", 201)), &note); err != nil {
		t.Fatal(err)
	}
	if err = s.DAL.Directory().SetUserActive(ctx, "frank", false); err != nil {
		t.Fatal(err)
	}
	call("PATCH", "/v1/comments/"+note.ID, `{"body": "Ask <@user:frank> first"}`, `"`+strconv.FormatInt(note.Version, 10)+`"`, 200)
	call("POST", "/v1/items/"+item+"/comments", `{"body": "Ask <@user:frank> again"}`, "", 422)
	call("DELETE", "/v1/comments/"+note.ID, "", `"`+strconv.FormatInt(note.Version+1, 10)+`"`, 204)

	// Edits tell only the people they add; only the author edits.
	reply := page.Value[0].Replies[0]
	if _, err := svc.Edit(ctx, "alice", reply.ID, reply.Version, "Edited"); err == nil {
		t.Fatal("someone else edited a reply")
	}
	if _, err := svc.Edit(ctx, "bob", reply.ID, reply.Version, "Yes, on page 3 <@user:alice>"); err != nil {
		t.Fatal(err)
	}
	if got := inbox("alice", notifications.TypeCommentMentioned); len(got) != 1 {
		t.Fatalf("an edit's new mention: %+v", got)
	}
	call("PATCH", "/v1/comments/"+root.ID, `{"body": "Is clause 4 signed?"}`, `"`+strconv.FormatInt(root.Version, 10)+`"`, 200)
	call("PATCH", "/v1/comments/"+root.ID, `{"body": "Stale"}`, `"`+strconv.FormatInt(root.Version, 10)+`"`, 409)

	// Resolving: the author, writers and managers; a reply cannot be.
	body = call("POST", "/v1/comments/"+root.ID+"/resolve", "", `"2"`, 200)
	if !strings.Contains(body, `"resolved_by":{"subject":"alice"`) {
		t.Fatalf("resolve: %s", body)
	}
	call("POST", "/v1/comments/"+reply.ID+"/resolve", "", `"2"`, 409)
	call("GET", "/v1/items/"+item+"/comments", "", "", 200)
	if body = call("GET", "/v1/items/"+item+"/comments?status=resolved", "", "", 200); !strings.Contains(body, root.ID) {
		t.Fatalf("resolved threads: %s", body)
	}
	body = call("POST", "/v1/comments/counts", `{"item_ids": ["`+item+`", "missing"]}`, "", 200)
	if body != `{"counts":{"`+item+`":{"open":0,"resolved":1}}}`+"\n" {
		t.Fatalf("counts: %s", body)
	}

	// A manager deletes someone's reply; the thread keeps the others.
	second := page.Value[0].Replies[1]
	call("DELETE", "/v1/comments/"+second.ID, "", `"`+strconv.FormatInt(second.Version, 10)+`"`, 204)
	if threads, _ := svc.Threads(ctx, "alice", item, Query{Status: StatusAll}); len(threads.Data[0].Replies) != 1 {
		t.Fatalf("a deleted reply remains: %+v", threads.Data[0].Replies)
	}

	// Readers: a collection opens its discussion to everyone who reads the
	// item; the visitor reads and posts but cannot resolve.
	call("PUT", "/v1/resources/"+list+"/settings/comments", `{"mode": "readers"}`, `"1"`, 409)
	body = call("PUT", "/v1/resources/"+list+"/settings/comments", `{"mode": "readers"}`, `"0"`, 200)
	if !strings.Contains(body, `"effective":{"mode":"readers","from":"collection"}`) {
		t.Fatalf("collection settings: %s", body)
	}
	call("PUT", "/v1/resources/"+item+"/settings/comments", `{"mode": "readers"}`, `"0"`, 422)
	call("PUT", "/v1/resources/"+list+"/settings/comments", `{"mode": "everyone"}`, `"1"`, 422)
	visitor, err := svc.Threads(ctx, "dave", item, Query{Status: StatusAll})
	if err != nil || !visitor.CanRead || !visitor.CanPost || len(visitor.Data) != 1 || visitor.Data[0].CanResolve {
		t.Fatalf("a visitor among readers: %+v %v", visitor, err)
	}
	if _, err = svc.Resolve(ctx, "dave", root.ID, 3, false); err == nil {
		t.Fatal("a visitor reopened someone's thread")
	}

	// The workspace turns comments off, but the collection's own mode
	// wins; the organization's switch wins over both.
	call("PUT", "/v1/resources/"+ws+"/settings/comments", `{"mode": "off"}`, `"0"`, 200)
	if settings, _ := svc.PlaceSettings(ctx, "alice", list); settings.Effective.Mode != ModeReaders {
		t.Fatalf("collection under an off workspace: %+v", settings)
	}
	call("PUT", "/v1/organization/settings/comments", `{"enabled": false}`, `"0"`, 403)
	directory := s.Directory()
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Organization", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/v1/organization/settings/comments", `{"enabled": false}`, `"0"`, 200)
	body = call("GET", "/v1/items/"+item+"/comments", "", "", 200)
	if !strings.Contains(body, `"mode":"off"`) || !strings.Contains(body, `"mode_from":"organization"`) || !strings.Contains(body, `"value":[]`) {
		t.Fatalf("comments off: %s", body)
	}
	call("POST", "/v1/items/"+item+"/comments", `{"body": "Anyone?"}`, "", 409)
	call("PUT", "/v1/organization/settings/comments", `{"enabled": true, "mode": "collaborators"}`, `"1"`, 200)
	if body = call("GET", "/v1/organization/settings/comments", "", "", 200); !strings.Contains(body, `"enabled":true,"mode":"collaborators","version":2`) {
		t.Fatalf("organization settings: %s", body)
	}

	// A read-only token reads but does not write; an archived item's
	// discussion is read only.
	w := request(h, "POST", "/v1/identity/me/tokens", `{"name":"Reader","scopes":["workspace.read","item.read"]}`, testToken, "", json_)
	var token struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &token)
	if w = request(h, "GET", "/v1/items/"+item+"/comments", "", token.Secret, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"can_post":false`) {
		t.Fatalf("read-only token reads: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/items/"+item+"/comments", `{"body": "Hi"}`, token.Secret, "", json_); w.Code != 403 {
		t.Fatalf("read-only token posts: %d %s", w.Code, w.Body)
	}
	current, err := s.Get(ctx, "alice", item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetArchived(ctx, "alice", item, current.Version, true); err != nil {
		t.Fatal(err)
	}
	call("POST", "/v1/items/"+item+"/comments", `{"body": "After archiving"}`, "", 409)
	if body = call("GET", "/v1/items/"+item+"/comments?status=all", "", "", 200); !strings.Contains(body, `"can_post":false`) || !strings.Contains(body, root.ID) {
		t.Fatalf("archived discussion: %s", body)
	}
}

// An integration's identity has only its token's access, which carries it
// through mentions and replies as through plain comments.
func TestIntegrationsMentionAndReply(t *testing.T) {
	h, s := setup(t)
	ctx := t.Context()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Ops"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Requests"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateIntegrationToken(ctx, "alice", identity.AccessTokenInput{Name: "Helpdesk", Scopes: []string{"workspace.read", "item.read", "comment.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: token.Token.Subject, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	post := func(body string) View {
		t.Helper()
		w := request(h, "POST", "/v1/items/"+item.ID+"/comments", body, token.Secret, "", "application/json")
		var out View
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("integration comment %s: %d %s", body, w.Code, w.Body)
		}
		return out
	}
	post(`{"body": "Ticket opened"}`)
	root := post(`{"body": "Approve the order, <@user:alice>?"}`)
	if _, err = (&Service{DMS: s}).Create(ctx, "alice", "", item.ID, Input{Body: "Approved", ThreadID: root.ID}); err != nil {
		t.Fatal(err)
	}
	post(`{"body": "Ordered", "thread_id": "` + root.ID + `"}`)
	if got, _ := (&notify.Service{DMS: s}).Inbox(ctx, "alice", notify.Filter{}); len(got.Data) != 2 {
		t.Fatalf("alice's mention and reply: %+v", got.Data)
	}
}

// A comment's notifications show its current text, and its text leaves
// with it when it is deleted.
func TestCommentNotificationsFollowTheComment(t *testing.T) {
	_, s := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Legal"})
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
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{DMS: s}
	post := func(subject, body, thread string) View {
		t.Helper()
		v, err := svc.Create(ctx, subject, "", item.ID, Input{Body: body, ThreadID: thread})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	inbox := func(typ notifications.Type) []notify.View {
		t.Helper()
		page, err := (&notify.Service{DMS: s}).Inbox(ctx, "bob", notify.Filter{Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		return page.Data
	}

	mention := post("alice", "The password is hunter2, <@user:bob>", "")
	edited, err := svc.Edit(ctx, "alice", mention.ID, mention.Version, "Ask me for the password, <@user:bob>")
	if err != nil {
		t.Fatal(err)
	}
	if got := inbox(notifications.TypeCommentMentioned); len(got) != 1 || got[0].Body != "Ask me for the password, @bob" {
		t.Fatalf("an edited mention: %+v", got)
	}
	if err = svc.Delete(ctx, "alice", mention.ID, edited.Version); err != nil {
		t.Fatal(err)
	}
	if got := inbox(notifications.TypeCommentMentioned); len(got) != 0 {
		t.Fatalf("a deleted comment's mention remains: %+v", got)
	}

	// A fold counts the replies left and shows the latest of them; with
	// none left, it goes.
	thread := post("bob", "Who signs?", "")
	first := post("alice", "Carol", thread.ID)
	second := post("alice", "No, Dave", thread.ID)
	third := post("alice", "Or Erin", thread.ID)
	if got := inbox(notifications.TypeCommentReplied); len(got) != 1 || got[0].Count != 3 || got[0].Body != "Or Erin" {
		t.Fatalf("folded replies: %+v", got)
	}
	if err = svc.Delete(ctx, "alice", second.ID, second.Version); err != nil {
		t.Fatal(err)
	}
	if got := inbox(notifications.TypeCommentReplied); len(got) != 1 || got[0].Count != 2 || got[0].Body != "Or Erin" || *got[0].CommentID != third.ID {
		t.Fatalf("a fold without an older reply: %+v", got)
	}
	if err = svc.Delete(ctx, "alice", third.ID, third.Version); err != nil {
		t.Fatal(err)
	}
	got := inbox(notifications.TypeCommentReplied)
	if len(got) != 1 || got[0].Count != 1 || got[0].Body != "Carol" || *got[0].CommentID != first.ID || got[0].Title != "alice replied on Lease" {
		t.Fatalf("a fold without its latest reply: %+v", got)
	}
	if err = svc.Delete(ctx, "alice", first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	if got = inbox(notifications.TypeCommentReplied); len(got) != 0 {
		t.Fatalf("a fold of deleted replies remains: %+v", got)
	}

}

// An edit tells nobody what they may no longer read.
func TestCommentEditsReachOnlyReaders(t *testing.T) {
	_, s := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Legal"})
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
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{DMS: s}
	post := func(subject, body, thread string) View {
		t.Helper()
		v, err := svc.Create(ctx, subject, "", item.ID, Input{Body: body, ThreadID: thread})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	inbox := func(typ notifications.Type) []notify.View {
		t.Helper()
		page, err := (&notify.Service{DMS: s}).Inbox(ctx, "bob", notify.Filter{Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		return page.Data
	}

	secret := post("alice", "Draft terms, <@user:bob>", "")
	if err = s.RemoveWorkspaceMember(ctx, "alice", ws.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Edit(ctx, "alice", secret.ID, secret.Version, "Final terms: 2% fee, <@user:bob>"); err != nil {
		t.Fatal(err)
	}
	if got := inbox(notifications.TypeCommentMentioned); len(got) != 1 || got[0].Body != "" {
		t.Fatalf("an edit reached someone who lost access: %+v", got)
	}
}

func TestCommentAnchors(t *testing.T) {
	h, s := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Legal"})
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
	call := func(method, path, body string, want int) string {
		t.Helper()
		w := request(h, method, path, body, testToken, "", "application/json")
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	base := "/v1/items/" + item.ID + "/comments"
	var root View
	body := call("POST", base, `{"body": "Check this clause", "anchor": {"kind": "file.region", "blob_id": "b1", "page": 2, "x": 0.1, "y": 0.2, "w": 0.3, "h": 0.05}}`, 201)
	if err = json.Unmarshal([]byte(body), &root); err != nil || !strings.Contains(string(root.Anchor), `"kind":"file.region"`) {
		t.Fatalf("anchored: %s %v", body, err)
	}
	call("POST", base, `{"body": "General", "anchor": null}`, 201)
	call("POST", base, `{"body": "Reply", "thread_id": "`+root.ID+`", "anchor": {"kind": "file.region"}}`, 422)
	call("POST", base, `{"body": "No kind", "anchor": {"page": 1}}`, 422)
	call("POST", base, `{"body": "Not an object", "anchor": [1]}`, 422)
	all := call("GET", base, "", 200)
	pinned := call("GET", base+"?anchor_kind=file.region", "", 200)
	if strings.Count(all, `"thread_id"`) != 2 || strings.Count(pinned, `"thread_id"`) != 1 || !strings.Contains(pinned, `"page":2`) {
		t.Fatalf("all: %s\npinned: %s", all, pinned)
	}
	if got := call("GET", "/v1/comments/"+root.ID, "", 200); !strings.Contains(got, `"anchor":{`) {
		t.Fatalf("one comment: %s", got)
	}
}

// A comment frame tells people who may read the discussion what changed.
func TestLiveCommentFrames(t *testing.T) {
	_, s := setup(t)
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxInFlight: 1, RequestTimeout: 5 * time.Second, Modules: []httpapi.Module{Module()}}
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
	svc := &Service{DMS: s}
	if _, err = svc.Create(ctx, "bob", "", hidden, Input{Body: "Private"}); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Create(ctx, "alice", "", shown, Input{Body: "Shared"})
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
	// A burst on both items, read as one batch, still shows only the
	// discussion the stream may read.
	var last View
	for i := range 3 {
		if _, err = svc.Create(ctx, "bob", "", hidden, Input{Body: "Private"}); err != nil {
			t.Fatal(err)
		}
		if last, err = svc.Create(ctx, "alice", "", shown, Input{Body: fmt.Sprint("Shared ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for seen := 0; seen < 3; {
		frame := readFrame(t, reader)
		if frame.kind == "keepalive" || frame.kind == "item" {
			continue
		}
		if frame.kind != "comment" || !strings.Contains(frame.data, shown) || strings.Contains(frame.data, hidden) {
			t.Fatalf("comment frame in a burst: %+v", frame)
		}
		seen++
		if seen == 3 && !strings.Contains(frame.data, last.ID) {
			t.Fatalf("the burst's last frame: %+v", frame)
		}
	}
}
