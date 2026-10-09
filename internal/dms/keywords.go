package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/term"
	"papergo/ent/termset"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
)

// Keywords are the terms of a workspace's keywords set plus the terms of
// other sets that are available as keywords (promoted keywords). People with
// write access add keywords; managers curate them by promoting keywords into
// managed sets.

const maxKeywordSuggestions = 20

func (s *Service) keywordsSet(ctx context.Context, workspaceID string) (*ent.TermSet, error) {
	return s.Client.TermSet.Query().Where(termset.WorkspaceIDEQ(workspaceID), termset.IsKeywordsEQ(true)).Only(ctx)
}

// keywordTerms selects the active keyword terms of a workspace.
func keywordTerms(q *ent.TermQuery, workspaceID, keywordsSetID string) *ent.TermQuery {
	return q.Where(term.DeprecatedEQ(false), term.Or(term.TermSetIDEQ(keywordsSetID),
		term.And(term.AvailableAsKeywordEQ(true), term.HasTermSetWith(termset.WorkspaceIDEQ(workspaceID)))))
}

// Keywords suggests up to 20 keywords whose name, label or synonym contains
// search; names that start with it come first.
func (s *Service) Keywords(ctx context.Context, subject, workspaceID, search string, limit int) ([]TermView, error) {
	if len(search) > maxTermSearch {
		return nil, invalid("keyword search exceeds 255 bytes")
	}
	if limit < 1 || limit > maxKeywordSuggestions {
		limit = maxKeywordSuggestions
	}
	return read(ctx, s, func(t *Service) ([]TermView, error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return nil, e
		}
		set, e := t.keywordsSet(ctx, workspaceID)
		if e != nil {
			return nil, e
		}
		q := keywordTerms(t.Client.Term.Query(), workspaceID, set.ID)
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
func (s *Service) AddKeyword(ctx context.Context, subject, workspaceID string, in KeywordInput) (out TermView, created bool, err error) {
	name := strings.TrimSpace(in.Name)
	if e := validateName(name); e != nil {
		return out, false, e
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "write")
		if e != nil {
			return e
		}
		v, set, e := t.keyword(ctx, w.ID, name)
		if e != nil || v != nil {
			out = TermView{Term: v}
			return e
		}
		if v, e = t.insertTerm(ctx, set, nil, TermInput{Name: name, Labels: map[string]string{}, Synonyms: []string{}}); e != nil {
			return e
		}
		out, created = TermView{Term: v}, true
		return t.audit(ctx, subject, "keyword.create", w, map[string]any{"term_id": v.ID})
	})
	return
}

// keyword finds the active keyword called name, if any. A root keyword of the
// keywords set wins over a promoted term.
func (s *Service) keyword(ctx context.Context, workspaceID, name string) (*ent.Term, *ent.TermSet, error) {
	set, e := s.keywordsSet(ctx, workspaceID)
	if e != nil {
		return nil, nil, e
	}
	key := strings.ToLower(name)
	rows, e := keywordTerms(s.Client.Term.Query(), workspaceID, set.ID).Where(func(sel *entsql.Selector) {
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

// PopularKeywords counts the items whose current content holds each keyword,
// most used first, for managers deciding what to promote.
func (s *Service) PopularKeywords(ctx context.Context, subject, workspaceID string, top int) ([]KeywordCount, error) {
	if top < 1 || top > 100 {
		top = maxKeywordSuggestions
	}
	return read(ctx, s, func(t *Service) ([]KeywordCount, error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "manage"); e != nil {
			return nil, e
		}
		set, e := t.keywordsSet(ctx, workspaceID)
		if e != nil {
			return nil, e
		}
		rows, e := t.Client.QueryContext(ctx, `SELECT f.value_text,count(DISTINCT f.item_id) n
FROM field_definitions fd JOIN resources c ON c.id=fd.container_id AND c.workspace_id=? AND c.deleted_at IS NULL
JOIN field_values f ON f.container_id=fd.container_id AND f.surface='head' AND f.field_key=fd.key
JOIN terms t ON t.id=f.value_text AND t.deprecated=0 AND (t.term_set_id=? OR t.available_as_keyword)
WHERE fd.type='term' AND fd.indexed AND json_extract(fd.options,'$.term_set_id')=?
GROUP BY f.value_text ORDER BY n DESC,f.value_text LIMIT ?`, workspaceID, set.ID, set.ID, top)
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
		kw, from, w, e := t.termForChange(ctx, subject, id, version)
		if e != nil {
			return e
		}
		if !from.IsKeywords || kw.Deprecated {
			return invalid("only active keywords of the keywords set can be promoted")
		}
		set, e := t.Client.TermSet.Get(ctx, in.TermSetID)
		if ent.IsNotFound(e) || e == nil && (set.WorkspaceID != from.WorkspaceID || set.IsKeywords) {
			return invalid("term_set_id must identify another term set of the workspace")
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
		return t.audit(ctx, subject, action, w, map[string]any{"term_id": id, "term_set_id": set.ID, "result_term_id": promoted.ID})
	})
	return
}
