package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/term"
	"papergo/ent/termset"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
)

// Keywords are the terms of a organization's keywords set plus the terms of
// other sets that are available as keywords (promoted keywords). People with
// read access add keywords; managers curate them by promoting keywords into
// managed sets.

const maxKeywordSuggestions = 20

func (s *Service) keywordsSet(ctx context.Context) (*ent.TermSet, error) {
	return s.Client.TermSet.Query().Where(termset.IsKeywordsEQ(true)).Only(ctx)
}

// keywordTerms selects the active organization keyword terms.
func keywordTerms(q *ent.TermQuery, keywordsSetID string) *ent.TermQuery {
	return q.Where(term.DeprecatedEQ(false), term.Or(term.TermSetIDEQ(keywordsSetID),
		term.AvailableAsKeywordEQ(true)))
}

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
		set, e := t.keywordsSet(ctx)
		if e != nil {
			return nil, e
		}
		q := keywordTerms(t.Client.Term.Query(), set.ID)
		needle := strings.ToLower(strings.TrimSpace(search))
		if needle != "" {
			q.Where(func(sel *entsql.Selector) {
				sel.Where(entsql.ExprP("(instr(normalized_name,?)>0 OR EXISTS(SELECT 1 FROM json_each(synonyms) WHERE instr(unicode_lower(value),?)>0) OR EXISTS(SELECT 1 FROM json_each(labels) WHERE instr(unicode_lower(value),?)>0))", needle, needle, needle))
			})
		}
		rows, e := q.Order(func(sel *entsql.Selector) {
			sel.OrderExpr(entsql.ExprP("instr(normalized_name,?)<>1", needle))
		}, ent.Asc(term.FieldNormalizedName), ent.Asc(term.FieldID)).Limit(limit).All(ctx)
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
		v, set, e := t.keyword(ctx, name)
		if e != nil || v != nil {
			out = TermView{Term: v}
			return e
		}
		if v, e = t.insertTerm(ctx, set, nil, TermInput{Name: name, Labels: map[string]string{}, Synonyms: []string{}}); e != nil {
			return e
		}
		out, created = TermView{Term: v}, true
		return t.auditTaxonomy(ctx, subject, "keyword.create", map[string]any{"term_id": v.ID})
	})
	return
}

