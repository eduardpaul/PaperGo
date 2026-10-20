package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"papergo/internal/model"
	"testing"
	"time"
)

func bulkStep(t *testing.T, s *Service) {
	t.Helper()
	if worked, e := s.operationStep(testContext); !worked || e != nil {
		t.Fatal("operation step", worked, e)
	}
}
func bulkStatus(t *testing.T, s *Service, id string) *OperationView {
	t.Helper()
	op, e := s.Operation(testContext, "alice", id)
	if e != nil {
		t.Fatal(e)
	}
	return op
}

func TestFilteredBulkUpdateFreezesSelectionResumesAndMerges(t *testing.T) {
	// One item per step, to stop and resume between items.
	defer func(n int) { bulkStepItems = n }(bulkStepItems)
	bulkStepItems = 1
	s, _, l := fixture(t)
	for _, f := range []CreateField{{Key: "stage", Label: "Stage", Type: "text", Indexed: true}, {Key: "keep", Label: "Keep", Type: "text"}, {Key: "amount", Label: "Amount", Type: "integer"}} {
		if _, e := s.CreateField(testContext, "alice", l.ID, f); e != nil {
			t.Fatal(e)
		}
	}
	folder := create(t, s, l.ID, "folder", "Nested", nil)
	a := create(t, s, l.ID, "item", "A", map[string]any{"stage": "todo", "keep": "A"})
	b := create(t, s, folder.ID, "item", "B", map[string]any{"stage": "todo", "keep": "B"})
	c := create(t, s, l.ID, "item", "C", map[string]any{"stage": "done", "keep": "C"})
	if _, e := s.Publish(testContext, "alice", a.ID, a.Version); e != nil {
		t.Fatal(e)
	}
	op, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "stage", Value: json.RawMessage(`"todo"`)}}, Values: map[string]any{"stage": "done", "amount": json.Number("9007199254740993")}})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s) // selection, without edits
	if got := bulkStatus(t, s, op.ID); got.Matched != 2 || got.Processed != 0 {
		t.Fatal(got)
	}
	late := create(t, s, l.ID, "item", "Late", map[string]any{"stage": "todo"})
	bulkStep(t, s)
	if got := bulkStatus(t, s, op.ID); got.Processed != 1 || got.Updated != 1 || got.Progress != 50 || got.Status != operations.StatusPending {
		t.Fatal(got)
	}
	// A new service resumes persisted selection and completion markers.
	resumed := NewService(s.db)
	if e = resumed.runOperations(testContext); e != nil {
		t.Fatal(e)
	}
	got := bulkStatus(t, resumed, op.ID)
	if got.Status != operations.StatusSucceeded || got.Matched != 2 || got.Updated != 2 || got.Failed != 0 || got.Progress != 100 {
		t.Fatal(got)
	}
	for _, r := range []*resources.Resource{a, b} {
		live := latest(t, s, r.ID)
		if live.Values["stage"] != "done" || live.Values["keep"] != r.Name || fmt.Sprint(live.Values["amount"]) != "9007199254740993" {
			t.Fatal("merge/precision", live.Values)
		}
	}
	for _, r := range []*resources.Resource{c, late} {
		if latest(t, s, r.ID).Version != r.Version {
			t.Fatal("unselected item edited", r.ID)
		}
	}
	pub, e := s.GetSurface(testContext, "alice", a.ID, "published")
	if e != nil || pub.Values["stage"] != "todo" {
		t.Fatal("published content edited", pub, e)
	}
	if n, e := s.DAL.Operations().CountItems(testContext, op.ID); e != nil || n != 0 {
		t.Fatal("completed selection retained", n, e)
	}
	if worked, e := resumed.operationStep(testContext); worked || e != nil {
		t.Fatal("finished operation replayed", worked, e)
	}
}

