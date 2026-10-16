package dms

import (
	"errors"
	"fmt"
	"testing"
)

func TestDecisionCandidatesHierarchySnapshots(t *testing.T) {
	s, _, _ := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	root := mustTerm(t, s, set.ID, TermInput{Name: "Finance", Description: "Finance documents"})
	child := mustTerm(t, s, set.ID, TermInput{Name: "Invoice", ParentID: &root.ID, Description: "Invoices"})
	other := mustTerm(t, s, set.ID, TermInput{Name: "Other", SortOrder: -1, Description: "Other topics"})
	snap, err := s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{}, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Candidates) != 3 || snap.Candidates[0].ID != other.ID {
		t.Fatal(snap)
	}
	if err = s.CheckTaxonomyGuard(testContext, "alice", snap.Guard); err != nil {
		t.Fatal(err)
	}
	leaves, err := s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{RootTermID: root.ID, LeafOnly: true}, nil, 50)
	if err != nil || len(leaves.Candidates) != 1 || leaves.Candidates[0].ID != child.ID {
		t.Fatal(leaves, err)
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{TermIDs: []string{root.ID}, LeafOnly: true}, nil, 50); err == nil {
		t.Fatal("accepted explicit non-leaf")
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{TermIDs: []string{child.ID}}, []DecisionOverride{{TermID: other.ID, Text: "x"}}, 50); err == nil {
		t.Fatal("accepted outside override")
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{TermIDs: []string{"missing"}}, nil, 50); err == nil {
		t.Fatal("accepted missing term")
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{}, []DecisionOverride{{child.ID, "Same"}, {other.ID, "Same"}}, 50); err == nil {
		t.Fatal("accepted duplicate text")
	}
	taxonomyOnlyRole(t, s, "writer", "workspace.read")
	if _, err = s.DecisionCandidates(testContext, "writer", set.ID, DecisionScope{}, nil, 50); !errors.Is(err, ErrForbidden) {
		t.Fatal("taxonomy scope", err)
	}
	_, err = s.UpdateTerm(testContext, "alice", root.ID, root.Version, TermInput{Name: root.Name, Description: root.Description, Deprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckTaxonomyGuard(testContext, "alice", snap.Guard); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale snapshot", err)
	}
	active, err := s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{}, nil, 50)
	if err != nil || len(active.Candidates) != 1 || active.Candidates[0].ID != other.ID {
		t.Fatal(active, err)
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{TermIDs: []string{child.ID}}, nil, 50); err == nil {
		t.Fatal("accepted descendant of deprecated term")
	}
}
func TestDecisionCandidatesCompleteMembership(t *testing.T) {
	s, _, _ := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 51; i++ {
		r := mustTerm(t, s, set.ID, TermInput{Name: fmt.Sprintf("Topic %02d", i)})
		ids = append(ids, r.ID)
	}
	if _, err = s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{}, nil, 50); err == nil {
		t.Fatal("truncated oversized set")
	}
	subset, err := s.DecisionCandidates(testContext, "alice", set.ID, DecisionScope{TermIDs: []string{ids[50], ids[0]}}, nil, 50)
	if err != nil || len(subset.Candidates) != 2 || subset.Candidates[0].ID != ids[0] {
		t.Fatal(subset, err)
	}
	scope := DecisionScope{RootTermID: ids[0]}
	old, err := s.DecisionCandidates(testContext, "alice", set.ID, scope, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	mustTerm(t, s, set.ID, TermInput{Name: "New descendant", ParentID: &ids[0]})
	if err = s.CheckTaxonomyGuard(testContext, "alice", old.Guard); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("creation did not invalidate", err)
	}
}

func TestTaxonomyGuardIgnoresPromptRendering(t *testing.T) {
	s, _, _ := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	first := mustTerm(t, s, set.ID, TermInput{Name: "Accounting", Description: "Shared description"})
	second := mustTerm(t, s, set.ID, TermInput{Name: "Finance", Description: "Shared description"})
	scope := DecisionScope{TermIDs: []string{second.ID, first.ID}}
	snapshot, err := s.DecisionCandidates(testContext, "alice", set.ID, scope, []DecisionOverride{{first.ID, "Accounting documents"}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckTaxonomyGuard(testContext, "alice", snapshot.Guard); err != nil {
		t.Fatal("guard incorrectly requires original overrides", err)
	}
	scope.TermIDs = []string{first.ID, second.ID}
	ordered, err := s.DecisionCandidates(testContext, "alice", set.ID, scope, []DecisionOverride{{first.ID, "Accounting documents"}}, 50)
	if err != nil || ordered.Guard.Fingerprint != snapshot.Guard.Fingerprint {
		t.Fatal("subset normalization", err)
	}
	if _, err = s.UpdateTerm(testContext, "alice", first.ID, first.Version, TermInput{Name: first.Name, Description: first.Description, Labels: map[string]string{"es": "Contabilidad"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckTaxonomyGuard(testContext, "alice", snapshot.Guard); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("relevant labels did not invalidate override snapshot", err)
	}
}
