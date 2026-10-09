package dms

import (
	"context"
	"encoding/json"
	"maps"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/itemrevision"
	"papergo/ent/term"
	"papergo/ent/termset"
	"slices"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
)

// Term and keywords fields store term IDs. Writers may give a term's ID or
// its label instead:
//
//   - A term field (one term set) matches a label against the names, then the
//     localized labels, then the synonyms of the set's active terms; a label
//     that matches several terms is ambiguous. An open set gets a new root
//     term for an unknown label; a closed set rejects it.
//   - A keywords field takes keywords: terms of the workspace's keywords set
//     and terms available as keywords. A label finds the keyword with that
//     name or synonym, or adds it to the keywords set.
//
// An ID of a merged term stands for the term it was merged into. Deprecated
// terms are rejected unless the value was already there.

func (s *Service) resolveTermValues(ctx context.Context, workspaceID string, d *ent.FieldDefinition, values []any, old map[string]bool) ([]any, error) {
	var set *ent.TermSet
	var e error
	if d.Type == "keywords" {
		set, e = s.keywordsSet(ctx, workspaceID)
	} else {
		set, e = s.Client.TermSet.Get(ctx, d.Options.TermSetID)
	}
	if e != nil {
		return nil, e
	}
	out := make([]any, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		raw := v.(string)
		id, e := s.resolveTermID(ctx, workspaceID, d, set, raw, old[raw])
		if e != nil {
			return nil, e
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}
func (s *Service) resolveTermID(ctx context.Context, workspaceID string, d *ent.FieldDefinition, set *ent.TermSet, raw string, kept bool) (string, error) {
	t, e := s.Client.Term.Get(ctx, raw)
	if e != nil && !ent.IsNotFound(e) {
		return "", e
	}
	if t != nil {
		for hops := 0; t.MergedIntoID != nil && hops < MaxDepth; hops++ {
			if t, e = s.Client.Term.Get(ctx, *t.MergedIntoID); e != nil {
				return "", e
			}
		}
		in, e := s.termInScope(ctx, workspaceID, d, set, t)
		if e != nil {
			return "", e
		}
		if in && !t.Deprecated {
			return t.ID, nil
		}
		if kept {
			return raw, nil
		}
		if !in {
			return "", invalid("term " + raw + " is not a value of " + d.Key)
		}
		return "", invalid("term " + raw + " of " + d.Key + " is deprecated")
	}
	if d.Type == "keywords" {
		kw, _, e := s.keyword(ctx, workspaceID, raw)
		if e != nil || kw != nil {
			return idOf(kw), e
		}
	} else {
		key := strings.ToLower(raw)
		for _, match := range []string{
			"normalized_name=?",
			"EXISTS(SELECT 1 FROM json_each(labels) WHERE unicode_lower(value)=?)",
			"EXISTS(SELECT 1 FROM json_each(synonyms) WHERE unicode_lower(value)=?)",
		} {
			found, e := s.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.DeprecatedEQ(false), func(sel *entsql.Selector) {
				sel.Where(entsql.ExprP(match, key))
			}).Limit(2).IDs(ctx)
			if e != nil {
				return "", e
			}
			if len(found) > 1 {
				return "", invalid("label " + raw + " of " + d.Key + " matches several terms; give the term ID")
			}
			if len(found) == 1 {
				return found[0], nil
			}
		}
		if !set.IsOpen {
			return "", invalid("label " + raw + " of " + d.Key + " is not a term of its closed term set")
		}
	}
	created, e := s.insertTerm(ctx, set, nil, TermInput{Name: raw, Labels: map[string]string{}, Synonyms: []string{}})
	if e != nil {
		return "", e
	}
	return created.ID, nil
}
func idOf(t *ent.Term) string {
	if t == nil {
		return ""
	}
	return t.ID
}

// termInScope reports whether t may be a value of d.
func (s *Service) termInScope(ctx context.Context, workspaceID string, d *ent.FieldDefinition, set *ent.TermSet, t *ent.Term) (bool, error) {
	if t.TermSetID == set.ID {
		return true, nil
	}
	if d.Type != "keywords" || !t.AvailableAsKeyword {
		return false, nil
	}
	return s.Client.TermSet.Query().Where(termset.IDEQ(t.TermSetID), termset.WorkspaceIDEQ(workspaceID)).Exist(ctx)
}

// termText is the search text of a revision's terms: the names, labels and
// synonyms of the terms its term and keywords fields hold (for a merged term,
// also those of the term it was merged into). It is captured when a surface is
// written, so a later rename shows in search after the item's next write.
func (s *Service) termText(ctx context.Context, payload []byte, defs []*ent.FieldDefinition) (string, error) {
	var values map[string]any
	ids := []string{}
	for _, d := range defs {
		if d.Type != "term" && d.Type != "keywords" {
			continue
		}
		if values == nil {
			var e error
			if values, e = decodeValues(payload); e != nil {
				return "", e
			}
		}
		list := []any{values[d.Key]}
		if d.Options.Multiple {
			list, _ = values[d.Key].([]any)
		}
		for _, v := range list {
			if id, ok := v.(string); ok {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	rows, e := s.Client.Term.Query().Where(term.IDIn(ids...)).WithMergedInto().All(ctx)
	if e != nil {
		return "", e
	}
	words := []string{}
	add := func(t *ent.Term) {
		words = append(words, t.Name)
		words = append(words, slices.Sorted(maps.Values(t.Labels))...)
		words = append(words, t.Synonyms...)
	}
	for _, t := range rows {
		add(t)
		if t.Edges.MergedInto != nil {
			add(t.Edges.MergedInto)
		}
	}
	return strings.Join(words, " "), nil
}

// ItemTerms returns the term IDs that values hold in the term and keywords
// fields of a collection.
func (s *Service) ItemTerms(ctx context.Context, collectionID string, values map[string]any) ([]string, error) {
	defs, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(collectionID), fielddefinition.TypeIn("term", "keywords")).All(ctx)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, d := range defs {
		list := []any{values[d.Key]}
		if d.Options.Multiple {
			list, _ = values[d.Key].([]any)
		}
		for _, v := range list {
			if id, ok := v.(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// RevisionTerms returns the term IDs an item revision holds, and those of
// the item's revision before it (none for a first revision).
func (s *Service) RevisionTerms(ctx context.Context, revisionID string) (current, previous []string, err error) {
	rev, err := s.Client.ItemRevision.Get(ctx, revisionID)
	if err != nil {
		return nil, nil, err
	}
	revisions := []*ent.ItemRevision{rev}
	if rev.RevisionNumber > 1 {
		before, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(rev.ItemID), itemrevision.RevisionNumberEQ(rev.RevisionNumber-1)).Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return nil, nil, err
		}
		if before != nil {
			revisions = append(revisions, before)
		}
	}
	out := [2][]string{{}, {}}
	for i, r := range revisions {
		values, err := decodeValues(r.Payload)
		if err != nil {
			return nil, nil, err
		}
		if out[i], err = s.ItemTerms(ctx, r.ContainerID, values); err != nil {
			return nil, nil, err
		}
	}
	return out[0], out[1], nil
}

// MatchTerms reports which of terms values hold: the term itself, one of its
// descendants, or a term merged into one of those. A merged term in terms
// stands for the term it was merged into.
func (s *Service) MatchTerms(ctx context.Context, terms, values []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(terms) == 0 || len(values) == 0 {
		return out, nil
	}
	rootsJSON, _ := json.Marshal(terms)
	valuesJSON, _ := json.Marshal(values)
	rows, e := s.Client.QueryContext(ctx, `SELECT DISTINCT j.value FROM json_each(?) j
JOIN terms r ON r.id=coalesce((SELECT merged_into_id FROM terms WHERE id=j.value),j.value)
JOIN json_each(?) v JOIN terms t ON t.id=coalesce((SELECT merged_into_id FROM terms WHERE id=v.value),v.value)
WHERE t.path>=r.path AND t.path<substr(r.path,1,length(r.path)-1)||'0'`, string(rootsJSON), string(valuesJSON))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out[id] = true
	}
	return out, rows.Err()
}
