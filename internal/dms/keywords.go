package dms

import (
	"context"
	"encoding/json"
	"strings"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
)

// Keywords are the terms of a organization's keywords set plus the terms of
// other sets that are available as keywords (promoted keywords). People with
// read access add keywords; managers curate them by promoting keywords into
// managed sets.

const maxKeywordSuggestions = 20

// Keywords suggests up to 20 keywords whose name, label or synonym contains
// search; names that start with it come first.
func (s *Service) Keywords(ctx context.Context, subject, search string, limit int) ([]TermView, error) {
	if len(search) > maxTermSearch {
		return nil, invalid("keyword search exceeds 255 bytes")
	}
	if limit < 1 || limit > maxKeywordSuggestions {
		limit = maxKeywordSuggestions
	}
	return read(ctx, s, func(t *Service) ([]TermView, error) {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return nil, e
		}
		set, e := t.DAL.Taxonomy().KeywordsSet(ctx)
		if e != nil {
			return nil, e
		}
		rows, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{KeywordSet: set.ID, Current: true, Search: strings.ToLower(strings.TrimSpace(search)), Order: taxonomy.OrderPrefix, Limit: limit})
		if e != nil {
			return nil, e
		}
		return t.termViews(ctx, rows)
	})
}

type KeywordInput struct {
	Name string `json:"name"`
}

// AddKeyword returns the keyword named name, matching names and synonyms of
// keywords without regard to case, or adds it to the keywords set.
func (s *Service) AddKeyword(ctx context.Context, subject string, in KeywordInput) (out TermView, created bool, err error) {
	name := strings.TrimSpace(in.Name)
	if e := validateName(name); e != nil {
		return out, false, e
	}
	err = s.write(ctx, func(t *Service) error {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return e
		}
		v, set, found, e := t.DAL.Taxonomy().Keyword(ctx, name)
		if e != nil || found {
			out = viewTerm(v)
			return e
		}
		if v, e = t.insertTerm(ctx, set.ID, TermInput{Name: name, Labels: map[string]string{}, Synonyms: []string{}}); e != nil {
			return e
		}
		out, created = viewTerm(v), true
		return t.auditTaxonomy(ctx, subject, "keyword.create", map[string]any{"term_id": v.ID})
	})
	return
}

type KeywordCount struct {
	Term  TermView `json:"term"`
	Count int      `json:"count"`
}

// PopularKeywords counts the items whose current content holds each keyword
// in a keywords field or a term field of the keywords set, most used first,
// for managers deciding what to promote.
func (s *Service) PopularKeywords(ctx context.Context, subject string, top int) ([]KeywordCount, error) {
	if top < 1 || top > 100 {
		top = maxKeywordSuggestions
	}
	return read(ctx, s, func(t *Service) ([]KeywordCount, error) {
		if e := t.requireTaxonomy(ctx, subject, "manage"); e != nil {
			return nil, e
		}
		set, e := t.DAL.Taxonomy().KeywordsSet(ctx)
		if e != nil {
			return nil, e
		}
		popular, e := t.DAL.Content().PopularKeywords(ctx, set.ID, top)
		if e != nil {
			return nil, e
		}
		counts := map[string]int{}
		ids := []string{}
		for _, c := range popular {
			counts[c.ID] = c.Count
			ids = append(ids, c.ID)
		}
		if len(ids) == 0 {
			return []KeywordCount{}, nil
		}
		terms, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: ids})
		if e != nil {
			return nil, e
		}
		byID := map[string]taxonomy.Term{}
		for _, v := range terms {
			byID[v.ID] = v
		}
		ordered := make([]taxonomy.Term, len(ids))
		for i, id := range ids {
			ordered[i] = byID[id]
		}
		views, e := t.termViews(ctx, ordered)
		if e != nil {
			return nil, e
		}
		out := make([]KeywordCount, len(views))
		for i, v := range views {
			out[i] = KeywordCount{Term: v, Count: counts[v.ID]}
		}
		return out, nil
	})
}

type PromoteKeywordInput struct {
	TermSetID string  `json:"term_set_id"`
	ParentID  *string `json:"parent_id,omitempty"`
}

