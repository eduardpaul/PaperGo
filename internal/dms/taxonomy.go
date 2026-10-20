package dms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
)

// The organization term store is shared by all workspaces. Open sets accept
// contributions with taxonomy.read; curation requires taxonomy.manage.

const maxTermSearch = 255

var termColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func validateDescription(s string) error {
	if !utf8.ValidString(s) || len(s) > 4096 {
		return invalid("description exceeds 4096 bytes")
	}
	return nil
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

// TermGroupView is a term group as the API returns it.
type TermGroupView struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"is_system"`
}

func viewTermGroup(g taxonomy.Group) TermGroupView {
	return TermGroupView{ID: g.ID, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, Version: g.Version, Name: g.Name, Description: g.Description, IsSystem: g.System}
}

// TermSetView is a term set as the API returns it.
type TermSetView struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int       `json:"version"`
	GroupID     string    `json:"group_id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsOpen      bool      `json:"is_open"`
	IsKeywords  bool      `json:"is_keywords"`
}

func viewTermSet(set taxonomy.Set) TermSetView {
	return TermSetView{ID: set.ID, CreatedAt: set.CreatedAt, UpdatedAt: set.UpdatedAt, Version: set.Version, GroupID: set.GroupID, Key: set.Key,
		Name: set.Name, Description: set.Description, IsOpen: set.Open, IsKeywords: set.Keywords}
}

// taxonomyError maps term store rules to service errors.
func taxonomyError(err error) error {
	switch {
	case taxonomy.IsViolation(err):
		return invalid(err.Error())
	case errors.Is(err, dberr.ErrConcurrentUpdate):
		return ErrPreconditionFailed
	case dberr.IsNotFound(err):
		return ErrNotFound
	}
	return err
}

func (s *Service) CreateTermGroup(ctx context.Context, subject string, in TermGroupInput) (out TermGroupView, err error) {
	if e := in.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		if e := t.requireTaxonomy(ctx, subject, "manage"); e != nil {
			return e
		}
		g, e := t.DAL.Taxonomy().CreateGroup(ctx, taxonomy.Group{Name: in.Name, Description: in.Description})
		if e != nil {
			return e
		}
		out = viewTermGroup(g)
		return t.auditTaxonomy(ctx, subject, "term_group.create", map[string]any{"term_group_id": g.ID})
	})
	return
}
func (s *Service) TermGroups(ctx context.Context, subject, after string, limit int) (Page[TermGroupView], error) {
	return read(ctx, s, func(t *Service) (Page[TermGroupView], error) {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return Page[TermGroupView]{}, e
		}
		rows, e := t.DAL.Taxonomy().Groups(ctx, taxonomy.GroupFilter{AfterID: after, Limit: pageSize(limit) + 1})
		return entityPage(views(rows, viewTermGroup), limit, func(v TermGroupView) string { return v.ID }), e
	})
}
func (s *Service) TermGroup(ctx context.Context, subject, id string) (TermGroupView, error) {
	return read(ctx, s, func(t *Service) (TermGroupView, error) {
		g, e := t.termGroup(ctx, subject, id, "read")
		return viewTermGroup(g), e
	})
}
func (s *Service) termGroup(ctx context.Context, subject, id, action string) (taxonomy.Group, error) {
	g, e := s.DAL.Taxonomy().Group(ctx, id)
	if e != nil {
		return g, taxonomyError(e)
	}
	return g, s.requireTaxonomy(ctx, subject, action)
}
func (s *Service) UpdateTermGroup(ctx context.Context, subject, id string, version int, in TermGroupInput) (out TermGroupView, err error) {
	if e := in.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		if _, e := t.termGroup(ctx, subject, id, "manage"); e != nil {
			return e
		}
		g, e := t.DAL.Taxonomy().SaveGroup(ctx, taxonomy.Group{ID: id, Name: in.Name, Description: in.Description}, version)
		if e != nil {
			return taxonomyError(e)
		}
		out = viewTermGroup(g)
		return t.auditTaxonomy(ctx, subject, "term_group.update", map[string]any{"term_group_id": id})
	})
	return
}

