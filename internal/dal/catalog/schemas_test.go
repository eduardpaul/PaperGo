package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/daltest"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
)

func TestCatalogSchemas_SQLite(t *testing.T)   { testCatalogSchemas(t, daltest.SQLiteStore(t)) }
func TestCatalogSchemas_Postgres(t *testing.T) { testCatalogSchemas(t, daltest.PostgresStore(t)) }

func testCatalogSchemas(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Catalog()
	second, err := r.RecordSchema(ctx, f.Source, "bob", json.RawMessage(`{"content_types": []}`))
	if err != nil || second.RevisionNumber != 2 {
		t.Fatalf("next revision: %+v %v", second, err)
	}
	head, err := s.Resources().Resource(ctx, f.Source)
	if err != nil || daltest.Val(head.SchemaHeadID) != second.ID {
		t.Fatalf("schema head: %v %v", head.SchemaHeadID, err)
	}
	if _, err = r.RecordSchema(ctx, f.Folder, "bob", json.RawMessage(`{}`)); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("schema for a folder: %v", err)
	}
	later, err := r.Schemas(ctx, catalog.SchemaFilter{ContainerID: f.Source, AfterNumber: 1})
	if err != nil || len(later) != 1 || later[0].ID != second.ID {
		t.Fatalf("later revisions: %+v %v", later, err)
	}
	found, err := r.Schemas(ctx, catalog.SchemaFilter{IDs: []string{f.Schema, second.ID}})
	if err != nil || len(found) != 2 || found[0].ID != f.Schema {
		t.Fatalf("revisions by id: %+v %v", found, err)
	}
	if none, err := r.Schemas(ctx, catalog.SchemaFilter{IDs: []string{}}); err != nil || len(none) != 0 {
		t.Fatalf("empty selection: %+v %v", none, err)
	}
	if stored, err := r.Schema(ctx, second.ID); err != nil || stored.CreatedBy != "bob" || string(stored.Definition) == "" {
		t.Fatalf("revision: %+v %v", stored, err)
	}

	tpl, err := r.CreateTemplate(ctx, catalog.Template{WorkspaceID: f.Workspace, Key: "invoice", Name: "Invoice", Definition: json.RawMessage(`{"fields": []}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateTemplate(ctx, catalog.Template{WorkspaceID: f.Workspace, Key: "invoice", Name: "Again", Definition: json.RawMessage(`{}`)}); !dberr.IsConstraintError(err) {
		t.Fatalf("duplicate template key: %v", err)
	}
	saved, err := r.SaveTemplate(ctx, catalog.Template{ID: tpl.ID, Name: "Invoices", Definition: json.RawMessage(`{"fields": [1]}`)}, tpl.Version)
	if err != nil || saved.Version != 2 || saved.Key != "invoice" {
		t.Fatalf("save template: %+v %v", saved, err)
	}
	if _, err = r.SaveTemplate(ctx, *saved, tpl.Version); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale template save: %v", err)
	}
	listed, err := r.Templates(ctx, catalog.TemplateFilter{WorkspaceID: f.Workspace})
	if err != nil || len(listed) != 1 || listed[0].Name != "Invoices" {
		t.Fatalf("templates: %+v %v", listed, err)
	}
}
