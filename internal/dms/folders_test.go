package dms

import (
	"encoding/json"
	"errors"
	"testing"

	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/publication"
	"papergo/internal/model"
)

func TestFolderFieldsSearchAndLiveReaderVisibility(t *testing.T) {
	s, w, c := fixture(t)
	fieldOK(t, s, c.ID, CreateField{Key: "required", Label: "Required", Type: "text", Required: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"item default"`)}})
	fieldOK(t, s, c.ID, CreateField{Key: "notes", Label: "Notes", Type: "text", Indexed: true})
	fieldOK(t, s, c.ID, CreateField{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}})
	group, err := s.CreateTermGroup(testContext, "alice", TermGroupInput{Name: "Classification"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, c.ID, CreateField{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}})
	f := create(t, s, c.ID, "folder", "Folder", map[string]any{"notes": "archival", "keywords": []any{"Falcon"}, "topic": "Research"})
	if f.ContentTypeID == nil || f.Values["required"] != nil || f.Values["topic"] == "Research" {
		t.Fatal("folder fields/defaults", f)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(testContext, "reader", f.ID)
	if err != nil || got.Values["notes"] != "archival" {
		t.Fatal("folder must be live in publishing collection", got, err)
	}
	for _, text := range []string{"archival", "Falcon", "Research"} {
		page, e := s.Browse(testContext, "reader", Browse{WorkspaceID: w.ID, Search: text})
		if e != nil || len(page.Data) != 1 || page.Data[0].ID != f.ID {
			t.Fatal(text, page, e)
		}
	}
	q, err := s.Query(testContext, "reader", c.ID, QueryRequest{IncludeTotal: true, Query: QuerySpec{Filter: &FilterExpr{Field: "notes", Value: json.RawMessage(`"archival"`)}}})
	if err != nil || counted(q.Total) != 1 || q.Data[0].ID != f.ID {
		t.Fatal("typed folder query", q, err)
	}
	selectFields := []string{"notes"}
	q, err = s.Query(testContext, "reader", c.ID, QueryRequest{Select: selectFields, Query: QuerySpec{Search: "Research"}})
	if err != nil || len(q.Data) != 1 || len(q.Data[0].Values) != 1 {
		t.Fatal("folder selection", q, err)
	}
	smart := smartOK(t, s, SmartFolderInput{Name: "Research folders", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{IncludeFolders: true, Terms: []string{f.Values["topic"].(string)}}})
	page, err := s.QuerySmartFolder(testContext, "reader", smart.ID, SmartFolderQueryRequest{IncludeTotal: true})
	if err != nil || counted(page.Total) != 1 || page.Data[0].Item.ID != f.ID {
		t.Fatal("taxonomy smart folders", page, err)
	}
	patch := map[string]any{"notes": "current", "keywords": nil}
	f, err = s.Update(testContext, "alice", f.ID, f.Version, UpdateResource{Values: &patch})
	if err != nil || f.Values["keywords"] != nil || f.Values["required"] != nil {
		t.Fatal(f, err)
	}
	got, err = s.Get(testContext, "reader", f.ID)
	if err != nil || got.Values["notes"] != "current" {
		t.Fatal(got, err)
	}
	if _, err = s.Publish(testContext, "alice", f.ID, f.Version); !isValidation(err) {
		t.Fatal("folder publication accepted", err)
	}
	if _, err = s.RevisionHistory(testContext, "reader", f.ID, 0, 10); !isValidation(err) {
		t.Fatal("folder history exposed", err)
	}
	count, err := s.Client.Publication.Query().Where(publication.ItemIDEQ(f.ID)).Count(testContext)
	if err != nil || count != 0 {
		t.Fatal("folder publication rows", count, err)
	}
	if _, err = s.ItemSchema(testContext, "reader", f.ID, "auto"); err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{"topic": "unrecognized", "notes": false}
	before, _ := s.Client.Term.Query().Count(testContext)
	if _, err = s.Update(testContext, "alice", f.ID, f.Version, UpdateResource{Values: &bad}); !isValidation(err) {
		t.Fatal("invalid folder metadata accepted", err)
	}
	after, _ := s.Client.Term.Query().Count(testContext)
	if before != after || latest(t, s, f.ID).Version != f.Version {
		t.Fatal("invalid metadata leaked writes")
	}
	if _, err = s.Update(testContext, "reader", f.ID, f.Version, UpdateResource{Values: &patch}); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader changed metadata", err)
	}
}

func TestFolderReclassificationSchemaChangesAndRecycle(t *testing.T) {
	s, _, c := fixture(t)
	old, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "old", Label: "Old", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	required, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "new", Label: "New", Type: "text", Required: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"default"`)}})
	if err != nil {
		t.Fatal(err)
	}
	a := typeOK(t, s, c.ID, ContentTypeInput{Key: "a", Name: "A", FieldIds: []string{old.ID}}, true)
	f := create(t, s, c.ID, "folder", "Typed folder", map[string]any{"old": "archive"})
	b := typeOK(t, s, c.ID, ContentTypeInput{Key: "b", Name: "B", FieldIds: []string{required.ID}})
	// Tightening the shared type must not force required values onto folders.
	a, err = s.UpdateContentType(testContext, "alice", a.ID, a.Version, ContentTypeInput{Name: a.Name, FieldIds: []string{old.ID, required.ID}})
	if err != nil {
		t.Fatal("folder blocked schema adoption", err)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, a.ID, latest(t, s, c.ID).Version); !errors.Is(err, ErrConflict) {
		t.Fatal("folder's live type detached", err)
	}
	f, err = s.Update(testContext, "alice", f.ID, f.Version, UpdateResource{ContentTypeID: &b.ID})
	if err != nil || len(f.Values) != 0 || *f.ContentTypeID != b.ID {
		t.Fatal("folder reclassification", f, err)
	}
	values := map[string]any{"new": "retained"}
	f, err = s.Update(testContext, "alice", f.ID, f.Version, UpdateResource{Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(testContext, "alice", f.ID, f.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	count, _ := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(f.ID)).Count(testContext)
	if count != 0 {
		t.Fatal("deleted folder has projections", count)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, b.ID, latest(t, s, c.ID).Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", c.ID, f.ID, false); !isValidation(err) {
		t.Fatal("restored detached folder type", err)
	}
	if _, err = s.AttachContentType(testContext, "alice", c.ID, latest(t, s, c.ID).Version, AttachContentTypeInput{ContentTypeID: b.ID}); err != nil {
		t.Fatal(err)
	}
	f, err = s.RestoreDeleted(testContext, "alice", c.ID, f.ID, false)
	if err != nil || f.Values["new"] != "retained" {
		t.Fatal(f, err)
	}
	q, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{ContentTypeID: b.ID}})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != f.ID {
		t.Fatal("restored folder query", q, err)
	}
	if err = s.Delete(testContext, "alice", f.ID, f.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(testContext, "alice", c.ID, f.ID, false); err != nil {
		t.Fatal(err)
	}
	count, err = s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(f.ID)).Count(testContext)
	if err != nil || count != 0 {
		t.Fatal("purged folder snapshots remain", count, err)
	}
}