// DeleteTermGroup deletes an empty group other than the system group.
func (s *Service) DeleteTermGroup(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		if _, e := t.termGroup(ctx, subject, id, "manage"); e != nil {
			return e
		}
		if e := t.DAL.Taxonomy().DeleteGroup(ctx, id, version); e != nil {
			return taxonomyError(e)
		}
		return t.auditTaxonomy(ctx, subject, "term_group.delete", map[string]any{"term_group_id": id})
	})
}

type TermSetInput struct {
	GroupID     string `json:"group_id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// IsOpen lets people with read access add terms; closed sets change
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
func (s *Service) CreateTermSet(ctx context.Context, subject string, in TermSetInput) (out TermSetView, err error) {
	if !fieldKey.MatchString(in.Key) {
		return out, invalid("invalid term set key")
	}
	if e := in.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		if e := t.requireTaxonomy(ctx, subject, "manage"); e != nil {
			return e
		}
		set, e := t.DAL.Taxonomy().CreateSet(ctx, taxonomy.Set{GroupID: in.GroupID, Key: in.Key, Name: in.Name, Description: in.Description, Open: in.IsOpen})
		if e != nil {
			return taxonomyError(e)
		}
		out = viewTermSet(set)
		return t.auditTaxonomy(ctx, subject, "term_set.create", map[string]any{"term_set_id": set.ID})
	})
	return
}
func (s *Service) TermSets(ctx context.Context, subject, groupID, after string, limit int) (Page[TermSetView], error) {
	return read(ctx, s, func(t *Service) (Page[TermSetView], error) {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return Page[TermSetView]{}, e
		}
		rows, e := t.DAL.Taxonomy().Sets(ctx, taxonomy.SetFilter{GroupID: groupID, AfterID: after, Limit: pageSize(limit) + 1})
		return entityPage(views(rows, viewTermSet), limit, func(v TermSetView) string { return v.ID }), e
	})
}
func (s *Service) TermSet(ctx context.Context, subject, id string) (TermSetView, error) {
	return read(ctx, s, func(t *Service) (TermSetView, error) {
		set, e := t.termSet(ctx, subject, id, "read")
		return viewTermSet(set), e
	})
}
func (s *Service) termSet(ctx context.Context, subject, id, action string) (taxonomy.Set, error) {
	set, e := s.DAL.Taxonomy().Set(ctx, id)
	if e != nil {
		return set, taxonomyError(e)
	}
	return set, s.requireTaxonomy(ctx, subject, action)
}
func (s *Service) UpdateTermSet(ctx context.Context, subject, setID string, version int, in TermSetInput) (out TermSetView, err error) {
	if e := in.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		if _, e := t.termSet(ctx, subject, setID, "manage"); e != nil {
			return e
		}
		set, e := t.DAL.Taxonomy().SaveSet(ctx, taxonomy.Set{ID: setID, GroupID: in.GroupID, Key: in.Key, Name: in.Name, Description: in.Description, Open: in.IsOpen}, version)
		if e != nil {
			return taxonomyError(e)
		}
		out = viewTermSet(set)
		return t.auditTaxonomy(ctx, subject, "term_set.update", map[string]any{"term_set_id": setID})
	})
	return
}

// DeleteTermSet deletes a term set that has no terms and no fields. Terms are
// retained, so a set that ever had terms stays.
func (s *Service) DeleteTermSet(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		if _, e := t.termSet(ctx, subject, id, "manage"); e != nil {
			return e
		}
		if e := t.DAL.Taxonomy().DeleteSet(ctx, id, version); e != nil {
			return taxonomyError(e)
		}
		return t.auditTaxonomy(ctx, subject, "term_set.delete", map[string]any{"term_set_id": id})
	})
}

// TermView is a term as the API returns it, with whether it has children
// for tree browsing.
type TermView struct {
	ID                 string            `json:"id"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	Version            int               `json:"version"`
	TermSetID          string            `json:"term_set_id"`
	ParentID           *string           `json:"parent_id"`
	Name               string            `json:"name"`
	NormalizedName     string            `json:"normalized_name"`
	Description        string            `json:"description"`
	Color              *string           `json:"color"`
	SortOrder          int               `json:"sort_order"`
	Path               string            `json:"path"`
	Labels             map[string]string `json:"labels"`
	Synonyms           []string          `json:"synonyms"`
	MergedIntoID       *string           `json:"merged_into_id"`
	AvailableAsKeyword bool              `json:"available_as_keyword"`
	Deprecated         bool              `json:"deprecated"`
	HasChildren        bool              `json:"has_children"`
}

