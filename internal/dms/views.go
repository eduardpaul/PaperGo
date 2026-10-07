package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/listview"
)

type ViewInput struct {
	Name      string    `json:"name"`
	Columns   []string  `json:"columns"`
	Query     QuerySpec `json:"query"`
	Layout    string    `json:"layout"`
	IsDefault bool      `json:"is_default"`
}

func (s *Service) validateView(ctx context.Context, subject, containerID string, in *ViewInput) error {
	if e := validateName(in.Name); e != nil {
		return e
	}
	if in.Layout == "" {
		in.Layout = "table"
	}
	if in.Layout != "table" && in.Layout != "board" && in.Layout != "calendar" && in.Layout != "gallery" {
		return invalid("invalid view layout")
	}
	if len(in.Columns) > 50 {
		return invalid("at most 50 view columns")
	}
	if len(in.Columns) == 0 {
		in.Columns = []string{"$name"}
	}
	defs, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(containerID)).All(ctx)
	if e != nil {
		return e
	}
	known := map[string]bool{"$id": true, "$name": true, "$tags": true}
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
func (s *Service) CreateView(ctx context.Context, subject, containerID string, in ViewInput) (out *ent.ListView, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if e = t.validateView(ctx, subject, containerID, &in); e != nil {
			return e
		}
		raw, _ := json.Marshal(in.Query)
		if in.IsDefault {
			if _, e = t.Client.ListView.Update().Where(listview.ContainerIDEQ(containerID), listview.IsDefaultEQ(true)).SetIsDefault(false).AddVersion(1).Save(ctx); e != nil {
				return e
			}
		}
		out, e = t.Client.ListView.Create().SetContainerID(containerID).SetName(in.Name).SetColumns(in.Columns).SetQuery(raw).SetLayout(in.Layout).SetIsDefault(in.IsDefault).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "view.create", c, map[string]any{"view_id": out.ID})
	})
	return
}
func (s *Service) Views(ctx context.Context, subject, containerID, after string, limit int) (Page[*ent.ListView], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.ListView], error) {
		if _, e := t.authorize(ctx, subject, containerID, "read"); e != nil {
			return Page[*ent.ListView]{}, e
		}
		rows, e := t.Client.ListView.Query().Where(listview.ContainerIDEQ(containerID), listview.IDGT(after)).Order(ent.Asc(listview.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.ListView) string { return v.ID }), e
	})
}
func (s *Service) View(ctx context.Context, subject, id string) (*ent.ListView, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.ListView, error) { return t.View(ctx, subject, id) })
	}
	v, e := s.Client.ListView.Get(ctx, id)
	if ent.IsNotFound(e) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if _, e = s.authorize(ctx, subject, v.ContainerID, "read"); e != nil {
		return nil, e
	}
	return v, nil
}
func (s *Service) UpdateView(ctx context.Context, subject, id string, version int, in ViewInput) (out *ent.ListView, err error) {
	err = s.write(ctx, func(t *Service) error {
		v, e := t.Client.ListView.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, v.ContainerID, "manage")
		if e != nil {
			return e
		}
		if v.Version != version {
			return ErrConflict
		}
		if e = t.validateView(ctx, subject, v.ContainerID, &in); e != nil {
			return e
		}
		raw, _ := json.Marshal(in.Query)
		if in.IsDefault {
			if _, e = t.Client.ListView.Update().Where(listview.ContainerIDEQ(v.ContainerID), listview.IsDefaultEQ(true), listview.IDNEQ(id)).SetIsDefault(false).AddVersion(1).Save(ctx); e != nil {
				return e
			}
		}
		out, e = t.Client.ListView.UpdateOne(v).SetName(in.Name).SetColumns(in.Columns).SetQuery(raw).SetLayout(in.Layout).SetIsDefault(in.IsDefault).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "view.update", c, map[string]any{"view_id": id})
	})
	return
}
func (s *Service) DeleteView(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		v, e := t.Client.ListView.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, v.ContainerID, "manage")
		if e != nil {
			return e
		}
		if v.Version != version {
			return ErrConflict
		}
		if e = t.Client.ListView.DeleteOne(v).Exec(ctx); e != nil {
			return e
		}
		return t.audit(ctx, subject, "view.delete", c, map[string]any{"view_id": id})
	})
}

type ViewQueryRequest struct {
	Surface string `json:"surface,omitempty"`
	After   string `json:"after,omitempty"`
	Limit   int    `json:"limit,omitempty"`
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
		out, e := t.Query(ctx, subject, v.ContainerID, QueryRequest{Query: spec, Surface: in.Surface, After: in.After, Limit: in.Limit})
		if e != nil {
			return out, e
		}
		selected := map[string]bool{}
		for _, column := range v.Columns {
			selected[column] = true
		}
		for _, r := range out.Data {
			values := map[string]any{}
			for k, x := range r.Values {
				if selected[k] {
					values[k] = x
				}
			}
			r.Values = values
			if !selected["$tags"] {
				r.Tags = []string{}
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
		return t.QueryGroups(ctx, subject, v.ContainerID, QueryRequest{Query: spec, Surface: in.Surface, After: in.After, Limit: in.Limit})
	})
}
