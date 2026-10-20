package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"

	"github.com/google/uuid"
)

// CreateWithStatus reports whether this request inserted a resource. A caller
// ID makes a repeated item/folder create an unchanged read of the original.
func (s *Service) CreateWithStatus(ctx context.Context, subject, parentID string, in CreateResource) (out *resources.Resource, created bool, err error) {
	err = s.write(ctx, func(t *Service) error {
		if in.ID != nil {
			if in.Kind != "item" && in.Kind != "folder" {
				return invalid("caller IDs belong to items and folders")
			}
			id, e := uuid.Parse(*in.ID)
			if e != nil || id == uuid.Nil {
				return invalid("id must be a non-empty UUID")
			}
			canonical := id.String()
			in.ID = &canonical
			if e = t.RequireScope(ctx, subject, "list.write"); e != nil {
				return e
			}
			existing, e := t.DAL.Resources().Resource(ctx, canonical)
			if e != nil && !dberr.IsNotFound(e) {
				return e
			}
			if existing != nil {
				parent, e := t.DAL.Resources().Resource(ctx, parentID)
				if e != nil || parent.DeletedAt != nil {
					if e != nil && !dberr.IsNotFound(e) {
						return e
					}
					return ErrConflict
				}
				collection := parent.ID
				if parent.Kind == refs.KindFolder && parent.ContainerID != nil {
					collection = *parent.ContainerID
				} else if parent.Kind != refs.KindList && parent.Kind != refs.KindLibrary {
					return ErrConflict
				}
				if existing.DeletedAt != nil || existing.ContainerID == nil || *existing.ContainerID != collection || (existing.Kind != refs.KindItem && existing.Kind != refs.KindFolder) {
					return ErrConflict
				}
				out, e = t.Get(ctx, subject, existing.ID)
				if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
					return ErrConflict
				}
				return e
			}
		}
		var e error
		out, e = t.create(ctx, subject, parentID, in, nil)
		created = e == nil
		return e
	})
	return
}
