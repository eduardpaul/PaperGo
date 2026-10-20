package runner

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

// test.index is a default, system role built-in that reacts to new folders;
// other runner tests never subscribe to them in a way that counts its runs.
func init() {
	workflow.RegisterRole(&workflow.Role{Key: "test.index", Name: "Test index",
		OutputSchema: forms.MustParse(`{"type": "object", "required": ["seen"], "properties": {"seen": {"type": "string"}}}`)})
	workflow.RegisterBuiltIn(&workflow.BuiltIn{Key: "test.index", Name: "Test index", Description: "Records new folders.", Role: "test.index", Default: true, System: true,
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`),
		Definition: `{"triggers": [{"type": "folder.created", "collection_id": "{param:collection}"}], "concurrency": "coalesce",
			"output_schema": {"type": "object", "required": ["seen"], "properties": {"seen": {"type": "string"}}},
			"outputs": {"seen": "{item:name}"},
			"flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`})
}

func (f *fixture) folder(t *testing.T, parent, name string) {
	t.Helper()
	if _, err := f.dms.Create(ctx, "alice", parent, dms.CreateResource{Kind: "folder", Name: name}); err != nil {
		t.Fatal(err)
	}
}

// fillers returns the workspace's workflows that fill test.index.
func (f *fixture) fillers(t *testing.T) []workflow.Workflow {
	if _, err := f.wf.ListBuiltIns(ctx, "alice", f.ws.ID); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	all, err := f.wf.List(ctx, "alice", f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(w workflow.Workflow) bool { return w.Role == nil || *w.Role != "test.index" })
}

func TestRoleBuiltInsAreCollectionScopedAndCopiesDoNotReplaceProviders(t *testing.T) {
	f := setup(t)
	other, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	fillers := f.fillers(t)
	if len(fillers) != 2 {
		t.Fatalf("one default per collection: %+v", fillers)
	}
	var own, peer workflow.Workflow
	for _, w := range fillers {
		if !w.AutomaticEnabled || w.CollectionID == nil {
			t.Fatal(w)
		}
		if *w.CollectionID == f.list.ID {
			own = w
		} else if *w.CollectionID == other.ID {
			peer = w
		}
	}
	f.folder(t, f.list.ID, "A")
	f.runs(t, own.ID, 1)
	copy, err := f.wf.CopyBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.CopyBuiltIn{Name: "Custom index", CollectionID: f.list.ID})
	if err != nil {
		t.Fatal(err)
	}
	if copy.State != "draft" || copy.AutomaticEnabled || copy.AutomaticProvider {
		t.Fatal(copy)
	}
	current, err := f.wf.Get(ctx, "alice", own.ID)
	if err != nil || current.Version != own.Version || !current.AutomaticEnabled {
		t.Fatal("copy changed source", current, err)
	}
	f.folder(t, other.ID, "B")
	f.runs(t, peer.ID, 1)
	raw, err := json.Marshal(copy.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.Update(ctx, "alice", copy.ID, copy.Version, workflow.Save{Name: copy.Name, State: "published", AutomaticEnabled: new(true), Definition: raw}); err != nil {
		t.Fatal(err)
	}
	f.folder(t, f.list.ID, "C")
	f.runs(t, copy.ID, 1)
	states, err := f.wf.CollectionRoles(ctx, "alice", f.list.ID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(states, func(st workflow.RoleState) bool { return st.Role.Key == "test.index" })
	if i < 0 || states[i].Workflow.ID != copy.ID || !states[i].AutomaticEnabled {
		t.Fatal(states)
	}
	current, err = f.wf.Get(ctx, "alice", own.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.SetBuiltIn{CollectionID: f.list.ID, AutomaticEnabled: true, Version: current.Version}); err != nil {
		t.Fatal(err)
	}
	copied, err := f.wf.Get(ctx, "alice", copy.ID)
	if err != nil || copied.AutomaticEnabled {
		t.Fatal(copied, err)
	}
	// Another collection's provider is unaffected by replacement.
	current, err = f.wf.Get(ctx, "alice", peer.ID)
	if err != nil || !current.AutomaticEnabled {
		t.Fatal(current, err)
	}
	broken := json.RawMessage(`{"triggers":[{"type":"folder.created"}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`)
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Broken", Role: "test.index", AutomaticEnabled: new(true), Definition: broken}); err == nil || !strings.Contains(err.Error(), "declares output seen") {
		t.Fatal(err)
	}
	var invalid *dms.ValidationError
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Scoped", CollectionID: f.list.ID, Definition: broken}); !errors.As(err, &invalid) {
		t.Fatal(err)
	}
}

func TestSystemRunsAreKeptADay(t *testing.T) {
	saved := systemRetention
	systemRetention = time.Millisecond
	t.Cleanup(func() { systemRetention = saved })
	f := setup(t)
	user := f.workflow(t, "Folders", `{"triggers": [{"type": "folder.created", "collection_id": "$LIST"}], "flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	f.folder(t, f.list.ID, "A")
	fillers := f.fillers(t)
	if len(fillers) != 1 {
		t.Fatalf("fillers: %+v", fillers)
	}
	f.runs(t, fillers[0].ID, 1)
	f.runs(t, user.ID, 1)
	time.Sleep(10 * time.Millisecond)
	deleted, err := f.r.PurgeSystemRuns(ctx)
	// The filler's run and those of search.index.
	if err != nil || deleted < 1 {
		t.Fatalf("purged %d: %v", deleted, err)
	}
	f.runs(t, fillers[0].ID, 0)
	f.runs(t, user.ID, 1)
}

func TestManualSystemRunsAreVisiblePagedAndRetained(t *testing.T) {
	f := setup(t)
	item := f.item(t, "alice", "Manual indexing", nil)
	root := f.builtIn(t, "search.index", "")
	if root.AutomaticEnabled {
		t.Fatal(root)
	}
	// Drive dispatch and status observation explicitly so paging is independent
	// of the background dispatcher's timing.
	f.r.stop()
	<-f.r.stopped
	ids := []string{}
	for range 2 {
		queued, err := f.wf.StartRuns(ctx, "alice", root.ID, workflow.Start{ItemIDs: []string{item.ID}})
		if err != nil || len(queued) != 1 {
			t.Fatal(queued, err)
		}
		ids = append(ids, queued[0])
		if _, err = f.r.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		f.status(t, queued[0], "completed")
	}
	// Run reads the engine directly; status filters use the observed status.
	if _, err := f.r.SyncLiveRuns(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{Origin: "manual", Status: "completed", Limit: 1})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != ids[1] || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	next, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{Origin: "manual", Status: "completed", Limit: 1, After: page.NextCursor})
	if err != nil || len(next.Data) != 1 || next.Data[0].ID != ids[0] || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	automatic, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{Origin: "automatic", IncludeSystem: true})
	if err != nil || len(automatic.Data) == 0 {
		t.Fatal(automatic, err)
	}
	hidden, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{Origin: "automatic"})
	if err != nil || len(hidden.Data) != 0 {
		t.Fatal(hidden, err)
	}
	saved := systemRetention
	systemRetention = time.Millisecond
	t.Cleanup(func() { systemRetention = saved })
	time.Sleep(10 * time.Millisecond)
	if _, err = f.r.PurgeSystemRuns(ctx); err != nil {
		t.Fatal(err)
	}
	retained, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{Origin: "manual"})
	if err != nil || len(retained.Data) != 2 {
		t.Fatal("manual system runs must use ordinary retention", retained, err)
	}
}
