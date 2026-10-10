package dms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/resource"
	"papergo/ent/term"
	"papergo/ent/termgroup"
	"papergo/ent/termset"
	"regexp"
	"strings"
	"unicode/utf8"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

// The taxonomy of a workspace: term groups hold term sets, term sets hold
// trees of terms. Every workspace has a system group with its keywords set,
// the open set of free keywords. Reading needs workspace read; adding terms
// to an open set needs write; everything else needs manage.

const (
	SystemTermGroupName = "System"
	KeywordsTermSetKey  = "keywords"
	maxTermSearch       = 255
)

var termColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

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

// createWorkspaceTaxonomy adds the system group and the keywords set of a
// new workspace.
func (s *Service) createWorkspaceTaxonomy(ctx context.Context, workspaceID string) error {
	g, e := s.Client.TermGroup.Create().SetWorkspaceID(workspaceID).SetName(SystemTermGroupName).SetDescription("Term sets managed by PaperGo.").SetIsSystem(true).Save(ctx)
	if e != nil {
		return e
	}
	return s.Client.TermSet.Create().SetWorkspaceID(workspaceID).SetGroupID(g.ID).SetKey(KeywordsTermSetKey).SetName("Keywords").
		SetDescription("Free keywords that people add while tagging items.").SetIsOpen(true).SetIsKeywords(true).Exec(ctx)
}

type TermGroupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (in *TermGroupInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if e := validateName(in.Name); e != nil {
		return e
	}
	return validateDescription(in.Description)
}
func (s *Service) CreateTermGroup(ctx context.Context, subject, workspaceID string, in TermGroupInput) (out *ent.TermGroup, err error) {
	if e := in.validate(); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		if out, e = t.Client.TermGroup.Create().SetWorkspaceID(w.ID).SetName(in.Name).SetDescription(in.Description).Save(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_group.create", w, map[string]any{"term_group_id": out.ID})
	})
	return
}
func (s *Service) TermGroups(ctx context.Context, subject, workspaceID, after string, limit int) (Page[*ent.TermGroup], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.TermGroup], error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*ent.TermGroup]{}, e
		}
		rows, e := t.Client.TermGroup.Query().Where(termgroup.WorkspaceIDEQ(workspaceID), termgroup.IDGT(after)).Order(ent.Asc(termgroup.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.TermGroup) string { return v.ID }), e
	})
}
func (s *Service) TermGroup(ctx context.Context, subject, id string) (*ent.TermGroup, error) {
	return read(ctx, s, func(t *Service) (*ent.TermGroup, error) {
		g, _, e := t.termGroup(ctx, subject, id, "read")
		return g, e
	})
}
func (s *Service) termGroup(ctx context.Context, subject, id, action string) (*ent.TermGroup, *ent.Resource, error) {
	g, e := s.Client.TermGroup.Get(ctx, id)
	if ent.IsNotFound(e) {
		return nil, nil, ErrNotFound
	}
	if e != nil {
		return nil, nil, e
	}
	w, e := s.workspace(ctx, subject, g.WorkspaceID, action)
	return g, w, e
}
func (s *Service) UpdateTermGroup(ctx context.Context, subject, id string, version int, in TermGroupInput) (out *ent.TermGroup, err error) {
	if e := in.validate(); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		g, w, e := t.termGroup(ctx, subject, id, "manage")
		if e != nil {
			return e
		}
		if g.Version != version {
			return ErrConflict
		}
		if g.IsSystem {
			return invalid("the system term group is managed by PaperGo")
		}
		if out, e = t.Client.TermGroup.UpdateOne(g).SetName(in.Name).SetDescription(in.Description).AddVersion(1).Save(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_group.update", w, map[string]any{"term_group_id": id})
	})
	return
}

// DeleteTermGroup deletes an empty group other than the system group.
func (s *Service) DeleteTermGroup(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		g, w, e := t.termGroup(ctx, subject, id, "manage")
		if e != nil {
			return e
		}
		if g.Version != version {
			return ErrConflict
		}
		if g.IsSystem {
			return invalid("the system term group cannot be deleted")
		}
		used, e := t.Client.TermSet.Query().Where(termset.GroupIDEQ(id)).Exist(ctx)
		if e != nil {
			return e
		}
		if used {
			return invalid("delete or empty the group's term sets first")
		}
		if e = t.Client.TermGroup.DeleteOne(g).Exec(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_group.delete", w, map[string]any{"term_group_id": id})
	})
}

