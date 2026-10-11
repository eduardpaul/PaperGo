package dms

import (
	"encoding/json"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/internal/model"
	"testing"
)

func TestMultipleFieldTypeMatrix(t *testing.T) {
	supported := map[string]bool{"choice": true, "person": true, "lookup": true, "term": true, "keywords": true}
	for _, typ := range FieldTypes() {
		t.Run(typ, func(t *testing.T) {
			d := &ent.FieldDefinition{Key: "values", Label: "Values", Type: fielddefinition.Type(typ), Indexed: true, Options: model.FieldOptions{Multiple: true}}
			switch typ {
			case "choice":
				d.Choices = []string{"one", "two"}
			case "lookup":
				d.Options.LookupContainerID = "collection"
			case "term":
				d.Options.TermSetID = "terms"
			case "currency":
				d.Options.CurrencyCode = "USD"
			}
			err := validateFieldDefinition(d)
			if supported[typ] != (err == nil) {
				t.Fatal(typ, err)
			}
		})
	}
}

func TestMultipleScalarDefinitionsRejectedEveryProvisioningPath(t *testing.T) {
	s, w, c := fixture(t)
	for _, typ := range []string{"text", "email", "url"} {
		in := CreateField{Key: typ, Label: typ, Type: typ, Options: model.FieldOptions{Multiple: true}}
		version := latest(t, s, c.ID).Version
		if _, err := s.CreateField(testContext, "alice", c.ID, in); err == nil {
			t.Fatal("collection field accepted", typ)
		}
		if _, err := s.CreateCatalogField(testContext, "alice", in); err == nil {
			t.Fatal("catalog field accepted", typ)
		}
		if _, err := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: typ, Name: typ, Fields: []CreateField{in}}); err == nil {
			t.Fatal("schema template accepted", typ)
		}
		if err := s.RegisterCollectionTemplate(CollectionTemplate{Key: typ, Name: typ, Kind: "list", ContentTypes: []TemplateInput{{Key: typ, Name: typ, Fields: []CreateField{in}}}}); err == nil {
			t.Fatal("collection template accepted", typ)
		}
		if latest(t, s, c.ID).Version != version {
			t.Fatal("rejected definition changed collection")
		}
	}
}

func TestMultiplePersonValuesDefaultsQueriesAndInactiveAssignments(t *testing.T) {
	s, _, c := fixture(t)
	for _, id := range []string{"assignee", "reviewer"} {
		if err := s.Directory().CreateUser(testContext, "alice", id); err != nil {
			t.Fatal(err)
		}
	}
	owner := fieldOK(t, s, c.ID, CreateField{Key: "owner", Label: "Owner", Type: "person", Indexed: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"assignee"`)}})
	fieldOK(t, s, c.ID, CreateField{Key: "reviewers", Label: "Reviewers", Type: "person", Indexed: true, Options: model.FieldOptions{Multiple: true}})
	item := create(t, s, c.ID, "item", "Record", map[string]any{"reviewers": []any{"reviewer", "assignee", "reviewer"}})
	if item.Values["owner"] != "assignee" || len(item.Values["reviewers"].([]any)) != 2 {
		t.Fatal(item.Values)
	}
	// Organization people need not belong to the item's workspace.
	rows, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "reviewers", Op: "eq", Value: json.RawMessage(`"reviewer"`)}}})
	if err != nil || len(rows.Data) != 1 || rows.Data[0].ID != item.ID {
		t.Fatal(rows, err)
	}
	if _, err = s.Directory().SetUserActive(testContext, "alice", "assignee", false); err != nil {
		t.Fatal(err)
	}
	people, err := s.People(testContext, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range people {
		if id == "assignee" {
			t.Fatal("inactive person offered")
		}
	}
	if _, err = s.Create(testContext, "alice", c.ID, CreateResource{Kind: "item", Name: "Invalid default"}); err == nil {
		t.Fatal("inactive default accepted")
	}
	if _, err = s.CreateCatalogField(testContext, "alice", CreateField{Key: "inactive_default", Label: "Inactive default", Type: "person", Options: model.FieldOptions{DefaultValue: json.RawMessage(`"assignee"`)}}); err == nil {
		t.Fatal("inactive field default accepted")
	}
	for _, values := range []map[string]any{
		{"owner": "assignee"}, {"owner": "missing"}, {"reviewers": []any{"reviewer", "assignee"}}, {"owner": []any{"reviewer"}}, {"reviewers": "reviewer"}, {"reviewers": []any{nil}},
	} {
		if _, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &values}); err == nil {
			t.Fatal("invalid person assignment accepted", values)
		}
		if latest(t, s, item.ID).Version != item.Version {
			t.Fatal("failed assignment committed")
		}
	}
	// Omitted people survive unrelated writes; explicit reassignment revalidates.
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Renamed")})
	if err != nil || item.Values["owner"] != "assignee" {
		t.Fatal(item, err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version); err == nil {
		t.Fatal("restored inactive assignment")
	}
	if latest(t, s, item.ID).Version != item.Version {
		t.Fatal("failed restore committed")
	}
	rows, err = s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "owner", Op: "eq", Value: json.RawMessage(`"assignee"`)}}})
	if err != nil || len(rows.Data) != 1 {
		t.Fatal("retained inactive assignment became unqueryable", rows, err)
	}
	if _, err = s.UpdateField(testContext, "alice", c.ID, owner.ID, latest(t, s, c.ID).Version, UpdateField{Options: &model.FieldOptions{Multiple: true}}); err == nil {
		t.Fatal("multiplicity changed")
	}
	if _, err = s.Directory().SetUserActive(testContext, "alice", "assignee", true); err != nil {
		t.Fatal(err)
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"owner": "assignee"}})
	if err != nil {
		t.Fatal(err)
	}
}
