package dms

import (
	"context"
	"errors"
	"papergo/ent/relationship"
	"papergo/ent/term"
	"papergo/internal/identity"
	"testing"
)

func TestRelationshipLabelsShareTaxonomyIdentity(t *testing.T) {
	s, w, l := fixture(t)
	ctx := context.Background()
	a := create(t, s, l.ID, "item", "A", nil)
	b := create(t, s, l.ID, "item", "B", nil)
	if e := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); e != nil {
		t.Fatal(e)
	}
	edge, e := s.Link(ctx, "bob", a.ID, CreateRelationship{Type: "Related", TargetID: b.ID})
	if e != nil || edge.Directed {
		t.Fatal(edge, e)
	}
	predicate, e := s.Term(ctx, "alice", edge.TypeID)
	if e != nil || predicate.Name != "Related" {
		t.Fatal(predicate, e)
	}
	predicate, e = s.UpdateTerm(ctx, "alice", predicate.ID, predicate.Version, TermInput{Name: "Associated", Labels: map[string]string{"fr": "Associé"}, Synonyms: []string{"Related", "linked"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, label := range []string{"linked", "ASSOCIÉ", predicate.ID, "Related"} {
		retry, e := s.Link(ctx, "bob", b.ID, CreateRelationship{Type: label, TargetID: a.ID})
		if e != nil || retry.ID != edge.ID || retry.TypeID != predicate.ID {
			t.Fatal(label, retry, e)
		}
	}
	types, e := s.RelationshipTypes(ctx, "bob", "", 50)
	if e != nil || len(types.Data) != 1 || types.Data[0].Term.Name != "Associated" {
		t.Fatal(types, e)
	}
	query, e := s.Relationships(ctx, "bob", a.ID, "outgoing", "Associé", "", 50)
	if e != nil || len(query.Data) != 1 {
		t.Fatal(query, e)
	}
	private, e := s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Private"})
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Create(ctx, "bob", private.ID, CreateResource{Kind: "list", Name: "Other"})
	if e != nil {
		t.Fatal(e)
	}
	x, e := s.Create(ctx, "bob", other.ID, CreateResource{Kind: "item", Name: "X"})
	if e != nil {
		t.Fatal(e)
	}
	y, e := s.Create(ctx, "bob", other.ID, CreateResource{Kind: "item", Name: "Y"})
	if e != nil {
		t.Fatal(e)
	}
	otherEdge, e := s.Link(ctx, "bob", x.ID, CreateRelationship{Type: "linked", TargetID: y.ID})
	if e != nil || otherEdge.TypeID != predicate.ID {
		t.Fatal(otherEdge, e)
	}
	count, e := s.Client.Term.Query().Where(term.TermSetIDEQ(predicate.TermSetID)).Count(ctx)
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if _, e = s.Link(ctx, "bob", a.ID, CreateRelationship{Type: predicate.ID, TargetID: b.ID, Directed: ptr(true)}); !isValidation(e) {
		t.Fatal("direction mismatch", e)
	}
	if _, e = s.CreateRelationshipType(ctx, "bob", RelationshipTypeInput{Name: "linked", Directed: true}); !isValidation(e) {
		t.Fatal("semantics changed", e)
	}
	predicate, e = s.UpdateTerm(ctx, "alice", predicate.ID, predicate.Version, TermInput{Name: predicate.Name, Deprecated: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Link(ctx, "bob", a.ID, CreateRelationship{Type: predicate.ID, TargetID: b.ID}); !isValidation(e) {
		t.Fatal("deprecated ID accepted", e)
	}
}

func TestRelationshipTermMergePreservesEdgesAndRejectsLimits(t *testing.T) {
	s, _, l := fixture(t)
	ctx := context.Background()
	a := create(t, s, l.ID, "item", "A", nil)
	b := create(t, s, l.ID, "item", "B", nil)
	first, e := s.Link(ctx, "alice", a.ID, CreateRelationship{Type: "Related", TargetID: b.ID})
	if e != nil {
		t.Fatal(e)
	}
	target, e := s.CreateRelationshipType(ctx, "alice", RelationshipTypeInput{Name: "Associated"})
	if e != nil {
		t.Fatal(e)
	}
	source, e := s.Term(ctx, "alice", first.TypeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MergeTerm(ctx, "alice", source.ID, source.Version, MergeTermInput{TargetTermID: target.ID}); e != nil {
		t.Fatal(e)
	}
	edge, e := s.Client.Relationship.Get(ctx, first.ID)
	if e != nil || edge.TypeID != target.ID || edge.Version != first.Version+1 {
		t.Fatal(edge, e)
	}
	retry, e := s.Link(ctx, "alice", b.ID, CreateRelationship{Type: source.ID, TargetID: a.ID})
	if e != nil || retry.ID != first.ID {
		t.Fatal(retry, e)
	}
	limited, e := s.CreateRelationshipType(ctx, "alice", RelationshipTypeInput{Name: "Owns", Directed: true, MaxOutgoing: ptr(1)})
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.CreateRelationshipType(ctx, "alice", RelationshipTypeInput{Name: "Possesses", Directed: true, MaxOutgoing: ptr(1)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Link(ctx, "alice", a.ID, CreateRelationship{Type: limited.ID, TargetID: b.ID}); e != nil {
		t.Fatal(e)
	}
	tm, e := s.Term(ctx, "alice", limited.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MergeTerm(ctx, "alice", tm.ID, tm.Version, MergeTermInput{TargetTermID: other.ID}); !isValidation(e) {
		t.Fatal("used limit merged", e)
	}
	tm, e = s.Term(ctx, "alice", tm.ID)
	if e != nil || tm.Deprecated {
		t.Fatal("failed merge leaked", tm, e)
	}
	n, e := s.Client.Relationship.Query().Where(relationship.TypeIDEQ(limited.ID)).Count(ctx)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	// A taxonomy term from another vocabulary is never a graph predicate.
	kw, _, e := s.AddKeyword(ctx, "alice", KeywordInput{Name: "Unrelated"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Link(ctx, "alice", a.ID, CreateRelationship{Type: kw.ID, TargetID: b.ID}); !isValidation(e) {
		t.Fatal("foreign vocabulary accepted", e)
	}
	if e = s.Directory().SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "visitor"}, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateRelationshipType(ctx, "visitor", RelationshipTypeInput{Name: "Denied"}); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
}

func TestRelationshipAmbiguousLabelsAndDuplicateMerge(t *testing.T) {
	s, _, l := fixture(t)
	ctx := context.Background()
	a := create(t, s, l.ID, "item", "A", nil)
	b := create(t, s, l.ID, "item", "B", nil)
	first, e := s.CreateRelationshipType(ctx, "alice", RelationshipTypeInput{Name: "First"})
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.CreateRelationshipType(ctx, "alice", RelationshipTypeInput{Name: "Second"})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{first.ID, second.ID} {
		tm, e := s.Term(ctx, "alice", id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.UpdateTerm(ctx, "alice", id, tm.Version, TermInput{Name: tm.Name, Synonyms: []string{"alias"}}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.Link(ctx, "alice", a.ID, CreateRelationship{Type: "alias", TargetID: b.ID}); !isValidation(e) {
		t.Fatal("ambiguous label accepted", e)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, e = s.Link(ctx, "alice", a.ID, CreateRelationship{Type: id, TargetID: b.ID}); e != nil {
			t.Fatal(e)
		}
	}
	tm, e := s.Term(ctx, "alice", first.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.MergeTerm(ctx, "alice", first.ID, tm.Version, MergeTermInput{TargetTermID: second.ID}); e != nil {
		t.Fatal(e)
	}
	edges, e := s.Relationships(ctx, "alice", a.ID, "outgoing", "alias", "", 50)
	if e != nil || len(edges.Data) != 1 || edges.Data[0].TypeID != second.ID {
		t.Fatal(edges, e)
	}
}
