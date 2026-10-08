package dms

import (
	"encoding/json"
	"errors"
	"papergo/internal/model"
	"testing"
)

func TestSmartFolderClassificationAndUnclassification(t *testing.T) {
	s, w, l := fixture(t)
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
	other, err := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []CreateField{{Key: "projects", Label: "Projects", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}}, {Key: "status", Label: "Status", Type: "text", Indexed: true}, {Key: "issued", Label: "Issued", Type: "date", Indexed: true}, {Key: "owner", Label: "Owner", Type: "text", Indexed: true}, {Key: "memo", Label: "Memo", Type: "text"}} {
		fieldOK(t, s, l.ID, field)
	}
	f := smartOK(t, s, SmartFolderInput{Name: "Open", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Terms: []string{root.ID}, Filter: &FilterExpr{And: []FilterExpr{{Field: "status", Value: json.RawMessage(`"open"`)}, {Field: "owner", ValueRef: "me"}}}, GroupBy: []SmartFolderGroupBy{{Field: "issued", By: "year"}, {Field: "status"}}}})
	physical := create(t, s, l.ID, "folder", "Physical", nil)
	year, status := "2026", "open"
	item, created, err := s.ClassifySmartFolder(testContext, "alice", f.ID, SmartFolderDrop{FolderVersion: f.Version, CollectionID: l.ID, ParentID: physical.ID, Create: &BulkCreate{Name: "Invoice", Values: map[string]any{"issued": "2026-10-08", "memo": "keep"}}, Path: []*string{&year, &status}})
	if err != nil || !created || item.Values["owner"] != "alice" || *item.ParentID != physical.ID {
		t.Fatal(item, created, err)
	}
	if _, _, err = s.ClassifySmartFolder(testContext, "alice", f.ID, SmartFolderDrop{FolderVersion: f.Version + 1, CollectionID: l.ID, ItemID: item.ID, Version: item.Version}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	values := item.Values
	values["projects"] = []any{child.ID, other.ID}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.UnclassifySmartFolder(testContext, "alice", f.ID, item.ID, f.Version, item.Version)
	if err != nil || out.Name != "Invoice" || *out.ParentID != physical.ID || out.Values["memo"] != "keep" || out.Values["status"] != nil || out.Values["owner"] != nil {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(out.Values["projects"])
	if string(raw) != `["`+other.ID+`"]` {
		t.Fatal(string(raw))
	}
	rows, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
	if err != nil || rows.Total != 0 {
		t.Fatal(rows, err)
	}
	out, created, err = s.ClassifySmartFolder(testContext, "alice", f.ID, SmartFolderDrop{FolderVersion: f.Version, CollectionID: l.ID, ItemID: out.ID, Version: out.Version, Path: []*string{&year}})
	if err != nil || created || out.ID != item.ID {
		t.Fatal(out, created, err)
	}
}

func TestSmartFolderActionsRollBackAndRequireItemPermissions(t *testing.T) {
	s, w, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "due", Label: "Due", Type: "date", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "serial", Label: "Serial", Type: "text", Indexed: true, Options: model.FieldOptions{Unique: true}})
	original := create(t, s, l.ID, "item", "Original", map[string]any{"status": "closed", "serial": "reserved"})
	f := smartOK(t, s, SmartFolderInput{Name: "Open", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Filter: &FilterExpr{And: []FilterExpr{{Field: "status", Value: json.RawMessage(`"open"`)}, {Field: "due", Op: "gte", Value: json.RawMessage(`"2026-10-01"`)}}}}})
	before, _ := s.Client.ItemRevision.Query().Count(testContext)
	audits, _ := s.Client.AuditEvent.Query().Count(testContext)
	_, _, err := s.ClassifySmartFolder(testContext, "alice", f.ID, SmartFolderDrop{FolderVersion: f.Version, CollectionID: l.ID, ItemID: original.ID, Version: original.Version})
	if !isValidation(err) {
		t.Fatal(err)
	}
	retained, _ := s.Get(testContext, "alice", original.ID)
	after, _ := s.Client.ItemRevision.Query().Count(testContext)
	auditAfter, _ := s.Client.AuditEvent.Query().Count(testContext)
	if retained.Version != original.Version || retained.Values["status"] != "closed" || before != after || audits != auditAfter {
		t.Fatal("failed classification leaked writes", retained, before, after, audits, auditAfter)
	}
	_, _, err = s.ClassifySmartFolder(testContext, "alice", f.ID, SmartFolderDrop{FolderVersion: f.Version, CollectionID: l.ID, Create: &BulkCreate{Name: "Duplicate", Values: map[string]any{"serial": "reserved", "due": "2026-10-08"}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	current, _ := s.Client.Resource.Get(testContext, w.ID)
	_, err = s.SetPermissions(testContext, "alice", w.ID, current.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "reader", Action: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ClassifySmartFolder(testContext, "reader", f.ID, SmartFolderDrop{FolderVersion: f.Version, CollectionID: l.ID, ItemID: original.ID, Version: original.Version})
	if !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	rangeFolder := smartOK(t, s, SmartFolderInput{Name: "By range", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Filter: &FilterExpr{Field: "serial", Op: "present"}}})
	_, err = s.UnclassifySmartFolder(testContext, "alice", rangeFolder.ID, original.ID, rangeFolder.Version, original.Version)
	if !isValidation(err) {
		t.Fatal(err)
	}
	retained, _ = s.Get(testContext, "alice", original.ID)
	if retained.Version != original.Version {
		t.Fatal("unclassification did not roll back")
	}
	// Contradictory equalities and navigation cannot silently overwrite each other.
	conflict := smartOK(t, s, SmartFolderInput{Name: "Conflict", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Filter: &FilterExpr{And: []FilterExpr{{Field: "status", Value: json.RawMessage(`"open"`)}, {Field: "status", Value: json.RawMessage(`"closed"`)}}}}})
	_, _, err = s.ClassifySmartFolder(testContext, "alice", conflict.ID, SmartFolderDrop{FolderVersion: conflict.Version, CollectionID: l.ID, Create: &BulkCreate{Name: "Impossible"}})
	if !isValidation(err) {
		t.Fatal(err)
	}
	bad := smartOK(t, s, SmartFolderInput{Name: "Wrong name type", Personal: true, Definition: SmartFolderDefinition{Filter: &FilterExpr{Field: "$name", Value: json.RawMessage(`42`)}}})
	_, _, err = s.ClassifySmartFolder(testContext, "alice", bad.ID, SmartFolderDrop{FolderVersion: bad.Version, CollectionID: l.ID, Create: &BulkCreate{Name: "Safe"}})
	if !isValidation(err) {
		t.Fatal(err)
	}
}