// PromoteKeyword moves a keyword into a managed set, where it stays
// available as a keyword. When the set already has an active term with the
// keyword's name, label or synonym, the keyword merges into that term
// instead, so values that hold the keyword now mean the managed term.
func (s *Service) PromoteKeyword(ctx context.Context, subject, id string, version int, in PromoteKeywordInput) (out TermView, err error) {
	err = s.write(ctx, func(t *Service) error {
		kw, from, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		if !from.Keywords || kw.Deprecated {
			return invalid("only active keywords of the keywords set can be promoted")
		}
		set, e := t.DAL.Taxonomy().Set(ctx, in.TermSetID)
		if dberr.IsNotFound(e) || e == nil && set.Keywords {
			return invalid("term_set_id must identify another organization term set")
		}
		if e != nil {
			return e
		}
		matches, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Current: true, Match: kw.NormalizedName,
			In: taxonomy.MatchName | taxonomy.MatchLabels | taxonomy.MatchSynonyms, Order: taxonomy.OrderExact, Limit: 1})
		if e != nil {
			return e
		}
		var promoted taxonomy.Term
		action := "keyword.promote"
		if len(matches) > 0 {
			action = "keyword.merge"
			if promoted, e = t.mergeInto(ctx, subject, kw, matches[0], true); e != nil {
				return e
			}
		} else {
			parent := ""
			if in.ParentID != nil {
				parent = *in.ParentID
			}
			if promoted, e = t.DAL.Taxonomy().Move(ctx, kw.ID, version, set.ID, parent, true); e != nil {
				return taxonomyError(e)
			}
			if e = t.emitTermMoved(ctx, subject, kw, promoted); e != nil {
				return e
			}
		}
		views, e := t.termViews(ctx, []taxonomy.Term{promoted})
		if e != nil {
			return e
		}
		out = views[0]
		return t.auditTaxonomy(ctx, subject, action, map[string]any{"term_id": id, "term_set_id": set.ID, "result_term_id": promoted.ID})
	})
	return
}

// normalizeKeywords gives every resource the same term-backed folksonomy as
// keywords fields. Labels create or reuse terms in the shared keywords set.
func (s *Service) normalizeKeywords(ctx context.Context, subject string, values, previous []string) ([]string, error) {
	if len(values) == 0 {
		return []string{}, nil
	}
	if err := validateKeywords(values); err != nil {
		return nil, err
	}
	input := make([]any, len(values))
	old := map[string]bool{}
	for i, value := range values {
		input[i] = value
	}
	for _, value := range previous {
		old[value] = true
	}
	normalized, err := s.resolveTermValues(ctx, subject, &catalog.FieldDefinition{Key: "$keywords", Type: "keywords"}, input, old)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(normalized))
	for i, value := range normalized {
		out[i] = value.(string)
	}
	return out, nil
}

// Surface keywords include the intrinsic keywords and every indexed keywords
// field, with one occurrence per term even if several fields hold it.

// CanonicalKeyword resolves a keyword filter to its canonical term ID.
// Keyword filters accept the same IDs and labels as keyword assignment, but
// querying never adds terms to the vocabulary.
func (s *Service) CanonicalKeyword(ctx context.Context, raw string) (string, error) {
	if raw == "" {
		return raw, nil
	}
	terms := s.DAL.Taxonomy()
	found, err := terms.Term(ctx, raw)
	if dberr.IsNotFound(err) {
		var ok bool
		if found, _, ok, err = terms.Keyword(ctx, strings.TrimSpace(raw)); err == nil && !ok {
			return raw, nil
		}
	}
	if err != nil {
		return "", err
	}
	if found.Merged() {
		return found.MergedIntoID, nil
	}
	return found.ID, nil
}

func (s *Service) keywordFilters(ctx context.Context, filter *FilterExpr) (*FilterExpr, error) {
	if filter == nil {
		return nil, nil
	}
	out := *filter
	var err error
	if filter.Field == "$keywords" && len(filter.Value) > 0 {
		var value string
		if json.Unmarshal(filter.Value, &value) == nil {
			value, err = s.CanonicalKeyword(ctx, value)
			if err != nil {
				return nil, err
			}
			out.Value, _ = json.Marshal(value)
		}
	}
	out.And = append([]FilterExpr(nil), filter.And...)
	out.Or = append([]FilterExpr(nil), filter.Or...)
	for _, children := range [][]FilterExpr{out.And, out.Or} {
		for i := range children {
			child, e := s.keywordFilters(ctx, &children[i])
			if e != nil {
				return nil, e
			}
			children[i] = *child
		}
	}
	out.Not, err = s.keywordFilters(ctx, filter.Not)
	return &out, err
}

// normalizeKeywordDefault stores keyword and taxonomy defaults as the same
// term IDs used by assigned values, including in portable schema definitions.
func (s *Service) normalizeKeywordDefault(ctx context.Context, subject string, d *catalog.FieldDefinition) error {
	values := map[string]any{}
	if err := s.normalizeItemValues(ctx, subject, []*catalog.FieldDefinition{d}, values, nil, true); err != nil {
		return err
	}
	if d.Type == catalog.FieldKeywords || d.Type == catalog.FieldTerm {
		raw, err := json.Marshal(values[d.Key])
		if err != nil {
			return err
		}
		d.Options.DefaultValue = raw
	}
	return nil
}
