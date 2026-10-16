package dms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"papergo/ent"
	"papergo/ent/predicate"
	"papergo/ent/term"
)

// DecisionScope is normalized independently of prompts and description overrides.
type DecisionScope struct {
	RootTermID string   `json:"root_term_id,omitempty"`
	TermIDs    []string `json:"term_ids,omitempty"`
	LeafOnly   bool     `json:"leaf_only"`
}
type TaxonomyGuard struct {
	TermSetID      string        `json:"term_set_id"`
	SelectionScope DecisionScope `json:"selection_scope"`
	Fingerprint    string        `json:"fingerprint"`
}
type DecisionOverride struct {
	TermID string `json:"term_id"`
	Text   string `json:"text"`
}
type DecisionCandidate struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Text string `json:"text"`
}
type DecisionSnapshot struct {
	Candidates []DecisionCandidate `json:"candidates"`
	Guard      TaxonomyGuard       `json:"guard"`
}

// DecisionCandidates resolves complete set membership in a bounded query, never a
// paginated UI listing. The transaction holds one taxonomy snapshot throughout.
func (s *Service) DecisionCandidates(ctx context.Context, subject, setID string, scope DecisionScope, overrides []DecisionOverride, limit int) (DecisionSnapshot, error) {
	return read(ctx, s, func(t *Service) (DecisionSnapshot, error) {
		return t.decisionCandidates(ctx, subject, setID, scope, overrides, limit, true)
	})
}

