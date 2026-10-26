package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

type MoveItem struct {
	CollectionID string `json:"collection_id"`
	// Empty or omitted parent selects the destination collection root.
	ParentID string `json:"parent_id,omitempty"`
}

func (s *Service) MoveItem(ctx context.Context, subject, id string, version int, in MoveItem) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "write")
		if e != nil {
			return e
		}
		if r.ContainerID == nil || (r.Kind != refs.KindItem && r.Kind != refs.KindFolder) {
			return invalid("only items and folders can move")
		}
		if r.Version != version {
			return ErrPreconditionFailed
		}
		dest, e := t.authorize(ctx, subject, in.CollectionID, "read")
		if e != nil {
			return e
		}
		if dest.Kind != refs.KindList && dest.Kind != refs.KindLibrary {
			return invalid("destination must be a collection")
		}
		parent := in.ParentID
		if parent == "" {
			parent = dest.ID
		}
		if dest.ID == *r.ContainerID {
			out, e = t.update(ctx, subject, id, version, UpdateResource{ParentID: &parent})
			return e
		}
		if managed, e := t.integrationManaged(ctx, id); e != nil {
			return e
		} else if managed {
			return invalid("remove the list integration before moving its item to another list")
		}
		if r.Kind != refs.KindItem {
			return invalid("cross-collection moves support content items only")
		}
		source, e := t.DAL.Resources().Resource(ctx, *r.ContainerID)
		if e != nil {
			return e
		}
		if source.Kind != dest.Kind {
			return invalid("destination must have the same collection kind")
		}
		if e = t.overlayHead(ctx, r); e != nil {
			return e
		}
		schema, e := t.DAL.Catalog().Schema(ctx, *dest.SchemaHeadID)
		if e != nil {
			return e
		}
		// The item becomes the destination's list type of its catalog type.
		typeID, e := t.DAL.Catalog().Counterpart(ctx, dest.ID, *r.ContentTypeID)
		if dberr.IsNotFound(e) {
			return invalid("the destination does not accept the item's content type")
		}
		if e != nil {
			return e
		}
		defs, rules, e := typedDefinitions(schema.Definition, typeID)
		if e != nil {
			return e
		}
		before, e := json.Marshal(r.Values)
		if e != nil {
			return e
		}
		// Revalidate every value and reference against the destination schema;
		// stale references cannot use the ordinary unchanged-value exception.
		if e = t.normalizeItemValues(ctx, subject, defs, r.Values, nil, true); e != nil {
			return e
		}
		if e = evaluateRules(defs, rules, r.Values); e != nil {
			return e
		}
		after, e := json.Marshal(r.Values)
		if e != nil {
			return e
		}
		oldWorkspace, oldContainer, oldParent := r.WorkspaceID, *r.ContainerID, *r.ParentID
		r.WorkspaceID, r.ContainerID = dest.WorkspaceID, &dest.ID
		if e = t.checkMove(ctx, subject, r, parent); e != nil {
			return e
		}
		key, e := t.fileNameKey(ctx, r.Kind, r.ContainerID, r.Name)
		if e != nil {
			return e
		}
		name := ""
		if key != nil {
			name = *key
		}
		out, e = t.DAL.Resources().Transfer(ctx, id, dest.ID, parent, name, after, subject)
		if e != nil {
			return e
		}
		if string(before) != string(after) {
			if _, e = t.recordRevision(ctx, subject, out, nil, false); e != nil {
				return e
			}
		} else {
			rev, e := t.revision(ctx, *out.HeadRevisionID)
			if e != nil {
				return e
			}
			if dest.PublishingEnabled {
				if e = t.replaceSurface(ctx, out, rev, refs.SurfaceHead, defs); e != nil {
					return e
				}
			}
			if !dest.PublishingEnabled {
				if _, e = t.publishRevision(ctx, subject, out, rev, false, nil); e != nil {
					return e
				}
			}
		}
		// Explicit publications retain their frozen content and schema.
		out, e = t.DAL.Resources().Resource(ctx, id)
		if e != nil {
			return e
		}
		t.emitScopeMoved(ctx, subject, r, out)
		if out.PublishedRevisionID != nil {
			rev, e := t.revision(ctx, *out.PublishedRevisionID)
			if e != nil {
				return e
			}
			frozen, e := t.DAL.Catalog().Schema(ctx, rev.SchemaRevisionID)
			if e != nil {
				return e
			}
			publishedDefs, _, e := typedDefinitions(frozen.Definition, rev.ContentTypeID)
			if e != nil {
				return e
			}
			if e = t.replaceSurface(ctx, out, rev, refs.SurfacePublished, publishedDefs); e != nil {
				return e
			}
		}
		details := map[string]any{"from_workspace_id": oldWorkspace, "from_collection_id": oldContainer, "from_parent_id": oldParent, "collection_id": dest.ID, "parent_id": parent}
		if string(before) == string(after) {
			details["revision_id"] = *out.HeadRevisionID
			rev, e := t.revision(ctx, *out.HeadRevisionID)
			if e != nil {
				return e
			}
			details["revision_number"], details["content_type_id"] = rev.RevisionNumber, rev.ContentTypeID
			if rev.BlobID != nil {
				details["blob_id"] = *rev.BlobID
			}
			t.emit(ctx, EventItemUpdated, subject, out.WorkspaceID, out.ContainerID, id, details)
		}
		if e = t.audit(ctx, subject, "resource.move", out, details); e != nil {
			return e
		}
		return t.overlayHead(ctx, out)
	})
	return
}
