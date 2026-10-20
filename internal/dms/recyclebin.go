package dms

import (
	"context"
	"log/slog"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"strings"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/relationships"
	"papergo/internal/storage"
)

type DeletedResource struct {
	*resources.Resource
	DeletedAt time.Time `json:"deleted_at"`
	DeletedBy string    `json:"deleted_by"`
	// DeletedWith counts what a folder took with it: descendants deleted at
	// the same time, which a recursive restore brings back.
	DeletedWith int `json:"deleted_with,omitempty"`
}

// RecycleBinQuery pages the recycle bin, newest deletions first. TopLevel
// leaves out entries deleted together with their folder.
type RecycleBinQuery struct {
	After    string
	Limit    int
	TopLevel bool
}

// RecycleBin exposes only writable deleted content in a live collection.
func (s *Service) RecycleBin(ctx context.Context, subject, collection string, in RecycleBinQuery) (Page[DeletedResource], error) {
	return read(ctx, s, func(t *Service) (Page[DeletedResource], error) {
		out := Page[DeletedResource]{Data: []DeletedResource{}}
		if _, err := t.recycleCollection(ctx, subject, collection); err != nil {
			return out, err
		}
		f := resources.Filter{ContainerID: collection, State: resources.Deleted, Reader: &resources.Reader{Principal: principal(ctx, subject), Action: access.Write},
			DeletionRoots: in.TopLevel, Order: resources.NewestDeletion, Limit: pageSize(in.Limit) + 1}
		if in.After != "" {
			at, id, ok := strings.Cut(in.After, "|")
			when, err := time.Parse(time.RFC3339Nano, at)
			if !ok || err != nil {
				return out, invalid("invalid recycle bin cursor")
			}
			f.BeforeDeletion = &resources.Cursor{At: when, ID: id}
		}
		rows, err := t.DAL.Resources().Resources(ctx, f)
		if err != nil {
			return out, err
		}
		if len(rows) > pageSize(in.Limit) {
			rows = rows[:pageSize(in.Limit)]
			last := rows[len(rows)-1]
			out.NextCursor = last.DeletedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
		}
		for _, r := range rows {
			if contentKind(r.Kind) {
				if err = t.overlayHead(ctx, r); err != nil {
					return out, err
				}
			}
			actor := ""
			if r.DeletedBy != nil {
				actor = *r.DeletedBy
			}
			entry := DeletedResource{r, *r.DeletedAt, actor, 0}
			if r.Kind == refs.KindFolder {
				tree, err := t.DAL.Resources().SubtreeIDs(ctx, r.ID, resources.TreeFilter{DeletedAt: r.DeletedAt})
				if err != nil {
					return out, err
				}
				entry.DeletedWith = len(tree) - 1
			}
			out.Data = append(out.Data, entry)
		}
		return out, nil
	})
}

func (s *Service) recycleCollection(ctx context.Context, subject, id string) (*resources.Resource, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	c, err := s.authorize(ctx, subject, id, "read")
	if err != nil {
		return nil, err
	}
	if c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
		return nil, invalid("recycle bin belongs to a collection")
	}
	return c, nil
}

