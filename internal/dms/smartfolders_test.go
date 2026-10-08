package dms

import (
	"encoding/json"
	"errors"
	"fmt"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/internal/model"
	"testing"
	"time"
)

func smartOK(t *testing.T, s *Service, in SmartFolderInput) *ent.SmartFolder {
	t.Helper()
	f, err := s.CreateSmartFolder(testContext, "alice", in)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSmartFolderPhysicalFoldersAndExactTimes(t *testing.T) {
	s, w, l := fixture(t)
	folder := create(t, s, l.ID, "folder", "Documents", nil)
	create(t, s, l.ID, "item", "Record", nil)
	stamp := time.Date(2026, 10, 8, 12, 30, 0, 123456789, time.FixedZone("offset", 3600))
	_, err := s.Client.Resource.UpdateOneID(folder.ID).SetUpdatedAt(stamp).Save(testContext)
	if err != nil {
		t.Fatal(err)
	}
	f := smartOK(t, s, SmartFolderInput{Name: "Folders", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{IncludeFolders: true, Filter: &FilterExpr{Field: "$modified_at", Value: json.RawMessage(`"2026-10-08T11:30:00.123456789Z"`)}, GroupBy: []SmartFolderGroupBy{{Field: "$modified_at", By: "month"}}}})
	rows, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || rows.Total != 1 || rows.Data[0].Item.ID != folder.ID {
		t.Fatal(rows, err)
	}
	groups, err := s.SmartFolderGroups(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || len(groups.Data) != 1 || groups.Data[0].Value != "2026-10" {
		t.Fatal(groups, err)
	}
	month := "2026-10"
	rows, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{Path: []*string{&month}})
	if err != nil || rows.Total != 1 {
		t.Fatal(rows, err)
	}
	if err = s.Delete(testContext, "alice", folder.ID, folder.Version); err != nil {
		t.Fatal(err)
	}
	rows, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || rows.Total != 0 || len(rows.Data) != 0 {
		t.Fatal("deleted folder remains visible", rows, err)
	}
}

func TestSmartFolderOwnershipConcurrencyAndDefinitions(t *testing.T) {
	s, w, l := fixture(t)
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "reader", Action: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	shared := smartOK(t, s, SmartFolderInput{Name: "Shared", WorkspaceID: &w.ID})
	private := smartOK(t, s, SmartFolderInput{Name: "Private", Personal: true, WorkspaceID: &w.ID})
	global := smartOK(t, s, SmartFolderInput{Name: "Everywhere", Personal: true})
	if _, err = s.SmartFolder(testContext, "reader", private.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("private leaked", err)
	}
	if _, err = s.SmartFolder(testContext, "reader", shared.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.SmartFolders(testContext, "reader", "", "", 100)
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != shared.ID {
		t.Fatal(page, err)
	}
	page, err = s.SmartFolders(testContext, "alice", "", "", 1)
	if err != nil || len(page.Data) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	if _, err = s.CreateSmartFolder(testContext, "reader", SmartFolderInput{Name: "Forbidden", WorkspaceID: &w.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(testContext, "reader", SmartFolderInput{Name: "Mine", WorkspaceID: &w.ID, Personal: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSmartFolder(testContext, "reader", shared.ID, shared.Version); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.UpdateSmartFolder(testContext, "alice", shared.ID, 99, SmartFolderInput{Name: "Bad", WorkspaceID: &w.ID}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.UpdateSmartFolder(testContext, "alice", private.ID, private.Version, SmartFolderInput{Name: "Shared now", WorkspaceID: &w.ID}); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(testContext, "alice", SmartFolderInput{Name: "No scope"}); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(testContext, "alice", SmartFolderInput{Name: "Wrong scope", WorkspaceID: &l.ID}); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err = s.CreateSmartFolder(testContext, "alice", SmartFolderInput{Name: "Invalid", Personal: true, Definition: SmartFolderDefinition{Filter: &FilterExpr{Field: "status", Op: "execute", Value: json.RawMessage(`"a"`)}}}); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err = s.UpdateSmartFolder(testContext, "alice", global.ID, global.Version, SmartFolderInput{Name: "Renamed", Personal: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSmartFolder(testContext, "alice", global.ID, global.Version); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.DeleteSmartFolder(testContext, "alice", global.ID, global.Version+1); err != nil {
		t.Fatal(err)
	}
}

func TestRelativeFiltersAreTypedAndBounded(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 4, 3, 0, time.UTC)
	for ref, want := range map[string]string{"today": "2026-10-08", "weekStart": "2026-10-05", "weekEnd": "2026-10-11", "monthStart": "2026-10-01", "monthEnd": "2026-10-31", "next7Days": "2026-10-15", "last30Days": "2026-09-08", "me": "alice"} {
		got, err := relativeValue(ref, "alice", now)
		if err != nil || got != want {
			t.Fatal(ref, got, err)
		}
	}
	defs := map[string]*ent.FieldDefinition{"due": {Type: fielddefinition.TypeDate}, "at": {Type: fielddefinition.TypeDatetime}, "owner": {Type: fielddefinition.TypeText}}
	for field, want := range map[string]string{"due": `"2026-10-08"`, "at": `"2026-10-08T00:00:00Z"`} {
		got, err := resolveFilter(&FilterExpr{Field: field, ValueRef: "today"}, "alice", now, defs)
		if err != nil || string(got.Value) != want {
			t.Fatal(got, err)
		}
	}
	if _, err := resolveFilter(&FilterExpr{Field: "owner", ValueRef: "today"}, "alice", now, defs); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err := resolveFilter(&FilterExpr{Field: "$tags", ValueRef: "today"}, "alice", now, defs); !isValidation(err) {
		t.Fatal(err)
	}
	if _, err := resolveFilter(&FilterExpr{Field: "due", Value: json.RawMessage(`"2026-10-08"`), ValueRef: "today"}, "alice", now, defs); !isValidation(err) {
		t.Fatal(err)
	}
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "owner", Label: "Owner", Type: "text", Indexed: true})
	create(t, s, l.ID, "item", "Mine", map[string]any{"owner": "alice"})
	create(t, s, l.ID, "item", "Literal", map[string]any{"owner": "me"})
	got, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "owner", ValueRef: "me"}}})
	if err != nil || got.Total != 1 || got.Data[0].Name != "Mine" {
		t.Fatal(got, err)
	}
	got, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "owner", Value: json.RawMessage(`"me"`)}}})
	if err != nil || got.Total != 1 || got.Data[0].Name != "Literal" {
		t.Fatal(got, err)
	}
}