// keyword finds the active keyword called name, if any. A root keyword of the
// keywords set wins over a promoted term.
func (s *Service) keyword(ctx context.Context, name string) (*ent.Term, *ent.TermSet, error) {
	set, e := s.keywordsSet(ctx)
	if e != nil {
		return nil, nil, e
	}
	key := strings.ToLower(name)
	rows, e := keywordTerms(s.Client.Term.Query(), set.ID).Where(func(sel *entsql.Selector) {
		sel.Where(entsql.ExprP("(normalized_name=? OR EXISTS(SELECT 1 FROM json_each(synonyms) WHERE unicode_lower(value)=?))", key, key))
	}).Order(func(sel *entsql.Selector) {
		sel.OrderExpr(entsql.ExprP("(term_set_id<>? OR parent_id IS NOT NULL OR normalized_name<>?)", set.ID, key))
	}, ent.Asc(term.FieldID)).Limit(1).All(ctx)
	if e != nil || len(rows) == 0 {
		return nil, set, e
	}
	return rows[0], set, nil
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
		set, e := t.keywordsSet(ctx)
		if e != nil {
			return nil, e
		}
		rows, e := t.Client.QueryContext(ctx, `WITH usage AS (
 SELECT p.item_id,coalesce(t.merged_into_id,t.id) keyword FROM item_surfaces p JOIN resources c ON c.id=p.container_id AND c.deleted_at IS NULL JOIN resources r ON r.id=p.item_id AND r.deleted_at IS NULL JOIN terms t ON t.id IN (`+surfaceKeywordsSQL+`) WHERE p.surface=CASE WHEN c.publishing_enabled THEN 'head' ELSE 'published' END
 ) SELECT u.keyword,count(DISTINCT u.item_id) n FROM usage u JOIN terms t ON t.id=u.keyword WHERE t.deprecated=0 AND (t.term_set_id=? OR t.available_as_keyword) GROUP BY u.keyword ORDER BY n DESC,u.keyword LIMIT ?`, set.ID, top)
		if e != nil {
			return nil, e
		}
		counts := map[string]int{}
		ids := []string{}
		for rows.Next() {
			var id string
			var n int
			if e = rows.Scan(&id, &n); e != nil {
				break
			}
			counts[id] = n
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return nil, e
		}
		terms, e := t.Client.Term.Query().Where(term.IDIn(ids...)).All(ctx)
		if e != nil {
			return nil, e
		}
		byID := map[string]*ent.Term{}
		for _, v := range terms {
			byID[v.ID] = v
		}
		ordered := make([]*ent.Term, len(ids))
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
		if !from.IsKeywords || kw.Deprecated {
			return invalid("only active keywords of the keywords set can be promoted")
		}
		set, e := t.Client.TermSet.Get(ctx, in.TermSetID)
		if ent.IsNotFound(e) || e == nil && set.IsKeywords {
			return invalid("term_set_id must identify another organization term set")
		}
		if e != nil {
			return e
		}
		parent, e := t.moveParent(ctx, set, in.ParentID)
		if e != nil {
			return e
		}
		key := kw.NormalizedName
		matches, e := t.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.DeprecatedEQ(false), func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP("(normalized_name=? OR EXISTS(SELECT 1 FROM json_each(labels) WHERE unicode_lower(value)=?) OR EXISTS(SELECT 1 FROM json_each(synonyms) WHERE unicode_lower(value)=?))", key, key, key))
		}).Order(func(sel *entsql.Selector) { sel.OrderExpr(entsql.ExprP("normalized_name<>?", key)) }, ent.Asc(term.FieldPath)).Limit(1).All(ctx)
		if e != nil {
			return e
		}
		var promoted *ent.Term
		action := "keyword.promote"
		if len(matches) > 0 {
			action = "keyword.merge"
			if promoted, e = t.mergeInto(ctx, subject, kw, matches[0], set, true); e != nil {
				return e
			}
		} else {
			if promoted, e = t.relocate(ctx, kw, set, parent); e != nil {
				return e
			}
			if promoted, e = t.Client.Term.UpdateOne(promoted).SetAvailableAsKeyword(true).Save(ctx); e != nil {
				return e
			}
		}
		views, e := t.termViews(ctx, []*ent.Term{promoted})
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
	normalized, err := s.resolveTermValues(ctx, subject, &ent.FieldDefinition{Key: "$keywords", Type: "keywords"}, input, old)
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
const surfaceKeywordsSQL = "SELECT keyword FROM item_surface_keywords WHERE surface_id=p.id UNION SELECT value_text FROM field_values WHERE surface_id=p.id AND (field_type='keywords' OR field_type='term' AND value_text IN (SELECT t.id FROM terms t JOIN term_sets ts ON ts.id=t.term_set_id WHERE ts.is_keywords OR t.available_as_keyword))"

// Keyword filters accept the same IDs and labels as keyword assignment, but
// querying never adds terms to the vocabulary.
func (s *Service) keywordFilter(ctx context.Context, raw string) (string, error) {
	if raw == "" {
		return raw, nil
	}
	found, err := s.Client.Term.Get(ctx, raw)
	if err != nil && !ent.IsNotFound(err) {
		return "", err
	}
	if found == nil {
		found, _, err = s.keyword(ctx, strings.TrimSpace(raw))
		if err != nil {
			return "", err
		}
	}
	if found == nil {
		return raw, nil
	}
	if found.MergedIntoID != nil {
		return *found.MergedIntoID, nil
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
			value, err = s.keywordFilter(ctx, value)
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
func (s *Service) normalizeKeywordDefault(ctx context.Context, subject string, d *ent.FieldDefinition) error {
	values := map[string]any{}
	if err := s.normalizeItemValues(ctx, subject, []*ent.FieldDefinition{d}, values, nil, true); err != nil {
		return err
	}
	if d.Type == "keywords" || d.Type == "term" {
		raw, err := json.Marshal(values[d.Key])
		if err != nil {
			return err
		}
		d.Options.DefaultValue = raw
	}
	return nil
}
