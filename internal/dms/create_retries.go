package dms

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/resource"
)

// CreateWithStatus reports whether this request inserted a resource. A caller
// ID makes a repeated item/folder create an unchanged read of the original.
func (s *Service) CreateWithStatus(ctx context.Context, subject, parentID string, in CreateResource) (out *ent.Resource, created bool, err error) {
	err = s.write(ctx, func(t *Service) error {
		if in.ID != "" {
			if in.Kind != "item" && in.Kind != "folder" {
				return invalid("caller IDs belong to items and folders")
			}
			id, e := uuid.Parse(in.ID)
			if e != nil || id == uuid.Nil {
				return invalid("id must be a non-empty UUID")
			}
			in.ID = id.String()
			if e = t.RequireScope(ctx, subject, "list.write"); e != nil {
				return e
			}
			existing, e := t.Client.Resource.Get(ctx, in.ID)
			if e != nil && !ent.IsNotFound(e) {
				return e
			}
			if existing != nil {
				parent, e := t.Client.Resource.Get(ctx, parentID)
				if e != nil || parent.DeletedAt != nil {
					if e != nil && !ent.IsNotFound(e) {
						return e
					}
					return ErrConflict
				}
				collection := parent.ID
				if parent.Kind == resource.KindFolder && parent.ContainerID != nil {
					collection = *parent.ContainerID
				} else if parent.Kind != resource.KindList && parent.Kind != resource.KindLibrary {
					return ErrConflict
				}
				if existing.DeletedAt != nil || existing.ContainerID == nil || *existing.ContainerID != collection || (existing.Kind != resource.KindItem && existing.Kind != resource.KindFolder) {
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