func TestSmartFoldersTermsSurfacesNavigationAndGlobalPaging(t *testing.T) {
	s, w, a := fixture(t)
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "reader", Action: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	b := create(t, s, w.ID, "list", "Papers", nil)
	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{Key: "projects", Name: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Apollo"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Lander", ParentID: &root.ID})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []*ent.Resource{a, b} {
		fieldOK(t, s, c.ID, CreateField{Key: fmt.Sprintf("project_%d", i), Label: "Project", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, Multiple: i == 1}})
		fieldOK(t, s, c.ID, CreateField{Key: "issued", Label: "Issued", Type: "date", Indexed: true})
		fieldOK(t, s, c.ID, CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true})
	}
	one := create(t, s, a.ID, "item", "Plan", map[string]any{"project_0": root.ID, "issued": "2025-03-01", "status": "open"})
	two := create(t, s, b.ID, "item", "Spec", map[string]any{"project_1": []any{child.ID}, "issued": "2026-02-01", "status": "open"})
	three := create(t, s, b.ID, "item", "Empty date", map[string]any{"project_1": []any{root.ID}, "status": "open"})
	for _, item := range []*ent.Resource{one, two, three} {
		if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
			t.Fatal(err)
		}
	}
	create(t, s, a.ID, "item", "Draft secret", map[string]any{"project_0": root.ID, "issued": "2026-03-01", "status": "open"})
	f := smartOK(t, s, SmartFolderInput{Name: "Apollo", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Terms: []string{root.ID}, GroupBy: []SmartFolderGroupBy{{Field: "issued", By: "year"}, {Field: "status"}}}})
	got, err := s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Limit: 1})
	if err != nil || got.Total != 3 || len(got.Data) != 1 || got.Data[0].Item.ID != three.ID || got.Data[0].CollectionName != "Papers" {
		t.Fatal(got, err)
	}
	seen := map[string]bool{got.Data[0].Item.ID: true}
	for got.NextCursor != "" {
		got, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Limit: 1, After: got.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range got.Data {
			if seen[entry.Item.ID] {
				t.Fatal("paging duplicate")
			}
			seen[entry.Item.ID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
	groups, err := s.SmartFolderGroups(testContext, "reader", f.ID, SmartFolderQueryRequest{})
	if err != nil || len(groups.Data) != 3 || groups.Data[0].Value != "2025" || groups.Data[1].Value != "2026" || groups.Data[2].Value != nil {
		t.Fatal(groups, err)
	}
	year := "2026"
	status := "open"
	groups, err = s.SmartFolderGroups(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{&year}})
	if err != nil || len(groups.Data) != 1 || groups.Data[0].Value != status || groups.Data[0].Count != 1 {
		t.Fatal(groups, err)
	}
	got, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{&year, &status}})
	if err != nil || got.Total != 1 || got.Data[0].Item.ID != two.ID {
		t.Fatal(got, err)
	}
	got, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{nil}})
	if err != nil || got.Total != 1 || got.Data[0].Item.ID != three.ID {
		t.Fatal(got, err)
	}
	got, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || got.Total != 4 {
		t.Fatal(got, err)
	}
	if _, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{&year, &status, &status}}); !isValidation(err) {
		t.Fatal(err)
	}
	first, err := s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{Limit: 1, After: first.NextCursor}); !isValidation(err) {
		t.Fatal("cursor caller binding", err)
	}
	if _, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{&year}, After: first.NextCursor}); !isValidation(err) {
		t.Fatal("cursor path binding", err)
	}
	// Unpublished edits do not change a reader's matching values or grouping.
	values := map[string]any{"project_1": []any{root.ID}, "issued": "2027-01-01", "status": "closed"}
	if _, err = s.Update(testContext, "alice", two.ID, latest(t, s, two.ID).Version, UpdateResource{Values: &values}); err != nil {
		t.Fatal(err)
	}
	got, err = s.QuerySmartFolder(testContext, "reader", f.ID, SmartFolderQueryRequest{Path: []*string{&year}})
	if err != nil || got.Total != 1 {
		t.Fatal("draft leaked", got, err)
	}
}

