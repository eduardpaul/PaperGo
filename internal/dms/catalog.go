package dms

import (
	"context"
	"papergo/ent"
)

func (s *Service) Template(ctx context.Context, subject, id string) (*ent.SchemaTemplate, error) {
	return read(ctx, s, func(t *Service) (*ent.SchemaTemplate, error) {
		v, e := t.Client.SchemaTemplate.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if _, e = t.schemaWorkspace(ctx, subject, v.WorkspaceID, "read"); e != nil {
			return nil, e
		}
		return v, nil
	})
}