func TestFilteredBulkUpdatePartialFailuresRollbackEveryItemSideEffect(t *testing.T) {
	s, _, l := fixture(t)
	if _, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "code", Label: "Code", Type: "text", Indexed: true, Options: model.FieldOptions{Unique: true}}); e != nil {
		t.Fatal(e)
	}
	items := []*resources.Resource{}
	for i := 0; i < 3; i++ {
		items = append(items, create(t, s, l.ID, "item", fmt.Sprintf("Item %d", i), map[string]any{"code": fmt.Sprint(i)}))
	}
	op, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Values: map[string]any{"code": "same"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.runOperations(testContext); e != nil {
		t.Fatal(e)
	}
	got := bulkStatus(t, s, op.ID)
	if got.Status != operations.StatusSucceeded || got.Updated != 1 || got.Failed != 2 || got.Processed != 3 || len(got.Failures) != 2 || got.Progress != 100 {
		t.Fatal(got)
	}
	for _, r := range items {
		live := latest(t, s, r.ID)
		want := 1
		if live.Values["code"] == "same" {
			want = 2
		}
		if live.Version != want {
			t.Fatal("failed edit consumed version", live)
		}
		if n, e := countRevisions(s, r.ID); e != nil || n != want {
			t.Fatal("failed edit retained revision", n, e)
		}
		audits, e := s.DAL.Audit().Count(testContext, auditstore.Filter{ResourceID: r.ID, Action: "resource.update"})
		if e != nil || audits != int64(want-1) {
			t.Fatal("failed edit retained audit", audits, e)
		}
	}
	// A database failure after revision creation stops the operation, rolls back
	// the current item and retains earlier operations' successful edits.
	daltest.Reject(t, s.DAL, "audit_events", daltest.OnInsert, daltest.Row{"action": "resource.update"})
	before, e := s.DAL.Events().Count(testContext, eventstore.Filter{Types: []string{EventItemUpdated}})
	if e != nil {
		t.Fatal(e)
	}
	op, e = s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Values: map[string]any{"code": nil}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.runOperations(testContext); e == nil {
		t.Fatal("database failure did not stop the operation")
	}
	got = bulkStatus(t, s, op.ID)
	if got.Status != operations.StatusFailed || got.Failed != 0 || got.Updated != 0 || got.Processed != 0 {
		t.Fatal(got)
	}
	if after, e := s.DAL.Events().Count(testContext, eventstore.Filter{Types: []string{EventItemUpdated}}); e != nil || after != before {
		t.Fatal("failed item emitted events", before, after, e)
	}
	for _, r := range items {
		live := latest(t, s, r.ID)
		want := 1
		if live.Values["code"] == "same" {
			want = 2
		}
		if live.Version != want || live.Values["code"] == nil {
			t.Fatal("database failure retained item changes", live)
		}
		if n, e := countRevisions(s, r.ID); e != nil || n != want {
			t.Fatal("database failure retained revisions", n, e)
		}
	}
}

func TestFilteredBulkUpdateRevocationDeletionValidationAndPrivacy(t *testing.T) {
	s, w, l := fixture(t)
	if _, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{}); e == nil {
		t.Fatal("empty patch")
	}
	if _, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "unknown", Value: json.RawMessage(`1`)}}, Values: map[string]any{"bad": true}}); e == nil {
		t.Fatal("invalid query")
	}
	if n, e := s.DAL.Operations().Count(testContext, operations.Filter{}); e != nil || n != 0 {
		t.Fatal("invalid request queued", n, e)
	}
	a := create(t, s, l.ID, "item", "A", nil)
	b := create(t, s, l.ID, "item", "B", nil)
	name := "Edited"
	op, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	if e = s.Delete(testContext, "alice", a.ID, a.Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	if e = s.runOperations(testContext); e != nil {
		t.Fatal(e)
	}
	got := bulkStatus(t, s, op.ID)
	if got.Updated != 1 || got.Failed != 1 || got.Failures[0].ItemID != a.ID || latest(t, s, b.ID).Name != name {
		t.Fatal(got)
	}
	if e = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Operation(testContext, "bob", op.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("other user read results", e)
	}
	page, e := s.Operations(testContext, "bob", l.ID, "", 100)
	if e != nil || len(page.Data) != 0 {
		t.Fatal("other user listed results", page, e)
	}
	op, e = s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	if e = s.Directory().SetAssignment(testContext, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, true); e != nil {
		t.Fatal(e)
	}
	if worked, e := s.operationStep(testContext); !worked || !errors.Is(e, ErrForbidden) {
		t.Fatal("role revocation ignored", worked, e)
	}
	got, e = findOperation(s, op.ID)
	if e != nil || got.Status != operations.StatusFailed || got.Processed != 0 {
		t.Fatal(got, e)
	}
	if n, e := s.DAL.Operations().CountItems(testContext, op.ID); e != nil || n != 0 {
		t.Fatal("failed operation kept selection", n, e)
	}
}

