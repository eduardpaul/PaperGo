package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	directorydomain "papergo/internal/dal/directory"
	"slices"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/model"
	"papergo/internal/worker"
)

type searchResponse struct {
	Value []struct {
		ID         string   `json:"id"`
		Surface    string   `json:"surface"`
		Name       string   `json:"name"`
		Snippet    string   `json:"snippet"`
		MatchedBy  []string `json:"matched_by"`
		Highlights [][2]int `json:"highlights"`
	} `json:"value"`
	Total          int     `json:"total"`
	TotalEstimated bool    `json:"total_estimated"`
	Mode           string  `json:"mode"`
	NextLink       *string `json:"@odata.nextLink"`
}

func searchAs(t *testing.T, h http.Handler, path string, want int) searchResponse {
	t.Helper()
	res := request(h, "GET", path, "", testToken, "", "")
	var out searchResponse
	if res.Code != want || (want == 200 && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		t.Fatal(path, res.Code, res.Body.String())
	}
	return out
}

func (b searchResponse) names() string {
	var out []string
	for _, v := range b.Value {
		out = append(out, v.Name+"@"+v.Surface)
	}
	return strings.Join(out, ",")
}

// Global search trims to what the caller reads now, on the surface they
// read, and its continuation cannot page another search.
func TestSearchTrimsByPermissionsAndSurfaces(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Reports", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Board"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Budget plan"}); err != nil {
		t.Fatal(err)
	}
	report, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Budget report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", report.ID, report.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "alice", secret.ID, dms.CreateResource{Kind: "item", Name: "Budget cuts"}); err != nil {
		t.Fatal(err)
	}
	head, err := s.Get(ctx, "alice", secret.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := []dms.Permission{{PrincipalType: "workspace_owner", PrincipalID: ws.ID, Action: "manage", Effect: "allow"}}
	if _, err = s.SetPermissions(ctx, "alice", secret.ID, head.Version, dms.Permissions{Grants: owners}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()

	all := searchAs(t, h, "/v1/search?q=budget", 200)
	if all.Total != 3 || len(all.Value) != 3 || !strings.Contains(all.names(), "Budget plan@head") || !strings.Contains(all.names(), "Budget report@head") || !strings.Contains(all.names(), "Budget cuts@") {
		t.Fatal("owner:", all.names(), all.Total)
	}
	for _, v := range all.Value {
		// Names match without a body to cut a snippet from.
		if (v.Snippet != "") != (len(v.Highlights) > 0) || len(v.MatchedBy) != 1 || v.MatchedBy[0] != "keyword" {
			t.Fatalf("hit: %+v", v)
		}
	}
	// A visitor reads published content only, and not the board.
	visitor := searchAs(t, bob, "/v1/search?q=budget", 200)
	if visitor.names() != "Budget report@published" || visitor.Total != 1 {
		t.Fatal("visitor:", visitor.names(), visitor.Total)
	}
	if got := searchAs(t, bob, "/v1/search?q=budget&surface=head", 200); len(got.Value) != 0 {
		t.Fatal("visitor read drafts:", got.names())
	}
	searchAs(t, h, "/v1/search", 422)
	searchAs(t, h, "/v1/search?q=budget&surface=draft", 422)
	searchAs(t, h, "/v1/search?q=budget&modified_from=yesterday", 422)
	// Without an embedding model, search is keyword only.
	searchAs(t, h, "/v1/search?q=budget&mode=semantic", 422)
	searchAs(t, h, "/v1/search?q=budget&mode=hybrid", 422)
	searchAs(t, h, "/v1/search?q=budget&mode=fuzzy", 422)
	if got := searchAs(t, h, "/v1/search?q=budget&mode=keyword", 200); got.Mode != "keyword" || got.Total != 3 {
		t.Fatal("keyword mode:", got.Mode, got.Total)
	}

	// Pages continue only the search they came from.
	first := searchAs(t, h, "/v1/search?q=budget&$top=2", 200)
	if len(first.Value) != 2 || first.NextLink == nil {
		t.Fatal("first page:", first.names())
	}
	next, err := url.Parse(*first.NextLink)
	if err != nil {
		t.Fatal(err)
	}
	second := searchAs(t, h, next.RequestURI(), 200)
	if len(second.Value) != 1 || second.NextLink != nil || strings.Contains(first.names(), second.Value[0].Name+"@") {
		t.Fatal("second page:", first.names(), second.names())
	}
	token := next.Query().Get("$skiptoken")
	searchAs(t, h, "/v1/search?q=report&$top=2&$skiptoken="+token, 400)
	searchAs(t, bob, "/v1/search?q=budget&$top=2&$skiptoken="+token, 400)
	searchAs(t, h, "/v1/search?q=budget&$top=2&$skiptoken=garbage", 400)

	// Revocation and deletion apply at once, before anything is reindexed.
	if err = s.RemoveWorkspaceMember(ctx, "alice", ws.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if got := searchAs(t, bob, "/v1/search?q=budget", 200); len(got.Value) != 0 {
		t.Fatal("revoked visitor:", got.names())
	}
	current, err := s.Get(ctx, "alice", report.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "alice", report.ID, current.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	stale := searchAs(t, h, "/v1/search?q=budget", 200)
	if strings.Contains(stale.names(), "Budget report") || !stale.TotalEstimated {
		t.Fatal("deleted entry found:", stale.names(), stale.TotalEstimated)
	}
}

// The reindex operation rebuilds the store; a rebuild of an empty store makes
// search unavailable until it completes.
func TestSearchReindexAndRebuild(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	directory := s.Directory()
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Search administrator", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Letters"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice March"}); err != nil {
		t.Fatal(err)
	}
	if got := searchAs(t, h, "/v1/search?q=invoice", 200); len(got.Value) != 0 {
		t.Fatal("indexed without the workflow:", got.names())
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	if res := request(bob, "POST", "/v1/search/reindex", "", testToken, "", ""); res.Code != 403 {
		t.Fatal("reindex without organization.manage", res.Code, res.Body.String())
	}
	workerCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		_ = worker.Single(s, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(workerCtx)
		close(stopped)
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	res := request(h, "POST", "/v1/search/reindex", "", testToken, "", "")
	var op dms.OperationView
	if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil || op.Kind != dms.OperationKindSearchReindex || res.Header().Get("Location") != "/v1/operations/"+op.ID {
		t.Fatal(res.Code, res.Body.String())
	}
	waitOperation(t, h, op.ID)
	if got := searchAs(t, h, "/v1/search?q=invoice", 200); got.names() != "Invoice March@published" {
		t.Fatal("after reindex:", got.names())
	}
	res = request(h, "GET", "/v1/search/status", "", testToken, "", "")
	var status dms.SearchStatus
	expectedStore := os.Getenv("PAPERGO_TEST_SEARCH_STORE")
	if expectedStore == "" {
		expectedStore = "sqlite"
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &status) != nil || status.Store != expectedStore || status.Documents != 1 || status.Rebuilding {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request(bob, "GET", "/v1/search/status", "", testToken, "", ""); res.Code != 403 {
		t.Fatal("status without organization.manage", res.Code)
	}

	// A rebuild (a new or recreated store) answers 503 until it completes,
	// in global search and in searches inside collections.
	cancel()
	<-stopped
	if err = s.RebuildSearch(ctx); err != nil {
		t.Fatal(err)
	}
	searchAs(t, h, "/v1/search?q=invoice", 503)
	if res = request(h, "GET", "/v1/resources/"+list.ID+"/children?q=invoice", "", testToken, "", ""); res.Code != 503 || !strings.Contains(res.Body.String(), "search_unavailable") {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request(h, "GET", "/v1/resources/"+list.ID+"/children", "", testToken, "", ""); res.Code != 200 {
		t.Fatal("listing without a search", res.Code)
	}
	rebuildCtx, stopRebuild := context.WithCancel(ctx)
	rebuilt := make(chan struct{})
	go func() {
		_ = worker.Single(s, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(rebuildCtx)
		close(rebuilt)
	}()
	t.Cleanup(func() { stopRebuild(); <-rebuilt })
	deadline := time.Now().Add(10 * time.Second)
	for request(h, "GET", "/v1/search?q=invoice", "", testToken, "", "").Code != 200 {
		if time.Now().After(deadline) {
			t.Fatal("the rebuild did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := searchAs(t, h, "/v1/search?q=invoice", 200); got.names() != "Invoice March@published" {
		t.Fatal("after rebuild:", got.names())
	}
}

func waitOperation(t *testing.T, h http.Handler, id string) dms.OperationView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res := request(h, "GET", "/v1/operations/"+id, "", testToken, "", "")
		var got dms.OperationView
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &got) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		if got.Status == "succeeded" || got.Status == "failed" {
			if got.Status != "succeeded" {
				t.Fatal("operation failed", got)
			}
			return got
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func postSearch(t *testing.T, h http.Handler, path, body string, want int) searchResponse {
	t.Helper()
	res := request(h, "POST", path, body, testToken, "", "application/json")
	var out searchResponse
	if res.Code != want || (want == 200 && json.Unmarshal(res.Body.Bytes(), &out) != nil) {
		t.Fatal(path, body, res.Code, res.Body.String())
	}
	return out
}

// Field filters type-check against the lists the caller reads, field
// weights place text, terms match their subtrees, and words find their
// other forms.
func TestSearchFieldsFiltersWeightsAndStems(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Accounts"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Bills"})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Payroll"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "costs", Name: "Costs"})
	if err != nil {
		t.Fatal(err)
	}
	travel, err := s.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Travel"})
	if err != nil {
		t.Fatal(err)
	}
	hotel, err := s.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Hotel", ParentID: &travel.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []dms.CreateField{
		{Key: "amount", Label: "Amount", Type: "integer"},
		{Key: "status", Label: "Status", Type: "choice", Choices: []string{"open", "paid"}},
		{Key: "memo", Label: "Memo", Type: "note"},
		{Key: "code", Label: "Code", Type: "text", Options: model.FieldOptions{SearchWeight: "none"}},
		{Key: "summary", Label: "Summary", Type: "text", Options: model.FieldOptions{SearchWeight: "high"}},
		{Key: "detail", Label: "Detail", Type: "text"},
		{Key: "cost", Label: "Cost", Type: "term", Options: model.FieldOptions{TermSetID: set.ID}},
	} {
		if _, err = s.CreateField(ctx, "alice", list.ID, f); err != nil {
			t.Fatal(f.Key, err)
		}
	}
	if _, err = s.CreateField(ctx, "alice", private.ID, dms.CreateField{Key: "salary", Label: "Salary", Type: "integer"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "bad", Label: "Bad", Type: "text", Options: model.FieldOptions{SearchWeight: "loud"}}); err == nil {
		t.Fatal("unknown search_weight accepted")
	}
	item := func(name string, values map[string]any) {
		t.Helper()
		if _, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: name, Values: values}); err != nil {
			t.Fatal(name, err)
		}
	}
	item("Invoice March", map[string]any{"amount": json.Number("120"), "status": "open", "code": "zebra", "detail": "quarterly audit", "cost": hotel.ID})
	item("Invoice April", map[string]any{"amount": json.Number("80"), "status": "paid", "summary": "quarterly audit", "memo": "harbour"})
	item("Receipt", map[string]any{"amount": json.Number("300"), "cost": travel.ID})
	if _, err = s.Create(ctx, "alice", private.ID, dms.CreateResource{Kind: "item", Name: "Salaries", Values: map[string]any{"salary": json.Number("5000")}}); err != nil {
		t.Fatal(err)
	}
	head := head(t, s, private.ID)
	owners := []dms.Permission{{PrincipalType: "workspace_owner", PrincipalID: ws.ID, Action: "manage", Effect: "allow"}}
	if _, err = s.SetPermissions(ctx, "alice", private.ID, head, dms.Permissions{Grants: owners}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()

	// Fields of the lists each caller reads; notes are text, not filters.
	fields := func(h http.Handler) map[string]dms.SearchField {
		res := request(h, "GET", "/v1/search/fields?workspace_id="+ws.ID, "", testToken, "", "")
		var out struct{ Value []dms.SearchField }
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		byKey := map[string]dms.SearchField{}
		for _, f := range out.Value {
			byKey[f.Key] = f
		}
		return byKey
	}
	mine := fields(h)
	if _, ok := mine["memo"]; ok || mine["amount"].Type != "integer" || !slices.Contains(mine["cost"].Operators, "under") || mine["salary"].Key == "" {
		t.Fatalf("fields: %+v", mine)
	}
	if _, ok := fields(bob)["salary"]; ok {
		t.Fatal("a field of an unreadable list is listed")
	}

	names := func(r searchResponse) string {
		var out []string
		for _, v := range r.Value {
			out = append(out, v.Name)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	if got := postSearch(t, h, "/v1/search", `{"filter":{"field":"amount","op":"gt","value":100}}`, 200); names(got) != "Invoice March,Receipt" {
		t.Fatal("gt:", names(got))
	}
	if got := postSearch(t, h, "/v1/search", `{"q":"invoice","filter":{"and":[{"field":"status","value":"paid"},{"field":"amount","op":"lt","value":100}]}}`, 200); names(got) != "Invoice April" {
		t.Fatal("q and filter:", names(got))
	}
	if got := postSearch(t, h, "/v1/search", `{"filter":{"field":"cost","op":"under","value":"`+travel.ID+`"}}`, 200); names(got) != "Invoice March,Receipt" {
		t.Fatal("under:", names(got))
	}
	if got := postSearch(t, h, "/v1/search", `{"filter":{"field":"cost","value":"`+travel.ID+`"}}`, 200); names(got) != "Receipt" {
		t.Fatal("term eq:", names(got))
	}
	if got := postSearch(t, h, "/v1/search", `{"collection_id":"`+list.ID+`","filter":{"field":"status","op":"missing"}}`, 200); names(got) != "Receipt" {
		t.Fatal("missing:", names(got))
	}
	postSearch(t, h, "/v1/search", `{"filter":{"field":"nope","value":1}}`, 422)
	postSearch(t, h, "/v1/search", `{"filter":{"field":"status","op":"gt","value":"open"}}`, 422)
	postSearch(t, h, "/v1/search", `{"filter":{"field":"amount","value":"many"}}`, 422)
	postSearch(t, bob, "/v1/search", `{"filter":{"field":"salary","op":"gt","value":1}}`, 422)

	// Weights: none keeps text out but filters still work; high ranks first.
	if got := searchAs(t, h, "/v1/search?q=zebra", 200); len(got.Value) != 0 {
		t.Fatal("search_weight none indexed:", names(got))
	}
	if got := postSearch(t, h, "/v1/search", `{"filter":{"field":"code","value":"zebra"}}`, 200); names(got) != "Invoice March" {
		t.Fatal("filter on a none field:", names(got))
	}
	if got := searchAs(t, h, "/v1/search?q=audit", 200); len(got.Value) != 2 || got.Value[0].Name != "Invoice April" {
		t.Fatal("high weight ranks first:", got.Value)
	}
	if got := searchAs(t, h, "/v1/search?q=harbour", 200); names(got) != "Invoice April" {
		t.Fatal("notes are text:", names(got))
	}
	// Words find their other forms.
	if got := searchAs(t, h, "/v1/search?q=invoices", 200); names(got) != "Invoice April,Invoice March" {
		t.Fatal("stemming:", names(got))
	}

	// A continuation of a POST search binds its body.
	first := postSearch(t, h, "/v1/search?$top=1", `{"q":"invoice"}`, 200)
	if first.NextLink == nil {
		t.Fatal("no next page")
	}
	next, err := url.Parse(*first.NextLink)
	if err != nil {
		t.Fatal(err)
	}
	if got := postSearch(t, h, next.RequestURI(), `{"q":"invoice"}`, 200); len(got.Value) != 1 || got.Value[0].ID == first.Value[0].ID {
		t.Fatal("second page:", got.Value)
	}
	postSearch(t, h, next.RequestURI(), `{"q":"receipt"}`, 400)
}

func head(t *testing.T, s *dms.Service, id string) int {
	t.Helper()
	r, err := s.Get(context.Background(), "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Version
}