func (s *Service) decisionCandidates(ctx context.Context, subject, setID string, scope DecisionScope, overrides []DecisionOverride, limit int, render bool) (DecisionSnapshot, error) {
	var out DecisionSnapshot
	if err := s.requireTaxonomy(ctx, subject, "read"); err != nil {
		return out, err
	}
	if limit < 1 || limit > 50 {
		return out, invalid("decision candidate limit must be 1–50")
	}
	if scope.TermIDs != nil && len(scope.TermIDs) == 0 {
		return out, invalid("term_ids must be a nonempty explicit subset")
	}
	if scope.RootTermID != "" && len(scope.TermIDs) > 0 {
		return out, invalid("root_term_id and term_ids are mutually exclusive")
	}
	if len(scope.TermIDs) > limit || len(overrides) > limit {
		return out, invalid("explicit taxonomy selections exceed candidate limit")
	}
	set, err := s.Client.TermSet.Get(ctx, setID)
	if ent.IsNotFound(err) {
		return out, invalid("term_set_id must identify an organization term set")
	}
	if err != nil {
		return out, err
	}

	query := s.Client.Term.Query().Where(term.TermSetIDEQ(setID))
	ancestorIDs := []string{}
	if scope.RootTermID != "" || len(scope.TermIDs) > 0 {
		ids := scope.TermIDs
		if scope.RootTermID != "" {
			ids = []string{scope.RootTermID}
		}
		explicit, err := s.Client.Term.Query().Where(term.TermSetIDEQ(setID), term.IDIn(ids...)).All(ctx)
		if err != nil {
			return out, err
		}
		if len(explicit) != len(ids) {
			return out, invalid("explicit terms must exist in the selected set and be unique")
		}
		for _, r := range explicit {
			ancestorIDs = append(ancestorIDs, strings.Split(strings.Trim(r.Path, "/"), "/")...)
		}
		if scope.RootTermID != "" {
			query.Where(term.Or(term.PathHasPrefix(explicit[0].Path), term.IDIn(ancestorIDs...)))
		} else {
			conditions := []predicate.Term{term.IDIn(ancestorIDs...)}
			if scope.LeafOnly {
				conditions = append(conditions, term.And(term.ParentIDIn(ids...), term.DeprecatedEQ(false), term.MergedIntoIDIsNil()))
			}
			query.Where(term.Or(conditions...))
		}
	}
	// Bound the complete scope; explicit subsets read their ancestors and, for
	// leaf checks, active children instead of scanning unrelated set membership.
	rows, err := query.Order(ent.Asc(term.FieldSortOrder, term.FieldNormalizedName, term.FieldID)).Limit(10001).All(ctx)
	if err != nil {
		return out, err
	}
	if len(rows) > 10000 {
		return out, invalid("decision scope exceeds the 10000-term snapshot budget; choose a subset or smaller subtree")
	}

	byID := map[string]*ent.Term{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	ancestors := func(r *ent.Term) ([]*ent.Term, error) {
		chain := []*ent.Term{}
		seen := map[string]bool{}
		for r != nil {
			if seen[r.ID] {
				return nil, invalid("taxonomy contains a cycle")
			}
			seen[r.ID] = true
			chain = append(chain, r)
			if r.ParentID == nil {
				break
			}
			r = byID[*r.ParentID]
			if r == nil {
				return nil, invalid("taxonomy contains a missing ancestor")
			}
		}
		slices.Reverse(chain)
		return chain, nil
	}
	eligible := map[string]bool{}
	paths := map[string]string{}
	chains := map[string][]*ent.Term{}
	for _, r := range rows {
		chain, err := ancestors(r)
		if err != nil {
			return out, err
		}
		chains[r.ID] = chain
		active := true
		names := []string{}
		for _, a := range chain {
			names = append(names, a.Name)
			if a.Deprecated || a.MergedIntoID != nil {
				active = false
			}
		}
		eligible[r.ID] = active
		paths[r.ID] = strings.Join(names, " / ")
	}
	check := func(id string) error {
		if byID[id] == nil {
			return invalid("term " + id + " is missing or belongs to another set")
		}
		if !eligible[id] {
			return invalid("term " + id + " has an inactive or merged branch")
		}
		return nil
	}
	if scope.RootTermID != "" {
		if err = check(scope.RootTermID); err != nil {
			return out, err
		}
	}
	subset := map[string]bool{}
	for _, id := range scope.TermIDs {
		if subset[id] {
			return out, invalid("duplicate term_id " + id)
		}
		if err = check(id); err != nil {
			return out, err
		}
		subset[id] = true
	}
	scope.TermIDs = slices.Clone(scope.TermIDs)
	sort.Strings(scope.TermIDs)
	universe := map[string]bool{}
	for _, r := range rows {
		if eligible[r.ID] && (scope.RootTermID == "" || strings.Contains(r.Path, "/"+scope.RootTermID+"/")) {
			universe[r.ID] = true
		}
	}
	hasChildren := map[string]bool{}
	for id := range universe {
		for _, a := range chains[id] {
			if a.ID != id {
				hasChildren[a.ID] = true
			}
		}
	}
	candidates := []*ent.Term{}
	relevant := map[string]*ent.Term{}
	for _, r := range rows {
		if !universe[r.ID] || (len(subset) > 0 && !subset[r.ID]) {
			continue
		}
		if scope.LeafOnly && hasChildren[r.ID] {
			if subset[r.ID] {
				return out, invalid("explicit term " + r.ID + " is not a leaf")
			}
			continue
		}
		candidates = append(candidates, r)
		for _, a := range chains[r.ID] {
			relevant[a.ID] = a
		}
	}
	if len(candidates) == 0 || len(candidates) > limit {
		return out, invalid(fmt.Sprintf("taxonomy needs 1–%d candidates; got %d; choose a smaller subset or subtree", limit, len(candidates)))
	}
	override := map[string]string{}
	selected := map[string]bool{}
	for _, r := range candidates {
		selected[r.ID] = true
	}
	for _, o := range overrides {
		if err = check(o.TermID); err != nil {
			return out, err
		}
		if !selected[o.TermID] {
			return out, invalid("override term " + o.TermID + " is not a candidate")
		}
		if _, ok := override[o.TermID]; ok {
			return out, invalid("duplicate override for " + o.TermID)
		}
		if strings.TrimSpace(o.Text) == "" {
			return out, invalid("empty override for " + o.TermID)
		}
		override[o.TermID] = o.Text
	}
	out.Candidates = make([]DecisionCandidate, 0, len(candidates))
	texts := map[string]string{}
	for _, r := range candidates {
		text, ok := override[r.ID]
		if !ok {
			text = r.Description
			if text == "" {
				text = paths[r.ID]
			}
		}
		if id, ok := texts[text]; ok && render {
			return out, invalid("duplicate option texts for terms " + id + " and " + r.ID)
		}
		texts[text] = r.ID
		out.Candidates = append(out.Candidates, DecisionCandidate{ID: r.ID, Path: paths[r.ID], Text: text})
	}
	// Candidate order and complete versioned ancestry capture membership, ordering,
	// eligibility and conservative invalidation on label/description changes.
	orderedAncestors := []*ent.Term{}
	for _, r := range rows {
		if relevant[r.ID] != nil {
			orderedAncestors = append(orderedAncestors, r)
		}
	}
	candidateIDs := []string{}
	for _, r := range candidates {
		candidateIDs = append(candidateIDs, r.ID)
	}
	raw, err := json.Marshal(struct {
		Set        *ent.TermSet
		Scope      DecisionScope
		Candidates []string
		Ancestry   []*ent.Term
	}{set, scope, candidateIDs, orderedAncestors})
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(raw)
	out.Guard = TaxonomyGuard{setID, scope, hex.EncodeToString(digest[:])}
	return out, nil
}

func (s *Service) CheckTaxonomyGuard(ctx context.Context, subject string, g TaxonomyGuard) error {
	if g.TermSetID == "" || g.Fingerprint == "" {
		return invalid("expected_taxonomy needs a term_set_id and fingerprint")
	}
	snap, err := read(ctx, s, func(t *Service) (DecisionSnapshot, error) {
		return t.decisionCandidates(ctx, subject, g.TermSetID, g.SelectionScope, nil, 50, false)
	})
	if err != nil {
		return err
	}
	if snap.Guard.Fingerprint != g.Fingerprint {
		return ErrPreconditionFailed
	}
	return nil
}

// CheckItemGuards runs in the caller's mutation/preparation transaction.
// Preconditions never grant access: the corresponding read/write still authorizes.
func (s *Service) CheckItemGuards(ctx context.Context, subject, itemID string, version *int, blobID string, taxonomy *TaxonomyGuard) error {
	r, err := s.Authorize(ctx, subject, itemID, "read")
	if err != nil {
		return err
	}
	if version != nil && r.Version != *version {
		return ErrPreconditionFailed
	}
	if blobID != "" {
		b, err := s.CurrentFile(ctx, subject, itemID)
		if err != nil {
			return err
		}
		if b.ID != blobID {
			return ErrPreconditionFailed
		}
	}
	if taxonomy != nil {
		return s.CheckTaxonomyGuard(ctx, subject, *taxonomy)
	}
	return nil
}
