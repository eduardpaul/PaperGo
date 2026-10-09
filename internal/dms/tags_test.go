package dms

import (
	"strings"
	"testing"
)

func TestTagVocabularyFollowsAccessAndSurfaces(t *testing.T) {
	s, w, list := fixture(t)
	other := create(t, s, w.ID, "list", "Other", nil)
	if _, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage", Effect: "allow"}, {Subject: "rita", Action: "read", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	tagged := func(parent, kind, name string, tags ...string) {
		t.Helper()
		r, err := s.Create(testContext, "alice", parent, CreateResource{Kind: kind, Name: name, Tags: tags})
		if err != nil {
			t.Fatal(err)
		}
		if kind == "item" && name != "Draft" {
			if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
		}
	}
	tagged(list.ID, "item", "Invoice 1", "finance", "legal")
	tagged(list.ID, "item", "Invoice 2", "finance")
	tagged(list.ID, "item", "Draft", "secret")
	tagged(list.ID, "folder", "2026", "archive")
	tagged(other.ID, "item", "Memo", "elsewhere")

	vocab := func(subject string, q TagsQuery) string {
		t.Helper()
		page, err := s.Tags(testContext, subject, w.ID, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tc := range page.Data {
			out = append(out, tc.Tag+":"+string(rune('0'+tc.Count)))
		}
		return strings.Join(out, ",")
	}
	if got := vocab("alice", TagsQuery{}); got != "archive:1,elsewhere:1,finance:2,legal:1,secret:1" {
		t.Fatalf("manager vocabulary: %s", got)
	}
	// rita reads published content only, so the draft's tag is not hers to see.
	if got := vocab("rita", TagsQuery{}); got != "archive:1,elsewhere:1,finance:2,legal:1" {
		t.Fatalf("reader vocabulary: %s", got)
	}
	if got := vocab("alice", TagsQuery{CollectionID: list.ID, Prefix: "f"}); got != "finance:2" {
		t.Fatalf("prefix in collection: %s", got)
	}
	if got := vocab("alice", TagsQuery{Tag: "legal"}); got != "legal:1" {
		t.Fatalf("exact tag: %s", got)
	}
	page, err := s.Tags(testContext, "alice", w.ID, TagsQuery{Limit: 2})
	if err != nil || len(page.Data) != 2 || page.NextCursor != "elsewhere" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if got := vocab("alice", TagsQuery{After: page.NextCursor, Limit: 2}); got != "finance:2,legal:1" {
		t.Fatalf("second page: %s", got)
	}
	if _, err = s.Tags(testContext, "mallory", w.ID, TagsQuery{}); err == nil {
		t.Fatal("a stranger read the vocabulary")
	}
	if _, err = s.Tags(testContext, "alice", w.ID, TagsQuery{CollectionID: w.ID}); err == nil {
		t.Fatal("the workspace accepted as a collection")
	}
}
