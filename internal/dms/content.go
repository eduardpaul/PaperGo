package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemsurface"
	"papergo/ent/publication"
	"papergo/ent/resource"
	"time"
)

func (s *Service) Publish(ctx context.Context, subject, id string, version int) (out *ent.Publication, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "publish")
		if e != nil {
			return e
		}
		if r.Kind != resource.KindItem || r.ContainerID == nil || r.HeadRevisionID == nil {
			return invalid("only items can be published")
		}
		if r.Version != version {
			return ErrConflict
		}
		c, e := t.Client.Resource.Get(ctx, *r.ContainerID)
		if e != nil {
			return e
		}
		if !c.PublishingEnabled {
			return invalid("publishing is automatic for this container")
		}
		rev, e := t.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
		if e != nil {
			return e
		}
		if c.Kind == resource.KindLibrary && rev.BlobID == nil {
			return invalid("library items need a blob before explicit publishing")
		}
		out, e = t.publishRevision(ctx, subject, r, rev, true)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "item.publish", r, map[string]any{"revision_id": rev.ID, "publication_id": out.ID})
	})
	return
}
func (s *Service) Unpublish(ctx context.Context, subject, id string, version int) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		r, e := t.authorize(ctx, subject, id, "publish")
		if e != nil {
			return e
		}
		if r.Kind != resource.KindItem || r.ContainerID == nil {
			return invalid("only items can be unpublished")
		}
		if r.Version != version {
			return ErrConflict
		}
		if r.PublishedRevisionID == nil {
			return ErrConflict
		}
		c, e := t.Client.Resource.Get(ctx, *r.ContainerID)
		if e != nil {
			return e
		}
		if !c.PublishingEnabled {
			return invalid("enable publishing before unpublishing items")
		}
		rev, e := t.Client.ItemRevision.Get(ctx, *r.PublishedRevisionID)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(map[string]any{"revision_id": rev.ID})
		if e != nil {
			return e
		}
		if _, e = t.Client.Publication.Create().SetItemID(id).SetRevisionID(rev.ID).SetVersion(version).SetAction(publication.ActionUnpublish).SetPublishedBy(subject).SetSnapshot(raw).Save(ctx); e != nil {
			return e
		}
		ids, e := t.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(id), itemsurface.SurfaceEQ(itemsurface.SurfacePublished)).IDs(ctx)
		if e != nil {
			return e
		}
		if _, e = t.Client.FieldValue.Delete().Where(fieldvalue.SurfaceIDIn(ids...)).Exec(ctx); e != nil {
			return e
		}
		if _, e = t.Client.ItemSurface.Delete().Where(itemsurface.IDIn(ids...)).Exec(ctx); e != nil {
			return e
		}
		n, e := t.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).ClearPublishedRevisionID().AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		out, e = t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if e = t.overlayHead(ctx, out); e != nil {
			return e
		}
		return t.audit(ctx, subject, "item.unpublish", out, map[string]any{"revision_id": rev.ID})
	})
	return
}
func (s *Service) Publications(ctx context.Context, subject, id string, after, limit int) ([]*ent.Publication, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.Publication, error) { return t.Publications(ctx, subject, id, after, limit) })
	}
	r, err := s.Get(ctx, subject, id)
	if err != nil {
		return nil, err
	}
	q := s.Client.Publication.Query().Where(publication.ItemIDEQ(id), publication.VersionGT(after))
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*ent.Resource{r})
	if err != nil {
		return nil, err
	}
	if !draft[id] {
		if r.PublishedRevisionID == nil {
			return []*ent.Publication{}, nil
		}
		q.Where(publication.RevisionIDEQ(*r.PublishedRevisionID), publication.ActionEQ(publication.ActionPublish))
	}
	return q.Order(ent.Asc(publication.FieldVersion)).Limit(pageSize(limit)).All(ctx)
}

type BlobInput struct {
	ObjectKey   string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
}

func (s *Service) CanUpload(ctx context.Context, subject, id string, version int) error {
	r, err := s.authorize(ctx, subject, id, "write")
	if err != nil {
		return err
	}
	if r.Version != version {
		return ErrConflict
	}
	if r.Kind != resource.KindItem || r.ContainerID == nil {
		return invalid("blob must belong to a library item")
	}
	c, err := s.Client.Resource.Get(ctx, *r.ContainerID)
	if err != nil {
		return err
	}
	if c.Kind != resource.KindLibrary {
		return invalid("only library items accept blobs")
	}
	return nil
}
func (s *Service) AttachBlob(ctx context.Context, subject, id string, version int, in BlobInput) (out *ent.Blob, err error) {
	if !filenameValid(in.Filename) || in.Size < 0 || len(in.SHA256) != 64 || len(in.ContentType) > 255 || in.ContentType == "" || in.ObjectKey == "" {
		return nil, invalid("invalid blob metadata")
	}
	err = s.write(ctx, func(t *Service) error {
		if e := t.CanUpload(ctx, subject, id, version); e != nil {
			return e
		}
		r, e := t.Client.Resource.Get(ctx, id)
		if e != nil {
			return e
		}
		if e = t.overlayHead(ctx, r); e != nil {
			return e
		}
		n, e := t.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).AddVersion(1).SetUpdatedAt(time.Now().UTC()).Save(ctx)
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		r.Version++
		blobVersion := 1
		last, e := t.Client.Blob.Query().Where(blob.ItemIDEQ(id)).Order(ent.Desc(blob.FieldVersion)).First(ctx)
		if e == nil {
			blobVersion = last.Version + 1
		} else if !ent.IsNotFound(e) {
			return e
		}
		out, e = t.Client.Blob.Create().SetItemID(id).SetVersion(blobVersion).SetObjectKey(in.ObjectKey).SetFilename(in.Filename).SetContentType(in.ContentType).SetSize(in.Size).SetSha256(in.SHA256).Save(ctx)
		if e != nil {
			return e
		}
		if _, e = t.recordRevision(ctx, subject, r, &out.ID); e != nil {
			return e
		}
		return t.audit(ctx, subject, "blob.attach", r, map[string]any{"blob_id": out.ID, "size": in.Size, "sha256": in.SHA256})
	})
	return
}
func (s *Service) GetBlob(ctx context.Context, subject, id, blobID string) (*ent.Blob, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.Blob, error) { return t.GetBlob(ctx, subject, id, blobID) })
	}
	r, err := s.Get(ctx, subject, id)
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindItem {
		return nil, ErrNotFound
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*ent.Resource{r})
	if err != nil {
		return nil, err
	}
	selected := r.PublishedRevisionID
	if draft[id] {
		selected = r.HeadRevisionID
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	rev, err := s.Client.ItemRevision.Get(ctx, *selected)
	if err != nil {
		return nil, err
	}
	if blobID == "" {
		if rev.BlobID == nil {
			return nil, ErrNotFound
		}
		blobID = *rev.BlobID
	} else if !draft[id] && (rev.BlobID == nil || blobID != *rev.BlobID) {
		return nil, ErrNotFound
	}
	b, err := s.Client.Blob.Query().Where(blob.ItemIDEQ(id), blob.IDEQ(blobID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return b, err
}
