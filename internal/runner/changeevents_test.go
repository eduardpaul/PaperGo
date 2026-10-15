package runner

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"papergo/ent/domainevent"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/workflow"
)

func TestPermissionsChangedIsDeliveredToEachAffectedEntry(t *testing.T) {
	f := setup(t)
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	collectionFlow := f.workflow(t, "Collection access", `{"triggers":[{"type":"permissions.changed","collection_id":"$LIST"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	workspaceFlow := f.workflow(t, "Workspace access", `{"triggers":[{"type":"permissions.changed"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	replaceOnly := f.workflow(t, "ACL edits", `{"triggers":[{"type":"permissions.changed","actions":["replace"]}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	other, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "list", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.dms.Create(ctx, "alice", f.list.ID, dms.CreateResource{Kind: "folder", Name: "Folder"})
	if err != nil {
		t.Fatal(err)
	}
	inside, err := f.dms.Create(ctx, "alice", folder.ID, dms.CreateResource{Kind: "item", Name: "Inside"})
	if err != nil {
		t.Fatal(err)
	}
	item := f.item(t, "alice", "Item", nil)
	// The list's own scope reaches the folder, the item inside it and the
	// item; the folder's reaches the folder and the item inside; the item's
	// only itself; the empty list's nothing.
	for _, id := range []string{f.list.ID, folder.ID, item.ID, other.ID} {
		r, err := f.dms.Get(ctx, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.dms.SetPermissions(ctx, "alice", id, r.Version, dms.Permissions{CopyInherited: true}); err != nil {
			t.Fatal(err)
		}
		// Each change reaches the entries it reaches when dispatched:
		// dispatch it before the next change narrows them.
		if _, err = f.r.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// A membership change reaches every entry of the workspace.
	if err = f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "carol", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{folder.ID: 3, inside.ID: 3, item.ID: 3}
	for _, flow := range []workflow.Workflow{collectionFlow, workspaceFlow} {
		got := map[string]int{}
		for _, run := range f.runs(t, flow.ID, 9) {
			if run.Status != "completed" || run.EventType != dms.EventPermissionsChanged || run.ItemID == nil {
				t.Fatal(run)
			}
			got[*run.ItemID]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s deliveries %v, want %v", flow.Name, got, want)
		}
	}
	// Membership changes are not replaces.
	f.runs(t, replaceOnly.ID, 6)
	run, err := f.r.Run(ctx, "alice", f.runs(t, collectionFlow.ID, 9)[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.EventID == "" || !strings.Contains(run.EventID, ":") {
		t.Fatalf("delivery event id %q", run.EventID)
	}
}

// Large changes are delivered in batches, in order with later events, and
// a change nobody listens to is not expanded at all.
func TestPermissionsChangedFansOutInBatches(t *testing.T) {
	saved := workflow.PermissionBatch
	workflow.PermissionBatch = 2
	t.Cleanup(func() { workflow.PermissionBatch = saved })
	f := setup(t)
	f.searchOff(t)
	var ids []string
	for i := range 5 {
		ids = append(ids, f.item(t, "alice", "Item "+strconv.Itoa(i), nil).ID)
	}
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	// Nobody listens: the event is dispatched without expansion.
	list := f.head(t, f.list.ID)
	if _, err := f.dms.SetPermissions(ctx, "alice", f.list.ID, list.Version, dms.Permissions{CopyInherited: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.Client.DomainEvent.Query().Where(domainevent.TypeEQ(dms.EventPermissionsChanged), domainevent.ResourceIDEQ(f.list.ID)).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].DispatchedAt == nil || rows[0].FanoutCursor != nil {
		t.Fatalf("unheard change: %+v %v", rows, err)
	}
	flow := f.workflow(t, "Access", `{"triggers":[{"type":"permissions.changed","collection_id":"$LIST"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	after := f.workflow(t, "After", `{"triggers":[{"type":"item.updated","collection_id":"$LIST"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	list = f.head(t, f.list.ID)
	if _, err = f.dms.SetPermissions(ctx, "alice", f.list.ID, list.Version, dms.Permissions{Inherit: true}); err != nil {
		t.Fatal(err)
	}
	first := f.head(t, ids[0])
	if _, err = f.dms.Update(ctx, "alice", ids[0], first.Version, dms.UpdateResource{Values: &map[string]any{"note": "later"}}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, flow.ID, 5)
	got := []string{}
	for _, run := range runs {
		got = append(got, *run.ItemID)
	}
	slices.Sort(got)
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	if !slices.Equal(got, sorted) {
		t.Fatalf("deliveries %v, want %v", got, sorted)
	}
	// The later change waited for the whole expansion.
	later := f.runs(t, after.ID, 1)[0]
	for _, run := range runs {
		if run.CreatedAt.After(later.CreatedAt) {
			t.Fatalf("a delivery started after a later event: %v > %v", run.CreatedAt, later.CreatedAt)
		}
	}
}

// Moving an entry into another permission scope is a permission change for
// it and what inherits from it.
func TestMovingIntoAnotherScopeRaisesPermissionsChanged(t *testing.T) {
	f := setup(t)
	flow := f.workflow(t, "Moves", `{"triggers":[{"type":"permissions.changed","collection_id":"$LIST","actions":["move"]}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	private, err := f.dms.Create(ctx, "alice", f.list.ID, dms.CreateResource{Kind: "folder", Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	if private, err = f.dms.SetPermissions(ctx, "alice", private.ID, private.Version, dms.Permissions{CopyInherited: true}); err != nil {
		t.Fatal(err)
	}
	box, err := f.dms.Create(ctx, "alice", f.list.ID, dms.CreateResource{Kind: "folder", Name: "Box"})
	if err != nil {
		t.Fatal(err)
	}
	inside, err := f.dms.Create(ctx, "alice", box.ID, dms.CreateResource{Kind: "item", Name: "Inside"})
	if err != nil {
		t.Fatal(err)
	}
	box = f.head(t, box.ID)
	if _, err = f.dms.MoveItem(ctx, "alice", box.ID, box.Version, dms.MoveItem{CollectionID: f.list.ID, ParentID: private.ID}); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, run := range f.runs(t, flow.ID, 2) {
		got[*run.ItemID] = true
	}
	if !got[box.ID] || !got[inside.ID] {
		t.Fatalf("move deliveries %v", got)
	}
}

func workspaceOnlyAuthor(t *testing.T, f *fixture) string {
	t.Helper()
	if err := f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	directory := f.dms.Directory()
	role, err := directory.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Workspace only", Scopes: []string{"workspace.read", "list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Type: "user", ID: "bob"}
	if err = directory.SetAssignment(ctx, "alice", role.ID, principal, false); err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "alice", identity.MemberRole, principal, true); err != nil {
		t.Fatal(err)
	}
	reader, err := directory.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Taxonomy reader", Scopes: []string{"taxonomy.read"}})
	if err != nil {
		t.Fatal(err)
	}
	return reader.ID
}

func TestTermChangedSubscriptionsRequireTaxonomyRead(t *testing.T) {
	f := setup(t)
	readerRole := workspaceOnlyAuthor(t, f)
	definition := json.RawMessage(`{"triggers":[{"type":"term.changed"}],"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	in := workflow.Save{Name: "Term curation", Definition: definition}
	if _, err := f.wf.Create(ctx, "bob", f.ws.ID, in); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("unreadable taxonomy subscription saved: %v", err)
	}
	existing, err := f.wf.Create(ctx, "alice", f.ws.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.Update(ctx, "bob", existing.ID, existing.Version, in); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("unreadable taxonomy subscription updated: %v", err)
	}
	directory := f.dms.Directory()
	principal := identity.Principal{Type: "user", ID: "bob"}
	if err = directory.SetAssignment(ctx, "alice", readerRole, principal, false); err != nil {
		t.Fatal(err)
	}
	authorized, err := f.wf.Create(ctx, "bob", f.ws.ID, workflow.Save{Name: "Authorized curation", Definition: definition})
	if err != nil {
		t.Fatal("taxonomy reader could not subscribe", err)
	}
	if err = directory.SetAssignment(ctx, "alice", readerRole, principal, true); err != nil {
		t.Fatal(err)
	}
	// Match directly so revocation happens before dispatch, without a race
	// against the fixture's background dispatcher.
	err = f.dms.Write(ctx, func(tx *dms.Service) error {
		starts, _, err := workflow.Match(ctx, tx, []dms.Event{{ID: dms.NewEventID(), Type: dms.EventTermChanged, WorkspaceID: f.ws.ID, Actor: "alice"}}, nil)
		if err != nil {
			return err
		}
		// search.index runs as the system subject, which always reads the taxonomy.
		starts = slices.DeleteFunc(starts, func(s workflow.RunInput) bool { return s.System })
		if len(starts) != 1 || starts[0].WorkflowID != existing.ID {
			t.Fatalf("revoked author %s received taxonomy event: %+v", authorized.ID, starts)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTermChangedRunRechecksTaxonomyReadAfterWait(t *testing.T) {
	f := setup(t)
	readerRole := workspaceOnlyAuthor(t, f)
	directory := f.dms.Directory()
	principal := identity.Principal{Type: "user", ID: "bob"}
	if err := directory.SetAssignment(ctx, "alice", readerRole, principal, false); err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`{"triggers":[{"type":"term.changed"}],"flow":{"start":"wait","nodes":{"wait":{"activity":"delay","inputs":{"duration":"1s"},"next":{"done":"read"}},"read":{"activity":"fail","inputs":{"message":"{trigger:data.current.name}"}}}}}`)
	flow, err := f.wf.Create(ctx, "bob", f.ws.ID, workflow.Save{Name: "Delayed taxonomy read", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "Restricted taxonomy name"
	if err = f.dms.Write(ctx, func(tx *dms.Service) error {
		tx.Raise(ctx, dms.EventTermChanged, "alice", f.ws.ID, nil, "", map[string]any{"current": map[string]any{"name": secret}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var runID string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		err = f.db.SQL.QueryRowContext(ctx, "SELECT r.id FROM workflow_runs r JOIN runner_step_results s ON s.run_id=r.id WHERE r.workflow_id=? AND s.step='wait'", flow.ID).Scan(&runID)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("run never reached its wait", err)
	}
	if err = directory.SetAssignment(ctx, "alice", readerRole, principal, true); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.TermGroups(ctx, "bob", "", 10); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("taxonomy read was not revoked: %v", err)
	}
	runs := f.runs(t, flow.ID, 1)
	if runs[0].Status != "failed" || !strings.Contains(runs[0].Error, "taxonomy.read") {
		t.Fatalf("revoked author executed taxonomy node: %+v", runs[0])
	}
	run, err := f.r.Run(ctx, "bob", runID)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := json.Marshal(run)
	if err != nil || strings.Contains(string(visible), secret) {
		t.Fatalf("taxonomy payload leaked in run output: %s, %v", visible, err)
	}
}

func TestTermRelabelStartsRunsInEveryLiveWorkspace(t *testing.T) {
	f := setup(t)
	other, err := f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	definition := `{"triggers":[{"type":"term.changed"}],"flow":{"start":"check","nodes":{"check":{"activity":"if","inputs":{"left":"{trigger:data.current.labels.es}","op":"eq","right":"Contabilidad"},"next":{"true":"done","false":"bad"}},"done":{"activity":"end"},"bad":{"activity":"fail","inputs":{"message":"missing changed label"}}}}}`
	first := f.workflow(t, "Term curation", definition)
	second, err := f.wf.Create(ctx, "alice", other.ID, workflow.Save{Name: "Term curation", Definition: json.RawMessage(definition)})
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Corporate"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.UpdateTerm(ctx, "alice", term.ID, term.Version, dms.TermInput{Name: "Finance", Labels: map[string]string{"es": "Contabilidad"}}); err != nil {
		t.Fatal(err)
	}
	otherScope := *f
	otherScope.ws = other
	for _, runs := range [][]Run{f.runs(t, first.ID, 1), otherScope.runs(t, second.ID, 1)} {
		if runs[0].Status != "completed" || runs[0].ItemID != nil || runs[0].EventType != dms.EventTermChanged {
			t.Fatal(runs)
		}
	}
}
