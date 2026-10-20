package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	viewstore "papergo/internal/dal/views"
)

type ViewInput struct {
	Name      string           `json:"name"`
	Columns   []string         `json:"columns"`
	Query     QuerySpec        `json:"query"`
	Layout    viewstore.Layout `json:"layout"`
	IsDefault bool             `json:"is_default"`
}

func (s *Service) validateView(ctx context.Context, subject, containerID string, in *ViewInput) error {
	if e := validateName(in.Name); e != nil {
		return e
	}
	if in.Layout == "" {
		in.Layout = viewstore.LayoutTable
	}
	if !in.Layout.Valid() {
		return invalid("invalid view layout")
	}
	if len(in.Columns) > 50 {
		return invalid("at most 50 view columns")
	}
	if len(in.Columns) == 0 {
		in.Columns = []string{"$name"}
	}
	defs, e := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: containerID})
	if e != nil {
		return e
	}
	known := map[string]bool{"$id": true, "$name": true, "$keywords": true, "$created_at": true, "$created_by": true, "$modified_at": true, "$modified_by": true}
	for _, d := range defs {
		known[d.Key] = true
	}
	seen := map[string]bool{}
	for _, col := range in.Columns {
		if !known[col] || seen[col] {
			return invalid("view columns must be unique known fields")
		}
		seen[col] = true
	}
	return s.validateQuery(ctx, subject, containerID, in.Query)
}
func (s *Service) CreateView(ctx context.Context, subject, containerID string, in ViewInput) (out *viewstore.ListView, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if e = t.validateView(ctx, subject, containerID, &in); e != nil {
			return e
		}
		raw, _ := json.Marshal(in.Query)
		v, e := t.DAL.Views().CreateListView(ctx, viewstore.ListView{ContainerID: containerID, Name: in.Name, Columns: in.Columns, Query: raw, Layout: in.Layout, IsDefault: in.IsDefault})
		if e != nil {
			return e
		}
		out = &v
		return t.audit(ctx, subject, "view.create", c, map[string]any{"view_id": out.ID})
	})
	return
}
func (s *Service) Views(ctx context.Context, subject, containerID, after string, limit int) (Page[viewstore.ListView], error) {
	return read(ctx, s, func(t *Service) (Page[viewstore.ListView], error) {
		if _, e := t.authorize(ctx, subject, containerID, "read"); e != nil {
			return Page[viewstore.ListView]{}, e
		}
		rows, e := t.DAL.Views().ListViews(ctx, viewstore.ViewFilter{ContainerID: containerID, AfterID: after, Limit: pageSize(limit) + 1})
		return entityPage(rows, limit, func(v viewstore.ListView) string { return v.ID }), e
	})
}
func (s *Service) View(ctx context.Context, subject, id string) (*viewstore.ListView, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*viewstore.ListView, error) { return t.View(ctx, subject, id) })
	}
	v, e := s.listView(ctx, id)
	if e != nil {
		return nil, e
	}
	if _, e = s.authorize(ctx, subject, v.ContainerID, "read"); e != nil {
		return nil, e
	}
	return v, nil
}

func (s *Service) listView(ctx context.Context, id string) (*viewstore.ListView, error) {
	v, e := s.DAL.Views().ListView(ctx, id)
	if dberr.IsNotFound(e) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	return &v, nil
}

func (s *Service) UpdateView(ctx context.Context, subject, id string, version int, in ViewInput) (out *viewstore.ListView, err error) {
	err = s.write(ctx, func(t *Service) error {
		v, e := t.listView(ctx, id)
		if e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, v.ContainerID, "manage")
		if e != nil {
			return e
		}
		if v.Version != version {
			return ErrPreconditionFailed
		}
		if e = t.validateView(ctx, subject, v.ContainerID, &in); e != nil {
			return e
		}
		raw, _ := json.Marshal(in.Query)
		before, e := json.Marshal(v)
		if e != nil {
			return e
		}
		updated, e := t.DAL.Views().UpdateListView(ctx, viewstore.ListView{ID: id, Name: in.Name, Columns: in.Columns, Query: raw, Layout: in.Layout, IsDefault: in.IsDefault}, version)
		if e != nil {
			return e
		}
		out = &updated
		if e = t.trackAuditProperties(id, before, out, "name", "columns", "query", "layout", "is_default"); e != nil {
			return e
		}
		return t.audit(ctx, subject, "view.update", c, map[string]any{"view_id": id})
	})
	return
}
func (s *Service) DeleteView(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		v, e := t.listView(ctx, id)
		if e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, v.ContainerID, "manage")
		if e != nil {
			return e
		}
		if v.Version != version {
			return ErrPreconditionFailed
		}
		if e = t.DAL.Views().DeleteListView(ctx, id, version); e != nil {
			return e
		}
		return t.audit(ctx, subject, "view.delete", c, map[string]any{"view_id": id})
	})
}

type ViewQueryRequest struct {
	Select       []string `json:"select,omitempty"`
	Surface      string   `json:"surface,omitempty"`
	After        string   `json:"-"`
	Limit        int      `json:"-"`
	IncludeTotal bool     `json:"include_total,omitempty"`
	TotalLimit   int      `json:"total_limit,omitempty"`
}

func (s *Service) QueryView(ctx context.Context, subject, id string, in ViewQueryRequest) (QueryResult, error) {
	return read(ctx, s, func(t *Service) (QueryResult, error) {
		v, e := t.View(ctx, subject, id)
		if e != nil {
			return QueryResult{}, e
		}
		var spec QuerySpec
		if e = json.Unmarshal(v.Query, &spec); e != nil {
			return QueryResult{}, e
		}
		selection := in.Select
		if selection == nil {
			selection = v.Columns
		}
		request := QueryRequest{Query: spec, Surface: in.Surface, After: in.After, Limit: in.Limit, IncludeTotal: in.IncludeTotal, TotalLimit: in.TotalLimit, Select: selection}
		if request.Surface == "" {
			request.Surface = "auto"
		}
		q, e := t.compileQuery(ctx, subject, v.ContainerID, request, false)
		if e != nil {
			return QueryResult{}, e
		}
		// Rows carry only the view's columns; detail reads return full content.
		out, e := t.queryCompiled(ctx, subject, request, q, q.projection)
		if e != nil {
			return out, e
		}
		if !q.includeKeywords {
			for _, r := range out.Data {
				r.Keywords = []string{}
			}
		}
		return out, nil
	})
}
func (s *Service) QueryViewGroups(ctx context.Context, subject, id string, in ViewQueryRequest) (Page[QueryGroup], error) {
	return read(ctx, s, func(t *Service) (Page[QueryGroup], error) {
		v, e := t.View(ctx, subject, id)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		var spec QuerySpec
		if e = json.Unmarshal(v.Query, &spec); e != nil {
			return Page[QueryGroup]{}, e
		}
		return t.QueryGroups(ctx, subject, v.ContainerID, QueryRequest{Query: spec, Surface: in.Surface, After: in.After, Limit: in.Limit, Select: in.Select})
	})
}
