package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/resource"
	"papergo/ent/term"
	"papergo/ent/termset"
	"strings"
	"unicode/utf8"
)

func (s *Service) workspace(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	w, e := s.authorize(ctx, subject, id, action)
	if e != nil {
		return nil, e
	}
	if w.Kind != resource.KindWorkspace {
		return nil, invalid("workspace_id must identify a workspace")
	}
	return w, nil
}
func validateDescription(s string) error {
	if !utf8.ValidString(s) || len(s) > 4096 {
		return invalid("description exceeds 4096 bytes")
	}
	return nil
}

type TermSetInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Service) CreateTermSet(ctx context.Context, subject, workspaceID string, in TermSetInput) (out *ent.TermSet, err error) {
	if !fieldKey.MatchString(in.Key) {
		return nil, invalid("invalid term set key")
	}
	if e := validateName(in.Name); e != nil {
		return nil, e
	}
	if e := validateDescription(in.Description); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		out, e = t.Client.TermSet.Create().SetWorkspaceID(w.ID).SetKey(in.Key).SetName(in.Name).SetDescription(in.Description).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_set.create", w, map[string]any{"term_set_id": out.ID})
	})
	return
}
func (s *Service) TermSets(ctx context.Context, subject, workspaceID, after string, limit int) (Page[*ent.TermSet], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.TermSet], error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*ent.TermSet]{}, e
		}
		rows, e := t.Client.TermSet.Query().Where(termset.WorkspaceIDEQ(workspaceID), termset.IDGT(after)).Order(ent.Asc(termset.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.TermSet) string { return v.ID }), e
	})
}
func (s *Service) UpdateTermSet(ctx context.Context, subject, setID string, version int, in TermSetInput) (out *ent.TermSet, err error) {
	if e := validateName(in.Name); e != nil {
		return nil, e
	}
	if e := validateDescription(in.Description); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		set, e := t.Client.TermSet.Get(ctx, setID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		w, e := t.workspace(ctx, subject, set.WorkspaceID, "manage")
		if e != nil {
			return e
		}
		if set.Version != version {
			return ErrConflict
		}
		if in.Key != "" && in.Key != set.Key {
			return invalid("term set key is immutable")
		}
		out, e = t.Client.TermSet.UpdateOne(set).SetName(in.Name).SetDescription(in.Description).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_set.update", w, map[string]any{"term_set_id": setID})
	})
	return
}

type TermInput struct {
	Name       string            `json:"name"`
	ParentID   *string           `json:"parent_id,omitempty"`
	Labels     map[string]string `json:"labels"`
	Synonyms   []string          `json:"synonyms"`
	Deprecated bool              `json:"deprecated"`
}

func normalizeTerm(in *TermInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if e := validateName(in.Name); e != nil {
		return e
	}
	if in.Labels == nil {
		in.Labels = map[string]string{}
	}
	if len(in.Labels) > 30 {
		return invalid("at most 30 localized labels")
	}
	for language, name := range in.Labels {
		if len(language) < 2 || len(language) > 35 || strings.TrimSpace(language) != language {
			return invalid("invalid language label key")
		}
		if e := validateName(name); e != nil {
			return e
		}
	}
	if in.Synonyms == nil {
		in.Synonyms = []string{}
	}
	if len(in.Synonyms) > 50 {
		return invalid("at most 50 synonyms")
	}
	seen := map[string]bool{}
	for _, v := range in.Synonyms {
		if v != strings.TrimSpace(v) {
			return invalid("synonyms must be trimmed")
		}
		if e := validateName(v); e != nil {
			return e
		}
		key := strings.ToLower(v)
		if seen[key] {
			return invalid("duplicate synonym")
		}
		seen[key] = true
	}
	return nil
}
func (s *Service) CreateTerm(ctx context.Context, subject, setID string, in TermInput) (out *ent.Term, err error) {
	if e := normalizeTerm(&in); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		set, e := t.Client.TermSet.Get(ctx, setID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		w, e := t.workspace(ctx, subject, set.WorkspaceID, "manage")
		if e != nil {
			return e
		}
		parent := in.ParentID
		depth := 0
		for parent != nil {
			p, e := t.Client.Term.Get(ctx, *parent)
			if ent.IsNotFound(e) {
				return invalid("unknown parent term")
			}
			if e != nil {
				return e
			}
			if p.TermSetID != setID || p.Deprecated {
				return invalid("parent must be active and in the same term set")
			}
			depth++
			if depth >= MaxDepth {
				return invalid("term hierarchy depth exceeded")
			}
			parent = p.ParentID
		}
		out, e = t.Client.Term.Create().SetTermSetID(setID).SetNillableParentID(in.ParentID).SetName(in.Name).SetNormalizedName(strings.ToLower(in.Name)).SetLabels(in.Labels).SetSynonyms(in.Synonyms).SetDeprecated(in.Deprecated).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "term.create", w, map[string]any{"term_id": out.ID, "term_set_id": setID})
	})
	return
}
func (s *Service) UpdateTerm(ctx context.Context, subject, termID string, version int, in TermInput) (out *ent.Term, err error) {
	if e := normalizeTerm(&in); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		old, e := t.Client.Term.Get(ctx, termID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		set, e := t.Client.TermSet.Get(ctx, old.TermSetID)
		if e != nil {
			return e
		}
		w, e := t.workspace(ctx, subject, set.WorkspaceID, "manage")
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrConflict
		}
		if in.ParentID != nil && (old.ParentID == nil || *in.ParentID != *old.ParentID) {
			return invalid("term parent is immutable")
		}
		out, e = t.Client.Term.UpdateOne(old).SetName(in.Name).SetNormalizedName(strings.ToLower(in.Name)).SetLabels(in.Labels).SetSynonyms(in.Synonyms).SetDeprecated(in.Deprecated).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "term.update", w, map[string]any{"term_id": termID, "deprecated": in.Deprecated})
	})
	return
}
func (s *Service) Terms(ctx context.Context, subject, setID, search, after string, limit int) (Page[*ent.Term], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.Term], error) {
		set, e := t.Client.TermSet.Get(ctx, setID)
		if ent.IsNotFound(e) {
			return Page[*ent.Term]{}, ErrNotFound
		}
		if e != nil {
			return Page[*ent.Term]{}, e
		}
		if _, e = t.workspace(ctx, subject, set.WorkspaceID, "read"); e != nil {
			return Page[*ent.Term]{}, e
		}
		if len(search) > 255 {
			return Page[*ent.Term]{}, invalid("term search exceeds 255 bytes")
		}
		q := t.Client.Term.Query().Where(term.TermSetIDEQ(setID), term.IDGT(after))
		if search != "" {
			needle := strings.ToLower(search)
			q.Where(func(sel *entsql.Selector) {
				// unicode_lower is registered by internal/database; SQLite's lower() folds ASCII only.
				sel.Where(entsql.ExprP("(instr(unicode_lower(name),?)>0 OR EXISTS(SELECT 1 FROM json_each(synonyms) WHERE instr(unicode_lower(value),?)>0) OR EXISTS(SELECT 1 FROM json_each(labels) WHERE instr(unicode_lower(value),?)>0))", needle, needle, needle))
			})
		}
		rows, e := q.Order(ent.Asc(term.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.Term) string { return v.ID }), e
	})
}
func entityPage[T any](rows []T, limit int, id func(T) string) Page[T] {
	size := pageSize(limit)
	out := Page[T]{Data: []T{}}
	if len(rows) > size {
		out.NextCursor = id(rows[size-1])
		rows = rows[:size]
	}
	out.Data = append(out.Data, rows...)
	return out
}
