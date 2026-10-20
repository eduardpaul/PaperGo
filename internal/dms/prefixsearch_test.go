package dms

import (
	"context"
	"testing"
)

// Without a text matcher, search text matches the names of entries on the
// surface read, ignoring case, anywhere in the name.
func TestSearchTextMatchesNamesWithoutAMatcher(t *testing.T) {
	s, w, c := fixture(t)
	acme := create(t, s, c.ID, "item", "Invoice INV-0000123 Acme Travel", nil)
	create(t, s, c.ID, "item", "Invoice INV-0000456 Globex Legal", nil)
	for search, want := range map[string]int{
		"acm":         1,
		"Acme tra":    1,
		"invoice":     2,
		"INV-0000":    2,
		"cme":         1,
		"travel acme": 0,
		"---":         0,
	} {
		q, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Search: search}})
		if err != nil || len(q.Data) != want {
			t.Fatal("query search", search, len(q.Data), err)
		}
		page, err := s.Browse(testContext, "alice", Browse{ParentID: c.ID, Search: search})
		if err != nil || len(page.Data) != want {
			t.Fatal("browse search", search, len(page.Data), err)
		}
		if want == 1 && q.Data[0].ID != acme.ID {
			t.Fatal("wrong match", search)
		}
	}
	// Across a workspace, collections match by name too.
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: c.Name})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != c.ID {
		t.Fatal("collection by name", page, err)
	}
}

// matchIDs is a text matcher that matches the IDs it holds, or declines.
type matchIDs struct {
	ids     []string
	decline bool
}

func (m matchIDs) MatchText(context.Context, *Service, string, TextMatch) (TextMatches, error) {
	if m.decline {
		return TextMatches{}, ErrNoTextMatch
	}
	return TextMatches{Head: m.ids, Published: m.ids, Capped: true}, nil
}

// A text matcher decides which items and folders match; where it declines,
// names match.
func TestTextMatcherDecidesMatches(t *testing.T) {
	s, _, c := fixture(t)
	acme := create(t, s, c.ID, "item", "Acme", nil)
	globex := create(t, s, c.ID, "item", "Globex", nil)
	s.SetTextMatcher(matchIDs{ids: []string{globex.ID}})
	q, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Search: "acme"}})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != globex.ID || !q.SearchCapped {
		t.Fatal("matcher", q, err)
	}
	s.SetTextMatcher(matchIDs{decline: true})
	q, err = s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Search: "acme"}})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != acme.ID || q.SearchCapped {
		t.Fatal("declined", q, err)
	}
}
