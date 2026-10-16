package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/resource"
)

type MoveSelectionItem struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}
type MoveSelection struct {
	ParentID string              `json:"parent_id"`
	Items    []MoveSelectionItem `json:"items"`
}

// MoveNameConflict discloses a replacement target only when both items are writable files.
type MoveNameConflict struct {
	SourceID string
	Existing *DocumentNameConflict
}

func (e *MoveNameConflict) Error() string {
	return "an entry with this name already exists in the destination"
}
func (e *MoveNameConflict) Unwrap() error { return ErrConflict }

// MoveSelection moves up to 100 top-level items/folders in one transaction.
// Selecting both an ancestor and its descendant is rejected to avoid flattening a subtree.
func (s *Service) MoveSelection(ctx context.Context, subject, collectionID string, in MoveSelection) (out []MoveSelectionItem, err error) {
	if len(in.Items) == 0 || len(in.Items) > 100 {
		return nil, invalid("select between 1 and 100 entries")
	}
	err = s.write(ctx, func(t *Service) error {
		collection, err := t.authorize(ctx, subject, collectionID, "read")
		if err != nil {
			return err
		}
		if collection.Kind != resource.KindList && collection.Kind != resource.KindLibrary {
			return invalid("moves require a collection")
		}
		parent := in.ParentID
		if parent == "" {
			parent = collectionID
		}
		target, err := t.authorize(ctx, subject, parent, "write")
		if err != nil {
			return err
		}
		if target.ID != collectionID && (target.Kind != resource.KindFolder || target.ContainerID == nil || *target.ContainerID != collectionID) {
			return invalid("destination must be a folder of this collection")
		}
		selected := map[string]bool{}
		rows := make([]*ent.Resource, 0, len(in.Items))
		for _, input := range in.Items {
			if input.ID == "" || selected[input.ID] || input.Version < 1 {
				return invalid("each selected entry needs a unique id and current positive version")
			}
			selected[input.ID] = true
			row, err := t.authorize(ctx, subject, input.ID, "write")
			if err != nil {
				return err
			}
			if row.ContainerID == nil || *row.ContainerID != collectionID || !contentKind(row.Kind) {
				return invalid("selected entries must belong to this collection")
			}
			if row.Version != input.Version {
				return ErrPreconditionFailed
			}
			rows = append(rows, row)
		}
		for _, row := range rows {
			for ancestor := row.ParentID; ancestor != nil && *ancestor != collectionID; {
				if selected[*ancestor] {
					return invalid("select a folder or its descendants, not both")
				}
				p, err := t.Client.Resource.Get(ctx, *ancestor)
				if err != nil {
					return err
				}
				ancestor = p.ParentID
			}
		}
		out = make([]MoveSelectionItem, 0, len(rows))
		for _, row := range rows {
			if row.ParentID != nil && *row.ParentID == parent {
				out = append(out, MoveSelectionItem{row.ID, row.Version})
				continue
			}
			if row.NameKey != nil {
				existing, err := t.Client.Resource.Query().Where(resource.ParentIDEQ(parent), resource.NameKeyEQ(*row.NameKey), resource.DeletedAtIsNil(), resource.IDNEQ(row.ID)).Only(ctx)
				if err == nil {
					conflict := &MoveNameConflict{SourceID: row.ID}
					_, writeErr := t.authorize(ctx, subject, existing.ID, "write")
					if row.Kind == resource.KindItem && existing.Kind == resource.KindItem && writeErr == nil {
						conflict.Existing = &DocumentNameConflict{ItemID: existing.ID, Version: existing.Version}
					}
					return conflict
				}
				if !ent.IsNotFound(err) {
					return err
				}
			}
			moved, err := t.update(ctx, subject, row.ID, row.Version, UpdateResource{ParentID: &parent})
			if err != nil {
				return err
			}
			out = append(out, MoveSelectionItem{moved.ID, moved.Version})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
