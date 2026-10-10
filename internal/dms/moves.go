package dms

import (
	"context"
	"encoding/json"
	"time"

	"papergo/ent"
	"papergo/ent/grant"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
)

type MoveItem struct {
	CollectionID string `json:"collection_id"`
	// Empty or omitted parent selects the destination collection root.
	ParentID string `json:"parent_id,omitempty"`
}

func (s *Service) MoveItem(ctx context.Context, subject, id string, version int, in MoveItem) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "write")
		if e != nil {
			return e
		}
		if r.ContainerID == nil || (r.Kind != resource.KindItem && r.Kind != resource.KindFolder) {
			return invalid("only items and folders can move")
		}
		if r.Version != version {
			return ErrConflict
		}
		dest, e := t.authorize(ctx, subject, in.CollectionID, "read")
		if e != nil {
			return e
		}
		if dest.Kind != resource.KindList && dest.Kind != resource.KindLibrary {
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
		if r.Kind != resource.KindItem {
			return invalid("cross-collection moves support content items only")
		}
		source, e := t.Client.Resource.Get(ctx, *r.ContainerID)
		if e != nil {
			return e
		}
		if source.Kind != dest.Kind {
			return invalid("destination must have the same collection kind")
		}
		if e = t.overlayHead(ctx, r); e != nil {
			return e
		}
		schema, e := t.Client.SchemaRevision.Get(ctx, *dest.SchemaHeadID)
		if e != nil {
			return e
		}
		defs, rules, e := typedDefinitions(schema.Definition, *r.ContentTypeID)
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
		for _, statement := range []string{`DELETE FROM field_values WHERE item_id=?`, `DELETE FROM business_keys WHERE item_id=?`, `DELETE FROM item_surfaces WHERE item_id=?`} {
			if _, e = t.Client.ExecContext(ctx, statement, id); e != nil {
				return e
			}
		}
		if _, e = t.Client.Grant.Delete().Where(grant.ResourceIDEQ(id)).Exec(ctx); e != nil {
			return e
		}
		out, e = t.Client.Resource.UpdateOneID(id).SetWorkspaceID(dest.WorkspaceID).SetContainerID(dest.ID).SetParentID(parent).SetInheritPermissions(true).SetNillableNameKey(key).SetValues(r.Values).AddVersion(1).SetUpdatedAt(time.Now().UTC()).SetUpdatedBy(subject).Save(ctx)
		if e != nil {
			return e
		}
		if string(before) != string(after) {
			if _, e = t.recordRevision(ctx, subject, out, nil, false); e != nil {
				return e
			}
		} else {
			rev, e := t.Client.ItemRevision.Get(ctx, *out.HeadRevisionID)
			if e != nil {
				return e
			}
			if e = t.replaceSurface(ctx, out, rev, itemsurface.SurfaceHead, defs); e != nil {
				return e
			}
			if !dest.PublishingEnabled {
				if _, e = t.publishRevision(ctx, subject, out, rev, false, nil); e != nil {
					return e
				}
			}
		}
		// Explicit publications retain their frozen content and schema.
		out, e = t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if out.PublishedRevisionID != nil {
			rev, e := t.Client.ItemRevision.Get(ctx, *out.PublishedRevisionID)
			if e != nil {
				return e
			}
			frozen, e := t.Client.SchemaRevision.Get(ctx, rev.SchemaRevisionID)
			if e != nil {
				return e
			}
			publishedDefs, _, e := typedDefinitions(frozen.Definition, rev.ContentTypeID)
			if e != nil {
				return e
			}
			if e = t.replaceSurface(ctx, out, rev, itemsurface.SurfacePublished, publishedDefs); e != nil {
				return e
			}
		}
		if _, e = t.Client.ExecContext(ctx, `UPDATE workflow_runs SET workspace_id=? WHERE item_id=?`, dest.WorkspaceID, id); e != nil {
			return e
		}
		details := map[string]any{"from_workspace_id": oldWorkspace, "from_collection_id": oldContainer, "from_parent_id": oldParent, "collection_id": dest.ID, "parent_id": parent}
		if string(before) == string(after) {
			details["revision_id"] = *out.HeadRevisionID
			rev, e := t.Client.ItemRevision.Get(ctx, *out.HeadRevisionID)
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
