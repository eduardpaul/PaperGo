package workflow

import (
	"encoding/json"
	"papergo/ent"
	"papergo/ent/workflowtrigger"
	"papergo/internal/dms"
	"papergo/internal/testutil"
	"strings"
	"testing"
)

func policyFixture(t *testing.T) (*Service, *ent.Resource, *ent.Resource, *ent.Resource, *ent.Resource) {
	t.Helper()
	ds := dms.NewService(testutil.Database(t).SQL)
	ctx := t.Context()
	create := func(parent string, in dms.CreateResource) *ent.Resource {
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
	raw := json.RawMessage(`{"triggers":[{"type":"manual","selection":"per_item"},{"type":"file.added","collection_id":"` + a.ID + `"}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`)
	w, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Process a photo", Definition: raw})
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
	if n, e := s.DMS.Client.WorkflowTrigger.Query().Where(workflowtrigger.WorkflowIDEQ(w.ID)).Count(ctx); e != nil || n != 0 {
		t.Fatal("manual use must not subscribe to events", n, e)
	}
	changed := json.RawMessage(strings.Replace(string(raw), `"flow":`, `"variables":{"new_version":true},"flow":`, 1))
	_, err = s.Update(ctx, "alice", w.ID, w.Version, Save{Name: w.Name, State: "archived", Definition: changed})
	if err != nil {
		t.Fatal(err)
	}
	eventID := strings.TrimPrefix(ids[0], "wf:"+w.ID+":")
	event, err := s.DMS.Client.DomainEvent.Get(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DMS.Write(ctx, func(tx *dms.Service) error {
		runs, _, err := Match(ctx, tx, []dms.Event{{ID: event.ID, Type: event.Type, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: event.Data}}, nil)
		if err == nil && (len(runs) != 1 || runs[0].Version != 1) {
			t.Fatalf("accepted invocation must keep version 1 after an edit/archive: %+v", runs)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticRulesAreLibraryScopedAndIndividuallyControllable(t *testing.T) {
	s, ws, a, b, item := policyFixture(t)
	ctx := t.Context()
	raw := json.RawMessage(`{"triggers":[{"type":"manual","selection":"per_item"},{"type":"file.added","collection_id":"` + a.ID + `"},{"type":"file.updated","collection_id":"` + a.ID + `","disabled":true}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`)
	w, err := s.Create(ctx, "alice", ws.ID, Save{Name: "Photos only", AutomaticEnabled: new(true), Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	events := []dms.Event{{ID: "add", Type: dms.EventFileAdded, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}, {ID: "replace", Type: dms.EventFileUpdated, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}, {ID: "other", Type: dms.EventFileAdded, WorkspaceID: ws.ID, CollectionID: b.ID, ResourceID: item.ID, Actor: "alice", Data: map[string]any{"source": "upload"}}}
	err = s.DMS.Write(ctx, func(tx *dms.Service) error {
		runs, _, err := Match(ctx, tx, events, nil)
		if err == nil && (len(runs) != 1 || runs[0].WorkflowID != w.ID || runs[0].Event.ID != "add") {
			t.Fatalf("only upload in selected library should match: %+v", runs)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	unscoped := json.RawMessage(strings.ReplaceAll(string(raw), `"collection_id":"`+a.ID+`",`, ""))
	unscoped = json.RawMessage(strings.ReplaceAll(string(unscoped), `,"collection_id":"`+a.ID+`"`, ""))
	if _, err = s.Create(ctx, "alice", ws.ID, Save{Name: "Unscoped", AutomaticEnabled: new(true), Definition: unscoped}); err == nil {
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
	event, err := s.DMS.Client.DomainEvent.Get(ctx, strings.TrimPrefix(ids[0], "wf:"+root.ID+":"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.DMS.Write(ctx, func(tx *dms.Service) error {
		runs, _, err := Match(ctx, tx, []dms.Event{{ID: event.ID, Type: TriggerManual, WorkspaceID: ws.ID, CollectionID: a.ID, ResourceID: item.ID, Actor: "alice", Data: event.Data}}, nil)
		if err == nil && len(runs) != 1 {
			t.Fatalf("manual request silently suppressed: %+v", runs)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
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
	b := &BuiltIn{Key: "test.policy", Name: "Rule policy", PerCollection: true, ParametersSchema: map[string]any{"type": "object", "properties": map[string]any{}}, Definition: `{"triggers":[{"type":"file.added","collection_id":"{param:collection}"},{"type":"file.updated","collection_id":"{param:collection}"},{"type":"manual","collection_id":"{param:collection}"}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`}
	RegisterBuiltIn(b)
	t.Cleanup(func() { delete(builtins, b.Key) })
	disabled := []string{"file.updated"}
	w, err := s.UpdateBuiltIn(t.Context(), "alice", ws.ID, b.Key, SetBuiltIn{CollectionID: lib.ID, AutomaticEnabled: true, ManualEnabled: new(true), DisabledTriggers: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	b.Definition = strings.Replace(b.Definition, `"flow":`, `"variables":{"release":2},"flow":`, 1)
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
	rows, err := s.DMS.Client.WorkflowTrigger.Query().Where(workflowtrigger.WorkflowIDEQ(w.ID)).All(t.Context())
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
	raw, _ := json.Marshal(copy.Definition)
	prepared, err := s.Update(t.Context(), "alice", copy.ID, copy.Version, Save{Name: copy.Name, Definition: raw, AutomaticEnabled: new(true)})
	if err != nil || prepared.AutomaticProvider {
		t.Fatal(prepared, err)
	}
	published, err := s.Update(t.Context(), "alice", copy.ID, prepared.Version, Save{Name: copy.Name, Definition: raw, State: "published"})
	if err != nil || !published.AutomaticProvider || !published.AutomaticEnabled {
		t.Fatal(published, err)
	}
	if _, err = s.Update(t.Context(), "alice", copy.ID, published.Version, Save{Name: copy.Name, Definition: raw, State: "archived"}); err != nil {
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
