package dms

import (
	"context"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/taxonomy"
)

// Term and keywords fields store term IDs. Writers may give a term's ID or
// its label instead:
//
//   - A term field (one term set) matches a label against the names, then the
//     localized labels, then the synonyms of the set's active terms; a label
//     that matches several terms is ambiguous. An open set gets a new root
//     term for an unknown label; a closed set rejects it.
//   - A keywords field takes keywords: terms of the organization's keywords set
//     and terms available as keywords. A label finds the keyword with that
//     name or synonym, or adds it to the keywords set.
//
// An ID of a merged term stands for the term it was merged into. Deprecated
// terms are rejected unless the value was already there.

func (s *Service) resolveTermValues(ctx context.Context, subject string, d *catalog.FieldDefinition, values []any, old map[string]bool) ([]any, error) {
	setID := taxonomy.KeywordsSetID
	if d.Type != catalog.FieldKeywords {
		setID = d.Options.TermSetID
	}
	set, e := s.DAL.Taxonomy().Set(ctx, setID)
	if e != nil {
		return nil, e
	}
	out := make([]any, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		raw := v.(string)
		id, e := s.resolveTermID(ctx, subject, d, set, raw, old[raw])
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
func (s *Service) resolveTermID(ctx context.Context, subject string, d *catalog.FieldDefinition, set taxonomy.Set, raw string, kept bool) (string, error) {
	terms := s.DAL.Taxonomy()
	t, e := terms.Canonical(ctx, raw)
	if e == nil {
		in := t.SetID == set.ID || d.Type == catalog.FieldKeywords && t.AvailableAsKeyword
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
	if !dberr.IsNotFound(e) {
		return "", e
	}
	if d.Type == catalog.FieldKeywords {
		kw, _, found, e := terms.Keyword(ctx, raw)
		if e != nil || found {
			return kw.ID, e
		}
	} else {
		found, e := terms.Lookup(ctx, set.ID, raw)
		if e != nil {
			return "", e
		}
		if len(found) > 1 {
			return "", invalid("label " + raw + " of " + d.Key + " matches several terms; give the term ID")
		}
		if len(found) == 1 {
			return found[0].ID, nil
		}
		if !set.Open {
			return "", invalid("label " + raw + " of " + d.Key + " is not a term of its closed term set")
		}
	}
	if e := s.requireTaxonomy(ctx, subject, "read"); e != nil {
		return "", e
	}
	created, e := s.insertTerm(ctx, set.ID, TermInput{Name: raw, Labels: map[string]string{}, Synonyms: []string{}})
	if e != nil {
		return "", e
	}
	return created.ID, nil
}

// ItemTerms returns the term IDs that values hold in the term and keywords
// fields of a collection.
func (s *Service) ItemTerms(ctx context.Context, collectionID string, values map[string]any) ([]string, error) {
	defs, e := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: collectionID, Types: []catalog.FieldType{catalog.FieldTerm, catalog.FieldKeywords}})
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
	rev, err := s.revision(ctx, revisionID)
	if err != nil {
		return nil, nil, err
	}
	revs := []*revisions.Revision{rev}
	if rev.RevisionNumber > 1 {
		before, err := s.DAL.Revisions().Numbered(ctx, rev.ItemID, rev.RevisionNumber-1)
		if err != nil && !dberr.IsNotFound(err) {
			return nil, nil, err
		}
		if err == nil {
			revs = append(revs, &before)
		}
	}
	out := [2][]string{{}, {}}
	for i, r := range revs {
		values, err := decodeValues(r.Payload)
		if err != nil {
			return nil, nil, err
		}
		if out[i], err = s.ItemTerms(ctx, r.ContainerID, values); err != nil {
			return nil, nil, err
		}
		out[i] = append(out[i], r.Keywords...)
	}
	return out[0], out[1], nil
}

// MatchTerms reports which of terms values hold: the term itself, one of its
// descendants, or a term merged into one of those. A merged term in terms
// stands for the term it was merged into.
func (s *Service) MatchTerms(ctx context.Context, terms, values []string) (map[string]bool, error) {
	return s.DAL.Taxonomy().Match(ctx, terms, values)
}