func TestFilteredBulkUpdateFailureCapProgressAndCancellation(t *testing.T) {
	s, _, l := fixture(t)
	ops := []BulkOperation{}
	for i := 0; i < 105; i++ {
		ops = append(ops, BulkOperation{Action: "create", Create: &BulkCreate{Name: fmt.Sprint(i)}})
		if len(ops) == 100 || i == 104 {
			if _, e := s.Bulk(testContext, "alice", l.ID, BulkRequest{Operations: ops}); e != nil {
				t.Fatal(e)
			}
			ops = nil
		}
	}
	op, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Values: map[string]any{"unknown": true}})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	if _, e = s.operationStep(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if got := bulkStatus(t, s, op.ID); got.Processed != 0 || got.Status != operations.StatusPending {
		t.Fatal("cancellation consumed selection", got)
	}
	if e = s.runOperations(testContext); e != nil {
		t.Fatal(e)
	}
	got := bulkStatus(t, s, op.ID)
	if got.Matched != 105 || got.Failed != 105 || got.Updated != 0 || got.Processed != 105 || got.Progress != 100 || len(got.Failures) != 50 {
		t.Fatal(got)
	}
	op, e = s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "$name", Value: json.RawMessage(`"missing"`)}}, Values: map[string]any{"unknown": true}})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	got = bulkStatus(t, s, op.ID)
	if got.Matched != 0 || got.Progress != 100 || got.Status != operations.StatusSucceeded {
		t.Fatal(got)
	}
}

func TestFilteredBulkUpdateItemACLsAndNullRemoval(t *testing.T) {
	s, w, l := fixture(t)
	if e := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "memo", Label: "Memo", Type: "text", Options: model.FieldOptions{DefaultValue: json.RawMessage(`"default"`)}}); e != nil {
		t.Fatal(e)
	}
	items := map[string]*resources.Resource{}
	for _, name := range []string{"hidden", "draft reader", "revoked", "editable"} {
		items[name] = create(t, s, l.ID, "item", name, map[string]any{"memo": "keep"})
	}
	set := func(name string, grants []Permission) {
		t.Helper()
		r := latest(t, s, items[name].ID)
		if _, e := s.SetPermissions(testContext, "alice", r.ID, r.Version, Permissions{Grants: grants}); e != nil {
			t.Fatal(e)
		}
	}
	draft := []Permission{{PrincipalType: "user", PrincipalID: "bob", Action: "read_draft"}}
	set("hidden", []Permission{})
	set("draft reader", draft)
	op, e := s.StartBulkUpdate(testContext, "bob", l.ID, FilteredBulkUpdate{Values: map[string]any{"memo": nil}})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	set("revoked", draft)
	if e = s.runOperations(testContext); e != nil {
		t.Fatal(e)
	}
	got, e := s.Operation(testContext, "bob", op.ID)
	if e != nil || got.Matched != 3 || got.Updated != 1 || got.Failed != 2 || len(got.Failures) != 2 {
		t.Fatal(got, e)
	}
	for _, failure := range got.Failures {
		if failure.Reason != "Access denied." || failure.ItemID == items["hidden"].ID {
			t.Fatal("permission/selection", failure)
		}
	}
	if _, exists := latest(t, s, items["editable"].ID).Values["memo"]; exists {
		t.Fatal("null did not remove value or reapplied default")
	}
	for _, name := range []string{"hidden", "draft reader", "revoked"} {
		if latest(t, s, items[name].ID).Values["memo"] != "keep" {
			t.Fatal("inaccessible item edited", name)
		}
	}
}

func TestBulkUpdateEditsABatchOfItemsPerStep(t *testing.T) {
	// A generous budget: under the race detector one item can take tens of milliseconds.
	defer func(budget time.Duration) { bulkStepBudget = budget }(bulkStepBudget)
	bulkStepBudget = time.Minute
	s, _, l := fixture(t)
	if _, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "stage", Label: "Stage", Type: "text", Indexed: true}); e != nil {
		t.Fatal(e)
	}
	ids := map[string]bool{}
	for i := range 30 {
		ids[create(t, s, l.ID, "item", fmt.Sprintf("Item %02d", i), map[string]any{"stage": "todo"}).ID] = true
	}
	gone := create(t, s, l.ID, "item", "Gone", map[string]any{"stage": "todo"})
	op, e := s.StartBulkUpdate(testContext, "alice", l.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "stage", Value: json.RawMessage(`"todo"`)}}, Values: map[string]any{"stage": "done"}})
	if e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s) // selection
	if e = s.Delete(testContext, "alice", gone.ID, latest(t, s, gone.ID).Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	bulkStep(t, s)
	// One step edits every item within its budget.
	if got := bulkStatus(t, s, op.ID); got.Processed != 31 {
		t.Fatal("one item per step", got.Processed)
	}
	for bulkStatus(t, s, op.ID).Status == operations.StatusPending {
		bulkStep(t, s)
	}
	got := bulkStatus(t, s, op.ID)
	if got.Status != operations.StatusSucceeded || got.Matched != 31 || got.Processed != 31 || got.Updated != 30 || got.Failed != 1 || len(got.Failures) != 1 || got.Failures[0].ItemID != gone.ID {
		t.Fatal("bulk result", got)
	}
	for id := range ids {
		if v := latest(t, s, id).Values["stage"]; v != "done" {
			t.Fatal("not updated", id, v)
		}
	}
}