func (s *Service) deletedResource(ctx context.Context, subject, collection, id, action string) (*resources.Resource, error) {
	if _, err := s.recycleCollection(ctx, subject, collection); err != nil {
		return nil, err
	}
	r, err := s.DAL.Resources().One(ctx, resources.Filter{IDs: []string{id}, ContainerID: collection, State: resources.Deleted, Reader: &resources.Reader{Principal: principal(ctx, subject), Action: access.Write}})
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if action == "manage" {
		allowed, err := s.allowedMany(ctx, subject, action, []*resources.Resource{r})
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
func (s *Service) RestoreDeleted(ctx context.Context, subject, collection, id string, recursive bool) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.deletedResource(ctx, subject, collection, id, "write")
		if e != nil {
			return e
		}
		c, e := t.DAL.Resources().Resource(ctx, collection)
		if e != nil {
			return e
		}
		rows := []*resources.Resource{r}
		if recursive && r.Kind == refs.KindFolder {
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
			if contentKind(row.Kind) {
				if _, e = t.contentType(ctx, collection, *row.ContentTypeID); e != nil {
					return invalid("reattach the deleted item's content type before restoring it")
				}
			}
			parent := collection
			if row.ParentID != nil {
				p, e := t.DAL.Resources().Resource(ctx, *row.ParentID)
				if e == nil && p.DeletedAt == nil {
					parent = p.ID
				} else if e != nil && !dberr.IsNotFound(e) {
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
			name := ""
			if key != nil {
				name = *key
			}
			row, e = t.DAL.Resources().Restore(ctx, row.ID, parent, name, subject)
			if e != nil {
				return e
			}
			if contentKind(row.Kind) && !c.PublishingEnabled && row.HeadRevisionID != nil && (row.PublishedRevisionID == nil || *row.PublishedRevisionID != *row.HeadRevisionID) {
				// Without publishing the head is the published content: an entry
				// deleted while publishing was on comes back published.
				head, e := t.revision(ctx, *row.HeadRevisionID)
				if e != nil {
					return e
				}
				if _, e = t.publishRevision(ctx, subject, row, head, false, nil); e != nil {
					return e
				}
				t.emitRestored(ctx, subject, row)
			} else if contentKind(row.Kind) {
				// Restore the retained head and publication with their frozen
				// schemas; without publishing only the published surface exists.
				surfaces := []struct {
					id   *string
					kind refs.Surface
				}{{row.HeadRevisionID, refs.SurfaceHead}, {row.PublishedRevisionID, refs.SurfacePublished}}
				if !c.PublishingEnabled {
					surfaces = surfaces[1:]
				}
				for _, surface := range surfaces {
					if surface.id == nil {
						continue
					}
					rev, e := t.revision(ctx, *surface.id)
					if e != nil {
						return e
					}
					schema, e := t.DAL.Catalog().Schema(ctx, rev.SchemaRevisionID)
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
				t.emitRestored(ctx, subject, row)
			}
			if e = t.audit(ctx, subject, "resource.restore", row, map[string]any{"parent_id": parent}); e != nil {
				return e
			}
			if row.ID == id {
				out = row
			}
		}
		if contentKind(out.Kind) {
			return t.overlayHead(ctx, out)
		}
		return nil
	})
	return
}

// deletedTree orders ancestors before children for restore; purge reverses it.
func (s *Service) deletedTree(ctx context.Context, r *resources.Resource, sameDeletion bool) ([]*resources.Resource, error) {
	f := resources.TreeFilter{State: resources.Deleted}
	if sameDeletion {
		f.DeletedAt = r.DeletedAt
	}
	return s.DAL.Resources().Subtree(ctx, r.ID, f)
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
			if err = t.DAL.Blobs().QueueItemObjects(ctx, row.ID); err != nil {
				return err
			}
			links, err := t.DAL.Relationships().List(ctx, relationships.Filter{Endpoint: row.ID})
			if err != nil {
				return err
			}
			for _, link := range links {
				if err = t.touchRelationship(ctx, subject, link, "deleted"); err != nil {
					return err
				}
			}
			if err = t.DAL.Resources().Purge(ctx, row.ID); err != nil {
				return err
			}
			switch row.Kind {
			case refs.KindItem:
				t.emit(ctx, EventItemPurged, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
			case refs.KindFolder:
				t.emit(ctx, EventFolderPurged, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
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
	if err := s.collectPreviews(ctx); err != nil {
		return err
	}
	if err := s.collectTexts(ctx); err != nil {
		return err
	}
	rows, err := s.DAL.Blobs().Deletions(ctx, 100)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = s.write(ctx, func(t *Service) error {
			if err := t.deleteUnreferencedObject(ctx, store, row.ObjectKey); err != nil {
				return err
			}
			return t.DAL.Blobs().Dequeue(ctx, row.ID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteUnreferencedObject(ctx context.Context, store objectDeleter, key string) error {
	referenced, err := s.DAL.Blobs().Referenced(ctx, key, blobs.Packages{ImportKind: OperationKindImport, ExportKind: OperationKindExport})
	if err != nil {
		return err
	}
	if referenced {
		return nil
	}
	// Keep the SQLite writer lock through storage deletion. Upload attachment,
	// file restoration and administrative backup cannot race this decision.
	return store.Delete(ctx, key)
}

const ObjectGracePeriod = time.Hour

// CollectObjects reconciles bytes left behind by interrupted uploads or commits.
// New uploads remain untouched for a grace period, as in PaperDotNet's cleanup.
func (s *Service) CollectObjects(ctx context.Context, store storage.Store, now time.Time) error {
	return store.WalkObjects(ctx, now.Add(-ObjectGracePeriod), func(key string) error {
		return s.write(ctx, func(t *Service) error { return t.deleteUnreferencedObject(ctx, store, key) })
	})
}

func (s *Service) RunBlobCleanup(ctx context.Context, store storage.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.CleanupPurgedBlobs(ctx, store); err != nil && ctx.Err() == nil {
			log.Error("purged blob cleanup", "error", err)
		}
		if err := s.CollectObjects(ctx, store, time.Now().UTC()); err != nil && ctx.Err() == nil {
			log.Error("unreferenced object cleanup", "error", err)
		}
		if err := s.SweepReclaims(ctx, store, time.Now().UTC()); err != nil && ctx.Err() == nil {
			log.Error("reclaimed file cleanup", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// emitRestored raises item.restored or folder.restored for a restored entry.
func (s *Service) emitRestored(ctx context.Context, subject string, row *resources.Resource) {
	switch row.Kind {
	case refs.KindItem:
		s.emit(ctx, EventItemRestored, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
	case refs.KindFolder:
		s.emit(ctx, EventFolderRestored, subject, row.WorkspaceID, row.ContainerID, row.ID, nil)
	}
}
