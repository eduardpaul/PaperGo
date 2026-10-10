package dms

import (
	"context"
	"log/slog"
	"time"

	"papergo/ent"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
)

type DeletedResource struct {
	*ent.Resource
	DeletedAt time.Time `json:"deleted_at"`
	DeletedBy string    `json:"deleted_by"`
}

// RecycleBin exposes only writable deleted content in a live collection.
func (s *Service) RecycleBin(ctx context.Context, subject, collection, after string, limit int) (Page[DeletedResource], error) {
	return read(ctx, s, func(t *Service) (Page[DeletedResource], error) {
		out := Page[DeletedResource]{Data: []DeletedResource{}}
		if _, err := t.recycleCollection(ctx, subject, collection); err != nil {
			return out, err
		}
		q := t.Client.Resource.Query().Where(resource.ContainerIDEQ(collection), resource.DeletedAtNotNil(), permissionPredicate(subject, "write"))
		if after != "" {
			q.Where(resource.IDGT(after))
		}
		rows, err := q.Order(ent.Asc(resource.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		if err != nil {
			return out, err
		}
		if len(rows) > pageSize(limit) {
			rows = rows[:pageSize(limit)]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, r := range rows {
			if r.Kind == resource.KindItem {
				if err = t.overlayHead(ctx, r); err != nil {
					return out, err
				}
			}
			actor := ""
			if r.DeletedBy != nil {
				actor = *r.DeletedBy
			}
			out.Data = append(out.Data, DeletedResource{r, *r.DeletedAt, actor})
		}
		return out, nil
	})
}

func (s *Service) recycleCollection(ctx context.Context, subject, id string) (*ent.Resource, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	c, err := s.authorize(ctx, subject, id, "read")
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
		return nil, invalid("recycle bin belongs to a collection")
	}
	return c, nil
}

func (s *Service) deletedResource(ctx context.Context, subject, collection, id, action string) (*ent.Resource, error) {
	if _, err := s.recycleCollection(ctx, subject, collection); err != nil {
		return nil, err
	}
	r, err := s.Client.Resource.Query().Where(resource.IDEQ(id), resource.ContainerIDEQ(collection), resource.DeletedAtNotNil(), permissionPredicate(subject, "write")).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if action == "manage" {
		allowed, err := s.allowedMany(ctx, subject, action, []*ent.Resource{r})
		if err != nil {
			return nil, err
		}
		if !allowed[id] {
			return nil, ErrForbidden
		}
	}
	return r, nil
}

// RestoreDeleted restores one entry. Recursive restores descendants deleted by
// the same deletion, leaving older recycle-bin entries untouched.
func (s *Service) RestoreDeleted(ctx context.Context, subject, collection, id string, recursive bool) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.deletedResource(ctx, subject, collection, id, "write")
		if e != nil {
			return e
		}
		rows := []*ent.Resource{r}
		if recursive && r.Kind == resource.KindFolder {
			rows, e = t.deletedTree(ctx, r, true)
			if e != nil {
				return e
			}
		}
		allowed, e := t.allowedMany(ctx, subject, "write", rows)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if !allowed[row.ID] {
				return ErrForbidden
			}
		}
		for _, row := range rows {
			if row.Kind == resource.KindItem {
				if _, e = t.contentType(ctx, collection, *row.ContentTypeID); e != nil {
					return invalid("reattach the deleted item's content type before restoring it")
				}
			}
			parent := collection
			if row.ParentID != nil {
				p, e := t.Client.Resource.Get(ctx, *row.ParentID)
				if e == nil && p.DeletedAt == nil {
					parent = p.ID
				} else if e != nil && !ent.IsNotFound(e) {
					return e
				}
			}
			if e = t.checkMove(ctx, subject, row, parent); e != nil {
				return e
			}
			key, e := t.fileNameKey(ctx, row.Kind, row.ContainerID, row.Name)
			if e != nil {
				return e
			}
			row, e = t.Client.Resource.UpdateOne(row).ClearDeletedAt().ClearDeletedBy().SetParentID(parent).SetNillableNameKey(key).AddVersion(1).SetUpdatedAt(time.Now().UTC()).SetUpdatedBy(subject).Save(ctx)
			if e != nil {
				return e
			}
			if row.Kind == resource.KindItem {
				// Restore the retained head and publication with their frozen schemas.
				for _, surface := range []struct {
					id   *string
					kind itemsurface.Surface
				}{{row.HeadRevisionID, itemsurface.SurfaceHead}, {row.PublishedRevisionID, itemsurface.SurfacePublished}} {
					if surface.id == nil {
						continue
					}
					rev, e := t.Client.ItemRevision.Get(ctx, *surface.id)
					if e != nil {
						return e
					}
					schema, e := t.Client.SchemaRevision.Get(ctx, rev.SchemaRevisionID)
					if e != nil {
						return e
					}
					defs, _, e := typedDefinitions(schema.Definition, rev.ContentTypeID)
					if e != nil {
						return e
					}
					if e = t.replaceSurface(ctx, row, rev, surface.kind, defs); e != nil {
						return e
					}
				}
				t.emit(ctx, EventItemRestored, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
			}
			if e = t.audit(ctx, subject, "resource.restore", row, map[string]any{"parent_id": parent}); e != nil {
				return e
			}
			if row.ID == id {
				out = row
			}
		}
		if out.Kind == resource.KindItem {
			return t.overlayHead(ctx, out)
		}
		return nil
	})
	return
}

