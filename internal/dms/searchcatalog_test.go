package dms

import (
	"papergo/internal/model"
	"testing"
)

func TestSearchContentTypesRespectReadableCollections(t *testing.T) {
	s, workspace, list := fixture(t)
	readers(t, s, workspace)
	visible := typeOK(t, s, list.ID, ContentTypeInput{Key: "invoice", Name: "Invoice"})
	private := create(t, s, workspace.ID, "list", "Private", nil)
	hidden := typeOK(t, s, private.ID, ContentTypeInput{Key: "secret", Name: "Secret"})
	if _, err := s.SetPermissions(testContext, "alice", private.ID, latest(t, s, private.ID).Version, Permissions{Grants: []Permission{{PrincipalType: "user", PrincipalID: "alice", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"", list.ID, private.ID} {
		types, err := s.SearchContentTypes(testContext, "reader", workspace.ID, collection)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, typ := range types {
			if typ.ID == hidden.ID {
				t.Fatal("private type leaked", typ)
			}
			found = found || typ.ID == visible.ID
		}
		if found != (collection != private.ID) {
			t.Fatal("wrong scoped catalog", collection, types)
		}
	}
}

func TestSearchFieldsPreserveRuleEditorReferenceScopesAndChoices(t *testing.T) {
	s, workspace, list := fixture(t)
	second := create(t, s, workspace.ID, "list", "Other", nil)
	for _, container := range []string{list.ID, second.ID} {
		fieldOK(t, s, container, CreateField{Key: "reference", Label: "Reference", Type: "lookup", Indexed: true, Options: model.FieldOptions{LookupContainerID: container}})
		fieldOK(t, s, container, CreateField{Key: "status", Label: "Status", Type: "choice", Indexed: true, Choices: []string{container}})
	}
	fields, err := s.SearchFields(testContext, "alice", workspace.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]bool{}
	choices := map[string]bool{}
	for _, field := range fields {
		if field.Key == "reference" {
			scopes[field.LookupContainerID] = true
		}
		if field.Key == "status" {
			for _, choice := range field.Choices {
				choices[choice] = true
			}
		}
	}
	if !scopes[list.ID] || !scopes[second.ID] || !choices[list.ID] || !choices[second.ID] {
		t.Fatalf("incomplete field metadata: %+v", fields)
	}
}
