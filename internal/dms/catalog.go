package dms

import (
	"context"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
)

func (s *Service) Template(ctx context.Context, subject, id string) (*catalog.Template, error) {
	return read(ctx, s, func(t *Service) (*catalog.Template, error) {
		v, e := t.DAL.Catalog().Template(ctx, id)
		if dberr.IsNotFound(e) {
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