// deletedTree orders ancestors before children for restore; purge reverses it.
func (s *Service) deletedTree(ctx context.Context, r *ent.Resource, sameDeletion bool) ([]*ent.Resource, error) {
	query := `WITH RECURSIVE sub(id,depth) AS (SELECT ?,0 UNION ALL SELECT c.id,sub.depth+1 FROM resources c JOIN sub ON c.parent_id=sub.id WHERE c.deleted_at IS NOT NULL`
	args := []any{r.ID}
	if sameDeletion {
		query += ` AND c.deleted_at=?`
		args = append(args, *r.DeletedAt)
	}
	query += `) SELECT id FROM sub ORDER BY depth,id`
	result, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for result.Next() {
		var id string
		if err = result.Scan(&id); err != nil {
			result.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = result.Err()
	result.Close()
	if err != nil {
		return nil, err
	}
	out := make([]*ent.Resource, 0, len(ids))
	for _, id := range ids {
		row, e := s.Client.Resource.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, nil
}

// PurgeDeleted removes content, versions and blobs, retaining audit and run IDs.
func (s *Service) PurgeDeleted(ctx context.Context, subject, collection, id string, recursive bool) error {
	return s.write(ctx, func(t *Service) error {
		r, err := t.deletedResource(ctx, subject, collection, id, "manage")
		if err != nil {
			return err
		}
		rows, err := t.deletedTree(ctx, r, false)
		if err != nil {
			return err
		}
		if len(rows) > 1 && !recursive {
			return ErrFolderNotEmpty
		}
		allowed, err := t.allowedMany(ctx, subject, "manage", rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !allowed[row.ID] {
				return ErrForbidden
			}
		}
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			if err = t.audit(ctx, subject, "resource.purge", row, nil); err != nil {
				return err
			}
			if _, err = t.Client.ExecContext(ctx, `INSERT INTO blob_deletions(id,created_at,object_key) SELECT id,?,object_key FROM blobs WHERE item_id=? ON CONFLICT(object_key) DO NOTHING`, time.Now().UTC(), row.ID); err != nil {
				return err
			}
			if err = t.Client.Resource.UpdateOne(row).ClearHeadRevisionID().ClearPublishedRevisionID().Exec(ctx); err != nil {
				return err
			}
			for _, statement := range []string{
				`DELETE FROM field_values WHERE item_id=?`, `DELETE FROM business_keys WHERE item_id=?`, `DELETE FROM item_surfaces WHERE item_id=?`,
				`DELETE FROM publications WHERE item_id=?`, `DELETE FROM item_histories WHERE revision_id IN (SELECT id FROM item_revisions WHERE item_id=?)`,
				`DELETE FROM item_revisions WHERE item_id=?`, `DELETE FROM blobs WHERE item_id=?`, `DELETE FROM grants WHERE resource_id=?`,
				`DELETE FROM operation_items WHERE item_id=?`,
				`DELETE FROM relationships WHERE source_id=? OR target_id=?`, `DELETE FROM resources WHERE id=?`,
			} {
				args := []any{row.ID}
				if statement == `DELETE FROM relationships WHERE source_id=? OR target_id=?` {
					args = append(args, row.ID)
				}
				if _, err = t.Client.ExecContext(ctx, statement, args...); err != nil {
					return err
				}
			}
			if row.Kind == resource.KindItem {
				t.emit(ctx, EventItemPurged, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
			}
		}
		return nil
	})
}

type objectDeleter interface {
	Delete(context.Context, string) error
}

// CleanupPurgedBlobs is restart-safe: failed storage deletes remain queued.
func (s *Service) CleanupPurgedBlobs(ctx context.Context, store objectDeleter) error {
	rows, err := s.Client.BlobDeletion.Query().Limit(100).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = store.Delete(ctx, row.ObjectKey); err != nil {
			return err
		}
		if err = s.write(ctx, func(t *Service) error { return t.Client.BlobDeletion.DeleteOneID(row.ID).Exec(ctx) }); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RunBlobCleanup(ctx context.Context, store objectDeleter, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.CleanupPurgedBlobs(ctx, store); err != nil && ctx.Err() == nil {
			log.Error("purged blob cleanup", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
