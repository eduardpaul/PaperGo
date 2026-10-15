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
	t.Helper()
	all, err := f.wf.List(ctx, "alice", f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(w workflow.Workflow) bool { return w.Role == nil || *w.Role != "test.index" })
}

func TestRoleBuiltInsDefaultsOverridesAndCopies(t *testing.T) {
	f := setup(t)
	other, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	// Default built-ins are turned on in the workspace by its creator.
	fillers := f.fillers(t)
	if len(fillers) != 1 || !fillers[0].Enabled || fillers[0].CollectionID != nil || fillers[0].CreatedBy != "alice" {
		t.Fatalf("default filler: %+v", fillers)
	}
	workspaceFiller := fillers[0]
	f.folder(t, f.list.ID, "A")
	runs := f.runs(t, workspaceFiller.ID, 1)
	detail, err := f.r.Run(ctx, "alice", runs[0].ID)
	if err != nil || detail.Status != "completed" || detail.Result.(map[string]any)["outputs"].(map[string]any)["seen"] != "A" {
		t.Fatalf("workspace filler run: %+v %v", detail, err)
	}

	// The list gets its own filler: the workspace's no longer runs for it.
	own, err := f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.SetBuiltIn{CollectionID: f.list.ID, Enabled: true})
	if err != nil || own.Role == nil || own.CollectionID == nil || *own.CollectionID != f.list.ID {
		t.Fatalf("collection filler: %+v %v", own, err)
	}
	f.folder(t, f.list.ID, "B")
	f.folder(t, other.ID, "C")
	f.runs(t, own.ID, 1)
	f.runs(t, workspaceFiller.ID, 2)

	// Turned off for the list, the role does nothing there.
	if own, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.SetBuiltIn{CollectionID: f.list.ID, Enabled: false, Version: own.Version}); err != nil {
		t.Fatal(err)
	}
	f.folder(t, f.list.ID, "D")
	states, err := f.wf.CollectionRoles(ctx, "alice", f.list.ID)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(states, func(s workflow.RoleState) bool { return s.Role.Key == "test.index" })
	if i < 0 || states[i].Scope != "collection" || states[i].Enabled {
		t.Fatalf("roles of the list: %+v", states)
	}

	// A filler must keep the role's outputs.
	broken := json.RawMessage(`{"triggers": [{"type": "folder.created"}], "flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Broken index", Role: "test.index", Definition: broken}); err == nil || !strings.Contains(err.Error(), "declares output seen") {
		t.Fatalf("filler without outputs: %v", err)
	}
	// A copy fills the role in the workspace and turns the built-in off.
	copied, err := f.wf.CopyBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.CopyBuiltIn{Name: "Custom index"})
	if err != nil || copied.Role == nil || !copied.Enabled {
		t.Fatalf("copy: %+v %v", copied, err)
	}
	f.folder(t, other.ID, "E")
	f.runs(t, copied.ID, 1)
	f.runs(t, workspaceFiller.ID, 2)
	f.runs(t, own.ID, 1)
	for _, w := range f.fillers(t) {
		if w.Enabled != (w.ID == copied.ID) {
			t.Fatalf("one enabled filler per scope: %+v", f.fillers(t))
		}
	}
	// Turning the built-in back on turns the copy off.
	builtin, err := f.wf.Get(ctx, "alice", workspaceFiller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, "test.index", workflow.SetBuiltIn{Enabled: true, Version: builtin.Version}); err != nil {
		t.Fatal(err)
	}
	if again, err := f.wf.Get(ctx, "alice", copied.ID); err != nil || again.Enabled {
		t.Fatalf("copy after the built-in returned: %+v %v", again, err)
	}
	// A collection scope needs a role, and the collection must be the workspace's.
	var invalid *dms.ValidationError
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Scoped", CollectionID: f.list.ID, Definition: broken}); !errors.As(err, &invalid) {
		t.Fatalf("scope without a role: %v", err)
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