func viewTerm(v taxonomy.Term) TermView {
	return TermView{ID: v.ID, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version, TermSetID: v.SetID, ParentID: optional(v.ParentID),
		Name: v.Name, NormalizedName: v.NormalizedName, Description: v.Description, Color: optional(v.Color), SortOrder: v.SortOrder, Path: v.Path,
		Labels: v.Labels, Synonyms: v.Synonyms, MergedIntoID: optional(v.MergedIntoID), AvailableAsKeyword: v.AvailableAsKeyword, Deprecated: v.Deprecated}
}

// optional is nil for an empty string.
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func views[T, V any](rows []T, view func(T) V) []V {
	out := make([]V, len(rows))
	for i, row := range rows {
		out[i] = view(row)
	}
	return out
}

func (s *Service) termViews(ctx context.Context, rows []taxonomy.Term) ([]TermView, error) {
	ids := make([]string, len(rows))
	for i, v := range rows {
		ids[i] = v.ID
	}
	has, e := s.DAL.Taxonomy().Parents(ctx, ids)
	if e != nil {
		return nil, e
	}
	out := make([]TermView, len(rows))
	for i, v := range rows {
		out[i] = viewTerm(v)
		out[i].HasChildren = has[v.ID]
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
	rows, e := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: ids})
	if e != nil {
		return nil, e
	}
	if e := s.requireTaxonomy(ctx, subject, "read"); e != nil {
		if errors.Is(e, ErrForbidden) {
			return out, nil
		}
		return nil, e
	}
	merged := []string{}
	for _, v := range rows {
		out[v.ID] = v.Name
		if v.Merged() {
			merged = append(merged, v.MergedIntoID)
		}
	}
	if len(merged) == 0 {
		return out, nil
	}
	targets, e := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: merged})
	if e != nil {
		return nil, e
	}
	names := map[string]string{}
	for _, v := range targets {
		names[v.ID] = v.Name
	}
	for _, v := range rows {
		if v.Merged() {
			out[v.ID] = names[v.MergedIntoID]
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

// termRecord is the term in carries, for the term store.
func termRecord(in TermInput) taxonomy.Term {
	t := taxonomy.Term{Name: in.Name, Description: in.Description, SortOrder: in.SortOrder, Labels: in.Labels, Synonyms: in.Synonyms,
		Deprecated: in.Deprecated, AvailableAsKeyword: in.AvailableAsKeyword}
	if in.ParentID != nil {
		t.ParentID = *in.ParentID
	}
	if in.Color != nil {
		t.Color = *in.Color
	}
	return t
}

// insertTerm adds a term to set without access checks.
func (s *Service) insertTerm(ctx context.Context, setID string, in TermInput) (taxonomy.Term, error) {
	t := termRecord(in)
	t.SetID = setID
	created, err := s.DAL.Taxonomy().AddTerm(ctx, t)
	return created, taxonomyError(err)
}

// termAction is the taxonomy scope needed to add terms to set.
func termAction(set taxonomy.Set) string {
	if set.Open {
		return "read"
	}
	return "manage"
}
func (s *Service) CreateTerm(ctx context.Context, subject, setID string, in TermInput) (out TermView, err error) {
	if e := normalizeTerm(&in); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return e
		}
		set, e := t.DAL.Taxonomy().Set(ctx, setID)
		if e != nil {
			return taxonomyError(e)
		}
		action := termAction(set)
		if in.Deprecated || in.AvailableAsKeyword {
			action = "manage"
		}
		if e := t.requireTaxonomy(ctx, subject, action); e != nil {
			return e
		}
		created, e := t.insertTerm(ctx, setID, in)
		if e != nil {
			return e
		}
		out = viewTerm(created)
		return t.auditTaxonomy(ctx, subject, "term.create", map[string]any{"term_id": created.ID, "term_set_id": setID})
	})
	return
}