type TermSetInput struct {
	GroupID     string `json:"group_id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// IsOpen lets people with write access add terms; closed sets change
	// only with manage access.
	IsOpen bool `json:"is_open"`
}

func (in *TermSetInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if e := validateName(in.Name); e != nil {
		return e
	}
	return validateDescription(in.Description)
}
func (s *Service) CreateTermSet(ctx context.Context, subject, workspaceID string, in TermSetInput) (out *ent.TermSet, err error) {
	if !fieldKey.MatchString(in.Key) {
		return nil, invalid("invalid term set key")
	}
	if e := in.validate(); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		g, e := t.Client.TermGroup.Get(ctx, in.GroupID)
		if ent.IsNotFound(e) || e == nil && g.WorkspaceID != w.ID {
			return invalid("group_id must identify a term group of the workspace")
		}
		if e != nil {
			return e
		}
		if g.IsSystem {
			return invalid("the system term group holds only the keywords set")
		}
		if out, e = t.Client.TermSet.Create().SetWorkspaceID(w.ID).SetGroupID(g.ID).SetKey(in.Key).SetName(in.Name).SetDescription(in.Description).SetIsOpen(in.IsOpen).Save(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_set.create", w, map[string]any{"term_set_id": out.ID})
	})
	return
}
func (s *Service) TermSets(ctx context.Context, subject, workspaceID, groupID, after string, limit int) (Page[*ent.TermSet], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.TermSet], error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*ent.TermSet]{}, e
		}
		q := t.Client.TermSet.Query().Where(termset.WorkspaceIDEQ(workspaceID), termset.IDGT(after))
		if groupID != "" {
			q.Where(termset.GroupIDEQ(groupID))
		}
		rows, e := q.Order(ent.Asc(termset.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.TermSet) string { return v.ID }), e
	})
}
func (s *Service) TermSet(ctx context.Context, subject, id string) (*ent.TermSet, error) {
	return read(ctx, s, func(t *Service) (*ent.TermSet, error) {
		set, _, e := t.termSet(ctx, subject, id, "read")
		return set, e
	})
}
func (s *Service) termSet(ctx context.Context, subject, id, action string) (*ent.TermSet, *ent.Resource, error) {
	set, e := s.Client.TermSet.Get(ctx, id)
	if ent.IsNotFound(e) {
		return nil, nil, ErrNotFound
	}
	if e != nil {
		return nil, nil, e
	}
	w, e := s.workspace(ctx, subject, set.WorkspaceID, action)
	return set, w, e
}
func (s *Service) UpdateTermSet(ctx context.Context, subject, setID string, version int, in TermSetInput) (out *ent.TermSet, err error) {
	if e := in.validate(); e != nil {
		return nil, e
	}
	err = s.write(ctx, func(t *Service) error {
		set, w, e := t.termSet(ctx, subject, setID, "manage")
		if e != nil {
			return e
		}
		if set.Version != version {
			return ErrConflict
		}
		if in.Key != "" && in.Key != set.Key || in.GroupID != "" && in.GroupID != set.GroupID {
			return invalid("term set key and group are immutable")
		}
		if set.IsKeywords && !in.IsOpen {
			return invalid("the keywords set stays open")
		}
		if out, e = t.Client.TermSet.UpdateOne(set).SetName(in.Name).SetDescription(in.Description).SetIsOpen(in.IsOpen).AddVersion(1).Save(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_set.update", w, map[string]any{"term_set_id": setID})
	})
	return
}

// DeleteTermSet deletes a term set that has no terms and no fields. Terms are
// retained, so a set that ever had terms stays.
func (s *Service) DeleteTermSet(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		set, w, e := t.termSet(ctx, subject, id, "manage")
		if e != nil {
			return e
		}
		if set.Version != version {
			return ErrConflict
		}
		if set.IsKeywords {
			return invalid("the keywords set cannot be deleted")
		}
		used, e := t.Client.Term.Query().Where(term.TermSetIDEQ(id)).Exist(ctx)
		if e != nil {
			return e
		}
		if used {
			return invalid("term sets with terms cannot be deleted")
		}
		rows, e := t.Client.QueryContext(ctx, `SELECT 1 FROM field_definitions WHERE type='term' AND json_extract(options,'$.term_set_id')=? LIMIT 1`, id)
		if e != nil {
			return e
		}
		used = rows.Next()
		rows.Close()
		if used {
			return invalid("the term set is used by fields")
		}
		if e = t.Client.TermSet.DeleteOne(set).Exec(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "term_set.delete", w, map[string]any{"term_set_id": id})
	})
}

// TermView is a term with whether it has children, for tree browsing.
type TermView struct {
	*ent.Term
	HasChildren bool `json:"has_children"`
}

func (s *Service) termViews(ctx context.Context, rows []*ent.Term) ([]TermView, error) {
	out := make([]TermView, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	for i, v := range rows {
		ids[i] = v.ID
	}
	var parents []string
	if e := s.Client.Term.Query().Where(term.ParentIDIn(ids...), term.MergedIntoIDIsNil()).Unique(true).Select(term.FieldParentID).Scan(ctx, &parents); e != nil {
		return nil, e
	}
	has := map[string]bool{}
	for _, p := range parents {
		has[p] = true
	}
	for i, v := range rows {
		v.Edges = ent.TermEdges{}
		out[i] = TermView{Term: v, HasChildren: has[v.ID]}
	}
	return out, nil
}

// termNames names the terms among ids that subject can read; a merged term
// takes the name of the term it was merged into.
func (s *Service) termNames(ctx context.Context, subject string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := s.Client.Term.Query().Where(term.IDIn(ids...)).WithTermSet().WithMergedInto().All(ctx)
	if e != nil {
		return nil, e
	}
	readable := map[string]bool{}
	for _, v := range rows {
		ws := v.Edges.TermSet.WorkspaceID
		ok, seen := readable[ws]
		if !seen {
			_, e = s.workspace(ctx, subject, ws, "read")
			if e != nil && !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) {
				return nil, e
			}
			ok = e == nil
			readable[ws] = ok
		}
		if !ok {
			continue
		}
		out[v.ID] = v.Name
		if v.Edges.MergedInto != nil {
			out[v.ID] = v.Edges.MergedInto.Name
		}
	}
	return out, nil
}

type TermInput struct {
	Name string `json:"name"`
	// ParentID places a new term; use the move operation to change it.
	ParentID           *string           `json:"parent_id,omitempty"`
	Description        string            `json:"description"`
	Color              *string           `json:"color,omitempty"`
	SortOrder          int               `json:"sort_order"`
	Labels             map[string]string `json:"labels"`
	Synonyms           []string          `json:"synonyms"`
	Deprecated         bool              `json:"deprecated"`
	AvailableAsKeyword bool              `json:"available_as_keyword"`
}

func normalizeTerm(in *TermInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if e := validateName(in.Name); e != nil {
		return e
	}
	if e := validateDescription(in.Description); e != nil {
		return e
	}
	if in.Color != nil && !termColor.MatchString(*in.Color) {
		return invalid("color must be #rrggbb in lowercase hex")
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

// termLevels is how deep a term with path sits: 1 for a root term.
func termLevels(path string) int { return strings.Count(path, "/") - 1 }

// subtree bounds the paths strictly below path: every descendant's path
// starts with path, and '0' is the byte after '/'.
func subtree(path string) (lo, hi string) { return path, path[:len(path)-1] + "0" }
func underPath(path string) func(*entsql.Selector) {
	lo, hi := subtree(path)
	return func(sel *entsql.Selector) {
		sel.Where(entsql.And(entsql.GT(sel.C(term.FieldPath), lo), entsql.LT(sel.C(term.FieldPath), hi)))
	}
}

// insertTerm adds a term under parent (nil for a root) without access checks.
func (s *Service) insertTerm(ctx context.Context, set *ent.TermSet, parent *ent.Term, in TermInput) (*ent.Term, error) {
	id := uuid.NewString()
	path := "/" + id + "/"
	var parentID *string
	if parent != nil {
		if parent.TermSetID != set.ID || parent.Deprecated {
			return nil, invalid("parent must be active and in the same term set")
		}
		path, parentID = parent.Path+id+"/", &parent.ID
	}
	if termLevels(path) > MaxDepth {
		return nil, invalid("term hierarchy depth exceeded")
	}
	if in.AvailableAsKeyword && set.IsKeywords {
		return nil, invalid("keywords are always available as keywords")
	}
	return s.Client.Term.Create().SetID(id).SetTermSetID(set.ID).SetNillableParentID(parentID).SetPath(path).SetName(in.Name).SetNormalizedName(strings.ToLower(in.Name)).
		SetDescription(in.Description).SetNillableColor(in.Color).SetSortOrder(in.SortOrder).SetLabels(in.Labels).SetSynonyms(in.Synonyms).
		SetDeprecated(in.Deprecated).SetAvailableAsKeyword(in.AvailableAsKeyword).Save(ctx)
}

// termAction is the workspace access needed to add terms to set.
func termAction(set *ent.TermSet) string {
	if set.IsOpen {
		return "write"
	}
	return "manage"
}
func (s *Service) CreateTerm(ctx context.Context, subject, setID string, in TermInput) (out TermView, err error) {
	if e := normalizeTerm(&in); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		set, e := t.Client.TermSet.Get(ctx, setID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		action := termAction(set)
		if in.Deprecated || in.AvailableAsKeyword {
			action = "manage"
		}
		w, e := t.workspace(ctx, subject, set.WorkspaceID, action)
		if e != nil {
			return e
		}
		var parent *ent.Term
		if in.ParentID != nil {
			if parent, e = t.Client.Term.Get(ctx, *in.ParentID); ent.IsNotFound(e) {
				return invalid("unknown parent term")
			} else if e != nil {
				return e
			}
		}
		created, e := t.insertTerm(ctx, set, parent, in)
		if e != nil {
			return e
		}
		out = TermView{Term: created}
		return t.audit(ctx, subject, "term.create", w, map[string]any{"term_id": created.ID, "term_set_id": setID})
	})
	return
}

// termForChange loads a term that a manager changes; merged terms are
// read-only.
func (s *Service) termForChange(ctx context.Context, subject, id string, version int) (*ent.Term, *ent.TermSet, *ent.Resource, error) {
	v, e := s.Client.Term.Get(ctx, id)
	if ent.IsNotFound(e) {
		return nil, nil, nil, ErrNotFound
	}
	if e != nil {
		return nil, nil, nil, e
	}
	set, w, e := s.termSet(ctx, subject, v.TermSetID, "manage")
	if e != nil {
		return nil, nil, nil, e
	}
	if v.Version != version {
		return nil, nil, nil, ErrConflict
	}
	if v.MergedIntoID != nil {
		return nil, nil, nil, invalid("the term was merged into " + *v.MergedIntoID)
	}
	return v, set, w, nil
}
func (s *Service) UpdateTerm(ctx context.Context, subject, termID string, version int, in TermInput) (out TermView, err error) {
	if e := normalizeTerm(&in); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		old, set, w, e := t.termForChange(ctx, subject, termID, version)
		if e != nil {
			return e
		}
		if in.ParentID != nil && (old.ParentID == nil || *in.ParentID != *old.ParentID) {
			return invalid("move terms with POST /v1/terms/{id}/move")
		}
		if in.AvailableAsKeyword && set.IsKeywords {
			return invalid("keywords are always available as keywords")
		}
		u := t.Client.Term.UpdateOne(old).SetName(in.Name).SetNormalizedName(strings.ToLower(in.Name)).SetDescription(in.Description).
			SetSortOrder(in.SortOrder).SetLabels(in.Labels).SetSynonyms(in.Synonyms).SetDeprecated(in.Deprecated).SetAvailableAsKeyword(in.AvailableAsKeyword).AddVersion(1)
		if in.Color == nil {
			u.ClearColor()
		} else {
			u.SetColor(*in.Color)
		}
		updated, e := u.Save(ctx)
		if e != nil {
			return e
		}
		views, e := t.termViews(ctx, []*ent.Term{updated})
		if e != nil {
			return e
		}
		out = views[0]
		return t.audit(ctx, subject, "term.update", w, map[string]any{"term_id": termID, "deprecated": in.Deprecated})
	})
	return
}
func (s *Service) Term(ctx context.Context, subject, id string) (TermView, error) {
	return read(ctx, s, func(t *Service) (TermView, error) {
		v, e := t.Client.Term.Get(ctx, id)
		if ent.IsNotFound(e) {
			return TermView{}, ErrNotFound
		}
		if e != nil {
			return TermView{}, e
		}
		if _, _, e = t.termSet(ctx, subject, v.TermSetID, "read"); e != nil {
			return TermView{}, e
		}
		views, e := t.termViews(ctx, []*ent.Term{v})
		if e != nil {
			return TermView{}, e
		}
		return views[0], nil
	})
}

// TermsByID returns the readable terms among ids, in the order asked;
// unknown ids and terms of workspaces the caller cannot read are left out.
// Merged terms come back too: merged_into_id names the term to show.
func (s *Service) TermsByID(ctx context.Context, subject string, ids []string) ([]TermView, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, invalid("ask for 1 to 200 term ids")
	}
	return read(ctx, s, func(t *Service) ([]TermView, error) {
		rows, e := t.Client.Term.Query().Where(term.IDIn(ids...)).WithTermSet().All(ctx)
		if e != nil {
			return nil, e
		}
		readable := map[string]bool{}
		byID := map[string]*ent.Term{}
		for _, v := range rows {
			ws := v.Edges.TermSet.WorkspaceID
			ok, seen := readable[ws]
			if !seen {
				_, e = t.workspace(ctx, subject, ws, "read")
				if e != nil && !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) {
					return nil, e
				}
				ok = e == nil
				readable[ws] = ok
			}
			if ok {
				byID[v.ID] = v
			}
		}
		ordered := []*ent.Term{}
		for _, id := range ids {
			if v := byID[id]; v != nil {
				ordered = append(ordered, v)
				delete(byID, id)
			}
		}
		return t.termViews(ctx, ordered)
	})
}

type TermsQuery struct {
	// ParentID lists that term's children; empty lists the root terms. With
	// Search, it narrows the search to the term's descendants.
	ParentID string
	// Search matches names, labels and synonyms anywhere in the set.
	Search            string
	IncludeDeprecated bool
	After             string
	Limit             int
}
type termCursor struct {
	SortOrder int    `json:"s"`
	Name      string `json:"n"`
	ID        string `json:"i"`
}

// Terms lists terms ordered by sort order, then name. Merged terms are never
// listed.
func (s *Service) Terms(ctx context.Context, subject, setID string, in TermsQuery) (Page[TermView], error) {
	if len(in.Search) > maxTermSearch {
		return Page[TermView]{}, invalid("term search exceeds 255 bytes")
	}
	var after *termCursor
	if in.After != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.After)
		after = &termCursor{}
		if e != nil || json.Unmarshal(raw, after) != nil {
			return Page[TermView]{}, invalid("invalid cursor")
		}
	}
	return read(ctx, s, func(t *Service) (Page[TermView], error) {
		if _, _, e := t.termSet(ctx, subject, setID, "read"); e != nil {
			return Page[TermView]{}, e
		}
		q := t.Client.Term.Query().Where(term.TermSetIDEQ(setID), term.MergedIntoIDIsNil())
		if !in.IncludeDeprecated {
			q.Where(term.DeprecatedEQ(false))
		}
		var parent *ent.Term
		if in.ParentID != "" {
			p, e := t.Client.Term.Query().Where(term.IDEQ(in.ParentID), term.TermSetIDEQ(setID)).Only(ctx)
			if ent.IsNotFound(e) {
				return Page[TermView]{}, invalid("parent_id must identify a term of the set")
			}
			if e != nil {
				return Page[TermView]{}, e
			}
			parent = p
		}
		if in.Search != "" {
			needle := strings.ToLower(in.Search)
			q.Where(func(sel *entsql.Selector) {
				// unicode_lower is registered by internal/database; SQLite's lower() folds ASCII only.
				sel.Where(entsql.ExprP("(instr(normalized_name,?)>0 OR EXISTS(SELECT 1 FROM json_each(synonyms) WHERE instr(unicode_lower(value),?)>0) OR EXISTS(SELECT 1 FROM json_each(labels) WHERE instr(unicode_lower(value),?)>0))", needle, needle, needle))
			})
			if parent != nil {
				q.Where(underPath(parent.Path))
			}
		} else if parent != nil {
			q.Where(term.ParentIDEQ(parent.ID))
		} else {
			q.Where(term.ParentIDIsNil())
		}
		if after != nil {
			q.Where(func(sel *entsql.Selector) {
				sel.Where(entsql.ExprP("(sort_order,normalized_name,id)>(?,?,?)", after.SortOrder, after.Name, after.ID))
			})
		}
		size := pageSize(in.Limit)
		rows, e := q.Order(ent.Asc(term.FieldSortOrder), ent.Asc(term.FieldNormalizedName), ent.Asc(term.FieldID)).Limit(size + 1).All(ctx)
		if e != nil {
			return Page[TermView]{}, e
		}
		out := Page[TermView]{}
		if len(rows) > size {
			last := rows[size-1]
			raw, _ := json.Marshal(termCursor{last.SortOrder, last.NormalizedName, last.ID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
			rows = rows[:size]
		}
		out.Data, e = t.termViews(ctx, rows)
		return out, e
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