func TestSmartFolderSkipsIncompatibleCollectionsAndSelectsTypesTemplates(t *testing.T) {
	s, w, a := fixture(t)
	b := create(t, s, w.ID, "list", "Papers", nil)
	fieldOK(t, s, a.ID, CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true})
	typ := typeOK(t, s, a.ID, ContentTypeInput{Key: "invoice", Name: "Invoice", FieldKeys: []string{"status"}})
	item, err := s.Create(testContext, "alice", a.ID, CreateResource{Kind: "item", Name: "Invoice", ContentTypeID: typ.ID, Values: map[string]any{"status": "open"}})
	if err != nil {
		t.Fatal(err)
	}
	create(t, s, b.ID, "item", "No status", nil)
	f := smartOK(t, s, SmartFolderInput{Name: "Open", Personal: true, Definition: SmartFolderDefinition{Filter: &FilterExpr{Field: "status", Value: json.RawMessage(`"open"`)}, ContentTypes: []string{"INVOICE"}}})
	got, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || got.Total != 1 || got.Data[0].Item.ID != item.ID {
		t.Fatal(got, err)
	}
	tpl, err := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "invoice_template", Name: "Invoice template"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyTemplate(testContext, "alice", a.ID, tpl.ID, latest(t, s, a.ID).Version, tpl.Version, typ.ID); err != nil {
		t.Fatal(err)
	}
	f = smartOK(t, s, SmartFolderInput{Name: "Template", Personal: true, Definition: SmartFolderDefinition{Templates: []string{"INVOICE_TEMPLATE"}}})
	got, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || got.Total != 1 {
		t.Fatal(got, err)
	}
	f = smartOK(t, s, SmartFolderInput{Name: "Invalid field", Personal: true, Definition: SmartFolderDefinition{Collections: []string{b.Name}, Filter: &FilterExpr{Field: "status", Value: json.RawMessage(`"open"`)}}})
	if _, err = s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{}); !isValidation(err) {
		t.Fatal(err)
	}
}
