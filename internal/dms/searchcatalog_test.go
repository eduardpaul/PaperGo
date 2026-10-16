package dms

import "testing"

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
