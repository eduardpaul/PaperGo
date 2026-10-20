package workflow

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resources"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"strconv"
	"strings"
	"testing"
)

// source is a workflow whose export declares triggers, a JSON array, and
// returns marker.
func source(triggers, marker string) string {
	return `import { workflow } from "@papergo/workflow";
export default workflow({ triggers: ` + triggers + `, run: () => ({ marker: "` + marker + `" }) });
`
}

// started returns the runs Match starts for events.
func started(t *testing.T, s *Service, events []dms.Event) []runstore.Run {
	t.Helper()
	var out []runstore.Run
	err := s.DMS.Write(t.Context(), func(tx *dms.Service) error {
		ids, _, err := Match(t.Context(), tx, events)
		for _, id := range ids {
			run, e := tx.DAL.Runs().Find(t.Context(), id)
			if e != nil {
				return e
			}
			out = append(out, run)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func policyFixture(t *testing.T) (*Service, *resources.Resource, *resources.Resource, *resources.Resource, *resources.Resource) {
	t.Helper()
	ds := dms.NewService(daltest.Database(t).SQL)
	// Runs are accepted; these tests never execute them.
	if err := ds.RegisterOperationHandler(dms.OperationKindRun, dms.OperationHandler{Scope: "workspace.read",
		Validate: func(context.Context, *dms.Service, string, string, json.RawMessage) error { return nil },
		Execute:  func(context.Context, *dms.Service, *operations.Operation) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	create := func(parent string, in dms.CreateResource) *resources.Resource {
		t.Helper()
		r, e := ds.Create(ctx, "alice", parent, in)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	ws := create("", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	a := create(ws.ID, dms.CreateResource{Kind: "library", Name: "Photos"})
	b := create(ws.ID, dms.CreateResource{Kind: "library", Name: "Other files"})
	item := create(a.ID, dms.CreateResource{Kind: "item", Name: "Photo"})
	return &Service{DMS: ds}, ws, a, b, item
}

func TestManualUseIndependentOfAutomationAndPinsAcceptedVersion(t *testing.T) {
	s, ws, a, _, item := policyFixture(t)
	ctx := t.Context()
	triggers := `[{"type":"manual","selection":"per_item"},{"type":"file.added","collection_id":"` + a.ID + `"}]`
	w, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Process a photo", Source: source(triggers, "first")})
	if err != nil {
		t.Fatal(err)
	}
	if w.AutomaticEnabled || !w.ManualEnabled || w.State != "published" {
		t.Fatal(w)
	}
	ready, err := s.CheckStart(ctx, "alice", w.ID, Start{ItemIDs: []string{item.ID}}, true)
	if err != nil || !ready.CanRun {
		t.Fatal(ready, err)
	}
	ids, err := s.StartRuns(ctx, "alice", w.ID, Start{WorkflowVersion: 1, ItemIDs: []string{item.ID}})
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	if rows, e := s.DMS.DAL.Workflows().Triggers(ctx, workflows.TriggerFilter{WorkflowID: w.ID}); e != nil || len(rows) != 0 {
		t.Fatal("manual use must not subscribe to events", rows, e)
	}
	_, err = s.Update(ctx, "alice", w.ID, w.Version, Save{Name: w.Name, State: "archived", Source: source(triggers, "second")})
	if err != nil {
		t.Fatal(err)
	}
	eventID := strings.TrimPrefix(ids[0], "wf:"+w.ID+":")
	event, err := s.DMS.DAL.Events().Find(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	runs := started(t, s, []dms.Event{{ID: event.ID, Type: event.Type, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: event.Data}})
	if len(runs) != 1 || runs[0].WorkflowVersion != 1 || runs[0].ID != ids[0] {
		t.Fatalf("accepted invocation must keep version 1 after an edit/archive: %+v", runs)
	}
}

func TestAutomaticRulesAreLibraryScopedAndIndividuallyControllable(t *testing.T) {
	s, ws, a, b, item := policyFixture(t)
	ctx := t.Context()
	triggers := `[{"type":"manual","selection":"per_item"},{"type":"file.added","collection_id":"` + a.ID + `"},{"type":"file.updated","collection_id":"` + a.ID + `","disabled":true}]`
	w, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Photos only", AutomaticEnabled: new(true), Source: source(triggers, "photos")})
	if err != nil {
		t.Fatal(err)
	}
	events := []dms.Event{{ID: "add", Type: dms.EventFileAdded, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}, {ID: "replace", Type: dms.EventFileUpdated, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}, {ID: "other", Type: dms.EventFileAdded, WorkspaceID: ws.ID, CollectionID: b.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}}
	if runs := started(t, s, events); len(runs) != 1 || runs[0].WorkflowID != w.ID || runs[0].EventID != "add" {
		t.Fatalf("only upload in selected library should match: %+v", runs)
	}
	unscoped := strings.ReplaceAll(triggers, `"collection_id":"`+a.ID+`",`, "")
	unscoped = strings.ReplaceAll(unscoped, `,"collection_id":"`+a.ID+`"`, "")
	if _, err = s.Create(ctx, "alice", ws.ID, Save{Name: "Unscoped", AutomaticEnabled: new(true), Source: source(unscoped, "unscoped")}); err == nil {
		t.Fatal("workspace-wide automatic content processing was accepted")
	}
}

func TestManualSystemFeatureDoesNotConsultAutomaticProvider(t *testing.T) {
	s, ws, a, _, item := policyFixture(t)
	ctx := t.Context()
	root, err := s.UpdateBuiltIn(ctx, "alice", ws.ID, "search.index", SetBuiltIn{AutomaticEnabled: false})
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.UpdateBuiltIn(ctx, "alice", ws.ID, "search.index", SetBuiltIn{CollectionID: a.ID, AutomaticEnabled: false})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.StartRuns(ctx, "alice", root.ID, Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.DMS.DAL.Events().Find(ctx, strings.TrimPrefix(ids[0], "wf:"+root.ID+":"))
	if err != nil {
		t.Fatal(err)
	}
	if runs := started(t, s, []dms.Event{{ID: event.ID, Type: TriggerManual, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: event.Data}}); len(runs) != 1 {
		t.Fatalf("manual request silently suppressed: %+v", runs)
	}
	copy, err := s.CopyBuiltIn(ctx, "alice", ws.ID, "search.index", CopyBuiltIn{Name: "Custom index", CollectionID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if copy.State != "draft" || copy.AutomaticEnabled || copy.AutomaticProvider {
		t.Fatal("copies must not take automatic ownership", copy)
	}
	unchanged, err := s.Get(ctx, "alice", local.ID)
	if err != nil || unchanged.Version != local.Version {
		t.Fatal("copy changed existing automatic settings", unchanged, err)
	}
	if _, err = s.UpdateBuiltIn(ctx, "alice", ws.ID, "search.index", SetBuiltIn{AutomaticEnabled: true, Version: root.Version}); err == nil {
		t.Fatal("workspace-wide automatic built-in accepted")
	}
}

func TestBuiltInRuleChoicesSurviveReleaseSync(t *testing.T) {
	s, ws, lib, _, _ := policyFixture(t)
	release := func(n int) string {
		return `import { workflow } from "@papergo/workflow";
export default workflow<unknown, unknown, { collection: string }>({
  triggers: (p) => ["file.added", "file.updated", "manual"].map((type) => ({ type, collection_id: p.collection })),
  run: () => ({ release: ` + strconv.Itoa(n) + ` }),
});
`
	}
	b := &BuiltIn{Key: "test.policy", Name: "Rule policy", PerCollection: true, ParametersSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Source: release(1)}
	RegisterBuiltIn(b)
	t.Cleanup(func() { delete(builtins, b.Key) })
	disabled := []string{"file.updated"}
	w, err := s.UpdateBuiltIn(t.Context(), "alice", ws.ID, b.Key, SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true, ManualEnabled: new(true), DisabledTriggers: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	b.Source = release(2)
	if err = s.SyncBuiltIns(t.Context()); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Get(t.Context(), "alice", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ManualEnabled || !updated.AutomaticEnabled || updated.CurrentVersion != 2 {
		t.Fatal(updated)
	}
	raw, _ := json.Marshal(updated.Definition)
	if got := disabledTriggerTypes(raw); len(got) != 1 || got[0] != "file.updated" {
		t.Fatal(got)
	}
	rows, err := s.DMS.DAL.Workflows().Triggers(t.Context(), workflows.TriggerFilter{WorkflowID: w.ID})
	if err != nil || len(rows) != 1 || rows[0].Type != "file.added" {
		t.Fatal(rows, err)
	}
}

func TestPublishingPreparedDraftClaimsOnlyItsCollection(t *testing.T) {
	s, ws, lib, _, _ := policyFixture(t)
	copy, err := s.CopyBuiltIn(t.Context(), "alice", ws.ID, "search.index", CopyBuiltIn{Name: "Custom search", CollectionID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.Update(t.Context(), "alice", copy.ID, copy.Version, Save{Name: copy.Name, Source: copy.Source, AutomaticEnabled: new(true)})
	if err != nil || prepared.AutomaticProvider {
		t.Fatal(prepared, err)
	}
	published, err := s.Update(t.Context(), "alice", copy.ID, prepared.Version, Save{Name: copy.Name, Source: copy.Source, State: "published"})
	if err != nil || !published.AutomaticProvider || !published.AutomaticEnabled {
		t.Fatal(published, err)
	}
	if _, err = s.Update(t.Context(), "alice", copy.ID, published.Version, Save{Name: copy.Name, Source: copy.Source, State: "archived"}); err != nil {
		t.Fatal(err)
	}
	active, err := s.UpdateBuiltIn(t.Context(), "alice", ws.ID, "search.index", SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	roles, err := s.CollectionRoles(t.Context(), "alice", lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if role.Role.Key == "search.index" {
			if !role.AutomaticEnabled || role.Workflow == nil || role.Workflow.ID != active.ID {
				t.Fatal(role)
			}
			return
		}
	}
	t.Fatal("search role missing")
}