// termForChange loads a term that a manager changes; merged terms are
// read-only.
func (s *Service) termForChange(ctx context.Context, subject, id string, version int) (taxonomy.Term, taxonomy.Set, error) {
	v, e := s.DAL.Taxonomy().Term(ctx, id)
	if e != nil {
		return v, taxonomy.Set{}, taxonomyError(e)
	}
	set, e := s.termSet(ctx, subject, v.SetID, "manage")
	if e != nil {
		return v, set, e
	}
	if v.Version != version {
		return v, set, ErrPreconditionFailed
	}
	if v.Merged() {
		return v, set, invalid("the term was merged into " + v.MergedIntoID)
	}
	return v, set, nil
}
func (s *Service) UpdateTerm(ctx context.Context, subject, termID string, version int, in TermInput) (out TermView, err error) {
	if e := normalizeTerm(&in); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		old, _, e := t.termForChange(ctx, subject, termID, version)
		if e != nil {
			return e
		}
		if in.ParentID != nil && *in.ParentID != old.ParentID {
			return invalid("move terms with POST /v1/terms/{id}/move")
		}
		change := termRecord(in)
		change.ID = termID
		updated, e := t.DAL.Taxonomy().SaveTerm(ctx, change, version)
		if e != nil {
			return taxonomyError(e)
		}
		views, e := t.termViews(ctx, []taxonomy.Term{updated})
		if e != nil {
			return e
		}
		out = views[0]
		if e = t.emitTermChanged(ctx, subject, old, updated); e != nil {
			return e
		}
		return t.auditTaxonomy(ctx, subject, "term.update", map[string]any{"term_id": termID, "deprecated": in.Deprecated})
	})
	return
}
func (s *Service) Term(ctx context.Context, subject, id string) (TermView, error) {
	return read(ctx, s, func(t *Service) (TermView, error) {
		v, e := t.DAL.Taxonomy().Term(ctx, id)
		if e != nil {
			return TermView{}, taxonomyError(e)
		}
		if _, e = t.termSet(ctx, subject, v.SetID, "read"); e != nil {
			return TermView{}, e
		}
		views, e := t.termViews(ctx, []taxonomy.Term{v})
		if e != nil {
			return TermView{}, e
		}
		return views[0], nil
	})
}

// TermsByID returns the readable terms among ids, in the order asked;
// unknown IDs are left out; taxonomy.read authorizes the organization store.
// Merged terms come back too: merged_into_id names the term to show.
func (s *Service) TermsByID(ctx context.Context, subject string, ids []string) ([]TermView, error) {
	if len(ids) < 1 || len(ids) > 200 {
		return nil, invalid("ask for 1 to 200 term ids")
	}
	return read(ctx, s, func(t *Service) ([]TermView, error) {
		rows, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: ids})
		if e != nil {
			return nil, e
		}
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return nil, e
		}
		byID := map[string]taxonomy.Term{}
		for _, v := range rows {
			byID[v.ID] = v
		}
		ordered := []taxonomy.Term{}
		for _, id := range ids {
			if v, ok := byID[id]; ok {
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
	f := taxonomy.TermFilter{SetID: setID, Unmerged: true, Current: !in.IncludeDeprecated, Limit: pageSize(in.Limit) + 1}
	if in.After != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.After)
		var after termCursor
		if e != nil || json.Unmarshal(raw, &after) != nil {
			return Page[TermView]{}, invalid("invalid cursor")
		}
		f.After = &taxonomy.Cursor{SortOrder: after.SortOrder, Name: after.Name, ID: after.ID}
	}
	return read(ctx, s, func(t *Service) (Page[TermView], error) {
		if _, e := t.termSet(ctx, subject, setID, "read"); e != nil {
			return Page[TermView]{}, e
		}
		var parent *taxonomy.Term
		if in.ParentID != "" {
			p, e := t.DAL.Taxonomy().Term(ctx, in.ParentID)
			if dberr.IsNotFound(e) || e == nil && p.SetID != setID {
				return Page[TermView]{}, invalid("parent_id must identify a term of the set")
			}
			if e != nil {
				return Page[TermView]{}, e
			}
			parent = &p
		}
		switch {
		case in.Search != "":
			f.Search = strings.ToLower(in.Search)
			if parent != nil {
				f.Under = parent.Path
			}
		case parent != nil:
			f.ParentID = parent.ID
		default:
			f.Roots = true
		}
		rows, e := t.DAL.Taxonomy().Terms(ctx, f)
		if e != nil {
			return Page[TermView]{}, e
		}
		out := Page[TermView]{}
		if size := pageSize(in.Limit); len(rows) > size {
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
