package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	directorydomain "papergo/internal/dal/directory"
	"papergo/internal/worker"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/identity"
	"papergo/internal/search"
	"papergo/internal/workflow"
)

// waitContent waits until the file version has published content.
func (f *fixture) waitContent(t *testing.T, itemID, blobID string) search.Content {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := f.dms.SearchStore().Content(ctx, itemID, blobID)
		if err != nil {
			t.Fatal(err)
		}
		if c.Generation != "" {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("no content for %s", itemID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) builtIn(t *testing.T, key, collectionID string) workflow.Workflow {
	t.Helper()
	_, err := f.wf.ListBuiltIns(ctx, "alice", f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := f.wf.List(ctx, "alice", f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range all {
		if w.BuiltInKey != nil && *w.BuiltInKey == key && (w.CollectionID == nil && collectionID == "" || w.CollectionID != nil && *w.CollectionID == collectionID) {
			return w
		}
	}
	t.Fatalf("no %s for %q", key, collectionID)
	return workflow.Workflow{}
}

// Uploading a PDF makes its text findable with its page, in global search
// and in the library's own search; a chained workflow reads the text; a
// rename re-chunks nothing; and a library can use another chunker.
func TestSearchContentMakesFileTextFindable(t *testing.T) {
	doctexttest.PDF(t)
	f := setup(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Contracts", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	follower := f.workflow(t, "After content", `{"triggers": [{"type": "role.search.content.completed"}], "flow": {"start": "check", "nodes": {
		"check": {"activity": "if", "inputs": {"left": "{trigger:data.outputs.text_id}", "op": "not_empty"}, "next": {"true": "done", "false": "bad"}},
		"done": {"activity": "end"}, "bad": {"activity": "fail", "inputs": {"message": "no text_id"}}}}}`)
	file := f.upload(t, lib.ID, "lease.pdf", "application/pdf", doctexttest.TextPDF("Lease agreement", "Payment within thirty days"))
	content := f.waitContent(t, file.ID, file.Blob.ID)

	f.searchNames(t, "alice", dms.SearchRequest{Q: "thirty", Surface: "head"}, "lease.pdf")
	found, err := f.dms.Search(ctx, "alice", dms.SearchRequest{Q: "payment", Surface: "head"})
	if err != nil || len(found.Hits) != 1 || found.Hits[0].Page != 2 || !strings.Contains(found.Hits[0].Snippet, "thirty days") {
		t.Fatalf("page hit: %+v %v", found.Hits, err)
	}
	page, err := f.dms.Browse(ctx, "alice", dms.Browse{ParentID: lib.ID, Search: "thirty da"})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != file.ID {
		t.Fatalf("library search: %+v %v", page.Data, err)
	}
	if runs := f.runs(t, follower.ID, 1); runs[0].Status != "completed" {
		t.Fatalf("chained run: %+v", runs[0])
	}

	// A rename re-indexes the name and keeps the content as it is.
	index := f.builtIn(t, "search.content", lib.ID)
	before := f.runs(t, index.ID, 1)
	current := f.head(t, file.ID)
	name := "lease-2026.pdf"
	if _, err = f.dms.Update(ctx, "alice", file.ID, current.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "thirty", Surface: "head"}, "lease-2026.pdf")
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if after := f.runs(t, index.ID, len(before)); len(after) != len(before) {
		t.Fatal("a rename ran search.content")
	}
	if again, err := f.dms.SearchStore().Content(ctx, file.ID, file.Blob.ID); err != nil || again != content {
		t.Fatalf("content after a rename: %+v (was %+v) %v", again, content, err)
	}

	// Replacing the same file starts content processing for its new blob.
	replacement := f.upload(t, lib.ID, name, "application/pdf", doctexttest.TextPDF("Replacement payment terms"))
	f.waitContent(t, replacement.ID, replacement.Blob.ID)
	if runs := f.runs(t, index.ID, 2); runs[0].EventType != dms.EventFileUpdated {
		t.Fatal("replacement did not run content processing on file.updated", runs)
	}
	f.runs(t, follower.ID, 2)

	// The library chunks page by page.
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "search.content", workflow.SetBuiltIn{CollectionID: lib.ID, Version: index.Version, AutomaticEnabled: true, Parameters: map[string]any{"chunker": "pages", "max_chars": 5000}}); err != nil {
		t.Fatal(err)
	}
	words := strings.Repeat("clause ", 400)
	book := f.upload(t, lib.ID, "terms.pdf", "application/pdf", doctexttest.TextPDF("One "+words, "Two "+words, "Three "+words))
	if c := f.waitContent(t, book.ID, book.Blob.ID); c.Chunks != 3 {
		t.Fatalf("pages chunker: %+v", c)
	}
	own := f.builtIn(t, "search.content", lib.ID)
	f.runs(t, own.ID, 3)
	f.runs(t, follower.ID, 3)
}

// search.publish refuses generations of other runs and files no surface
// has any more.
func TestSearchPublishChecks(t *testing.T) {
	doctexttest.PDF(t)
	f := setup(t)
	f.searchOff(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Letters", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.list = lib
	file := f.upload(t, lib.ID, "letter.pdf", "application/pdf", doctexttest.TextPDF("Dear customer"))
	w := f.workflow(t, "Publish another's", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}], "flow": {"start": "publish", "nodes": {
		"publish": {"activity": "search.publish", "inputs": {"generation": "someone-else:chunk"}, "next": {"done": "bad", "stale": "end"}},
		"bad": {"activity": "fail", "inputs": {"message": "published"}}, "end": {"activity": "end"}}}}`)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{file.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.wait(t, ids[0], finished); run.Status != "completed" {
		t.Fatalf("stale generation: %+v", run)
	}
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{AutomaticEnabled: new(true), Name: "Bad role", Definition: []byte(`{"triggers": [{"type": "role.nothing.completed"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`)}); !errors.As(err, new(*dms.ValidationError)) {
		t.Fatalf("unknown role accepted: %v", err)
	}
}

// A reindex with content has the files without content read again, by the
// workflow filling search.content; text read before is reused.
func TestContentReindex(t *testing.T) {
	doctexttest.PDF(t)
	f := setup(t)
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Archive", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	index := f.builtIn(t, "search.content", lib.ID)
	off, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "search.content", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: false, Version: index.Version})
	if err != nil {
		t.Fatal(err)
	}
	file := f.upload(t, lib.ID, "memo.pdf", "application/pdf", doctexttest.TextPDF("Quarterly memo about budgets"))
	f.searchNames(t, "alice", dms.SearchRequest{Q: "memo", Surface: "head"}, "memo.pdf")
	if c, err := f.dms.SearchStore().Content(ctx, file.ID, file.Blob.ID); err != nil || c.Generation != "" {
		t.Fatalf("content while search.content is off: %+v %v", c, err)
	}
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "search.content", workflow.SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true, Version: off.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.StartSearchReindex(ctx, "alice", dms.SearchReindex{Force: true}); err == nil {
		t.Fatal("force without content")
	}
	f.organizationManager(t, "alice")
	if _, err = f.dms.StartSearchReindex(ctx, "alice", dms.SearchReindex{Content: true}); err != nil {
		t.Fatal(err)
	}
	stop := f.operations(t)
	defer stop()
	f.waitContent(t, file.ID, file.Blob.ID)
	f.searchNames(t, "alice", dms.SearchRequest{Q: "budgets", Surface: "head"}, "memo.pdf")
}

// organizationManager lets subject manage the organization.
func (f *fixture) organizationManager(t *testing.T, subject string) {
	t.Helper()
	directory := f.dms.Directory()
	role, err := directory.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Search administrator", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: subject}, false); err != nil {
		t.Fatal(err)
	}
}

// operations runs the organization's operations until stopped.
func (f *fixture) operations(t *testing.T) func() {
	t.Helper()
	work, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		_ = worker.Single(f.dms, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(work)
		close(stopped)
	}()
	return func() { cancel(); <-stopped }
}
