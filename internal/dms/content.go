package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/publication"
	"papergo/ent/resource"
	"papergo/internal/storage"
	"time"
)

func (s *Service) Publish(ctx context.Context, subject, id string, version int) (out *ent.Publication, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.publish(ctx, subject, id, version)
		return e
	})
	return
}

// publish applies the transition inside the caller's write transaction.
func (s *Service) publish(ctx context.Context, subject, id string, version int) (*ent.Publication, error) {
	r, e := s.authorize(ctx, subject, id, "publish")
	if e != nil {
		return nil, e
	}
	if r.Kind != resource.KindItem || r.ContainerID == nil || r.HeadRevisionID == nil {
		return nil, invalid("only items can be published")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	c, e := s.Client.Resource.Get(ctx, *r.ContainerID)
	if e != nil {
		return nil, e
	}
	if !c.PublishingEnabled {
		return nil, invalid("publishing is automatic for this container")
	}
	rev, e := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
	if e != nil {
		return nil, e
	}
	if c.Kind == resource.KindLibrary && rev.BlobID == nil {
		return nil, invalid("library items need a blob before explicit publishing")
	}
	out, e := s.publishRevision(ctx, subject, r, rev, true, nil)
	if e != nil {
		return nil, e
	}
	return out, s.audit(ctx, subject, "item.publish", r, map[string]any{"revision_id": rev.ID, "publication_id": out.ID})
}
func (s *Service) Unpublish(ctx context.Context, subject, id string, version int) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.unpublish(ctx, subject, id, version)
		return e
	})
	return
}

// unpublish applies the transition inside the caller's write transaction.
func (s *Service) unpublish(ctx context.Context, subject, id string, version int) (*ent.Resource, error) {
	r, e := s.authorize(ctx, subject, id, "publish")
	if e != nil {
		return nil, e
	}
	if r.Kind != resource.KindItem || r.ContainerID == nil {
		return nil, invalid("only items can be unpublished")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	if r.PublishedRevisionID == nil {
		return nil, ErrConflict
	}
	c, e := s.Client.Resource.Get(ctx, *r.ContainerID)
	if e != nil {
		return nil, e
	}
	if !c.PublishingEnabled {
		return nil, invalid("enable publishing before unpublishing items")
	}
	rev, e := s.Client.ItemRevision.Get(ctx, *r.PublishedRevisionID)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(map[string]any{"revision_id": rev.ID})
	if e != nil {
		return nil, e
	}
	if _, e = s.Client.Publication.Create().SetItemID(id).SetRevisionID(rev.ID).SetVersion(version).SetAction(publication.ActionUnpublish).SetPublishedBy(subject).SetSnapshot(raw).Save(ctx); e != nil {
		return nil, e
	}
	ids, e := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(id), itemsurface.SurfaceEQ(itemsurface.SurfacePublished)).IDs(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = s.Client.FieldValue.Delete().Where(fieldvalue.SurfaceIDIn(ids...)).Exec(ctx); e != nil {
		return nil, e
	}
	if _, e = s.Client.ItemSurface.Delete().Where(itemsurface.IDIn(ids...)).Exec(ctx); e != nil {
		return nil, e
	}
	n, e := s.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).ClearPublishedRevisionID().AddVersion(1).Save(ctx)
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, ErrPreconditionFailed
	}
	out, e := s.Client.Resource.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if e = s.overlayHead(ctx, out); e != nil {
		return nil, e
	}
	if e = s.syncBusinessKeys(ctx, out); e != nil {
		return nil, e
	}
	s.emit(ctx, EventItemUnpublished, subject, out.WorkspaceID, out.ContainerID, out.ID, map[string]any{"revision_id": rev.ID})
	return out, s.audit(ctx, subject, "item.unpublish", out, map[string]any{"revision_id": rev.ID})
}
func (s *Service) Publications(ctx context.Context, subject, id string, after, limit int) (Page[*ent.Publication], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*ent.Publication], error) {
			return t.Publications(ctx, subject, id, after, limit)
		})
	}
	r, err := s.Get(ctx, subject, id)
	if err != nil {
		return Page[*ent.Publication]{}, err
	}
	q := s.Client.Publication.Query().Where(publication.ItemIDEQ(id), publication.VersionGT(after))
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*ent.Resource{r})
	if err != nil {
		return Page[*ent.Publication]{}, err
	}
	if !draft[id] {
		if r.PublishedRevisionID == nil {
			return Page[*ent.Publication]{Data: []*ent.Publication{}}, nil
		}
		q.Where(publication.RevisionIDEQ(*r.PublishedRevisionID), publication.ActionEQ(publication.ActionPublish))
	}
	rows, err := q.Order(ent.Asc(publication.FieldVersion)).Limit(pageSize(limit) + 1).All(ctx)
	if err != nil {
		return Page[*ent.Publication]{}, err
	}
	return numberedPage(rows, limit, func(v *ent.Publication) int { return v.Version }), nil
}

type BlobInput struct {
	ObjectKey   string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	Languages   string
	// Source says where the file came from, for file change events:
	// FileWebDAV, FileRestore or FileWorkflow; empty is an upload.
	Source string
}

// emitFileChange distinguishes first attachments from replacement and reindex events.
func (s *Service) emitFileChange(ctx context.Context, actor string, r *ent.Resource, b *ent.Blob, rev *ent.ItemRevision, source string, previousBlobID *string) {
	if source == "" {
		source = FileUpload
	}
	typ := EventFileAdded
	data := map[string]any{
		"source": source, "file_version": b.Version, "blob_id": b.ID, "content_type": b.ContentType, "size": b.Size,
		"revision_id": rev.ID, "content_type_id": rev.ContentTypeID, "filename": b.Filename,
	}
	if previousBlobID != nil {
		typ = EventFileUpdated
		data["previous_blob_id"] = *previousBlobID
	}
	s.emit(ctx, typ, actor, r.WorkspaceID, r.ContainerID, r.ID, data)
}

func (s *Service) CanUpload(ctx context.Context, subject, id string, version int) error {
	r, err := s.canWriteFile(ctx, subject, id)
	if err != nil {
		return err
	}
	if r.Version != version {
		return ErrPreconditionFailed
	}
	return nil
}

// canWriteFile checks that subject may change the file of library item id.
func (s *Service) canWriteFile(ctx context.Context, subject, id string) (*ent.Resource, error) {
	if err := s.RequireScope(ctx, subject, "document.write"); err != nil {
		return nil, err
	}
	r, err := s.authorize(ctx, subject, id, "write")
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindItem || r.ContainerID == nil {
		return nil, invalid("blob must belong to a library item")
	}
	c, err := s.Client.Resource.Get(ctx, *r.ContainerID)
	if err != nil {
		return nil, err
	}
	if c.Kind != resource.KindLibrary {
		return nil, invalid("only library items accept blobs")
	}
	return r, nil
}
func (s *Service) AttachBlob(ctx context.Context, subject, id string, version int, in BlobInput) (out *BlobUpload, err error) {
	err = s.write(ctx, func(t *Service) error {
		b, e := t.attachBlob(ctx, subject, id, version, in)
		if e != nil {
			return e
		}
		matches, e := t.checkDuplicatePolicy(ctx, subject, id, in.SHA256)
		out = &BlobUpload{b, matches}
		return e
	})
	return
}

// attachBlob records a new content revision pinning the blob, inside a write transaction.
func (s *Service) attachBlob(ctx context.Context, subject, id string, version int, in BlobInput) (*ent.Blob, error) {
	return s.writeBlob(ctx, subject, id, version, in, "blob.attach", nil)
}

func (s *Service) writeBlob(ctx context.Context, subject, id string, version int, in BlobInput, action string, sourceVersion *int) (*ent.Blob, error) {
	if e := s.CanUpload(ctx, subject, id, version); e != nil {
		return nil, e
	}
	r, e := s.Client.Resource.Get(ctx, id)
	if e != nil {
		return nil, e
	}
	if e = s.overlayHead(ctx, r); e != nil {
		return nil, e
	}
	n, e := s.Client.Resource.Update().Where(resource.IDEQ(id), resource.VersionEQ(version)).AddVersion(1).SetUpdatedAt(time.Now().UTC()).Save(ctx)
	if e != nil {
		return nil, e
	}
	if n != 1 {
		return nil, ErrPreconditionFailed
	}
	r.Version++
	out, e := s.createBlob(ctx, subject, id, in, sourceVersion == nil)
	if e != nil {
		return nil, e
	}
	var previousBlobID *string
	if r.HeadRevisionID != nil {
		previous, err := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
		if err != nil {
			return nil, err
		}
		previousBlobID = previous.BlobID
	}
	rev, e := s.recordRevision(ctx, subject, r, &out.ID, true)
	if e != nil {
		return nil, e
	}
	s.emitFileChange(ctx, subject, r, out, rev, in.Source, previousBlobID)
	details := map[string]any{"blob_id": out.ID, "size": in.Size, "sha256": in.SHA256}
	if sourceVersion != nil {
		details["source_version"] = *sourceVersion
	}
	return out, s.audit(ctx, subject, action, r, details)
}

// RestoreBlob creates a new file version from immutable earlier content, keeping
// the current metadata and applying the collection's publication rules.
func (s *Service) RestoreBlob(ctx context.Context, subject, id string, number, version int) (out *ent.Blob, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.CanUpload(ctx, subject, id, version); e != nil {
			return e
		}
		source, e := t.Client.Blob.Query().Where(blob.ItemIDEQ(id), blob.VersionEQ(number)).Only(ctx)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if source.Availability == blob.AvailabilityReclaimed {
			return ErrContentUnavailable
		}
		out, e = t.writeBlob(ctx, subject, id, version, BlobInput{ObjectKey: source.ObjectKey, Filename: source.Filename, ContentType: source.ContentType, Size: source.Size, SHA256: source.Sha256, Source: FileRestore}, "blob.restore", &number)
		return e
	})
	return
}

// createBlob stores the metadata of the item's next blob version.
func (s *Service) createBlob(ctx context.Context, subject, itemID string, in BlobInput, checkDuplicates bool) (*ent.Blob, error) {
	if err := s.RequireScope(ctx, subject, "document.write"); err != nil {
		return nil, err
	}
	if err := ValidateDocumentLanguages(in.Languages); err != nil {
		return nil, err
	}
	if !filenameValid(in.Filename) || in.Size < 0 || len(in.SHA256) != 64 || len(in.ContentType) > 255 || in.ContentType == "" || in.ObjectKey == "" {
		return nil, invalid("invalid blob metadata")
	}
	if !storage.SupportedDocumentType(in.ContentType) {
		return nil, storage.ErrUnsupportedFile
	}
	if checkDuplicates {
		if _, err := s.checkDuplicatePolicy(ctx, subject, itemID, in.SHA256); err != nil {
			return nil, err
		}
	}
	// Physical bytes are shared across the organization; filenames, media types,
	// language hints and file-version permissions remain specific to each item.
	stored, err := s.Client.Blob.Query().Where(blob.Sha256EQ(in.SHA256), blob.SizeEQ(in.Size), blob.AvailabilityNEQ(blob.AvailabilityReclaimed)).Order(ent.Asc(blob.FieldID)).First(ctx)
	if err == nil && stored.ObjectKey != in.ObjectKey {
		if _, err = s.Client.BlobDeletion.Create().SetObjectKey(in.ObjectKey).Save(ctx); err != nil {
			return nil, err
		}
		in.ObjectKey = stored.ObjectKey
	} else if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	version := 1
	last, err := s.Client.Blob.Query().Where(blob.ItemIDEQ(itemID)).Order(ent.Desc(blob.FieldVersion)).First(ctx)
	if err == nil {
		version = last.Version + 1
	} else if !ent.IsNotFound(err) {
		return nil, err
	}
	return s.Client.Blob.Create().SetItemID(itemID).SetVersion(version).SetObjectKey(in.ObjectKey).SetFilename(in.Filename).SetContentType(in.ContentType).SetLanguages(in.Languages).SetSize(in.Size).SetSha256(in.SHA256).Save(ctx)
}
func (s *Service) GetBlob(ctx context.Context, subject, id, blobID string) (*ent.Blob, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.Blob, error) { return t.GetBlob(ctx, subject, id, blobID) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
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
	if err != nil {
		return nil, err
	}
	if b.Availability == blob.AvailabilityReclaimed {
		return nil, ErrContentUnavailable
	}
	return b, nil
}

// FileVersion is one retained file of a library item.
type FileVersion struct {
	ID          string    `json:"id"`
	Version     int       `json:"version"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
	// CreatedBy is who uploaded or restored this version.
	CreatedBy string `json:"created_by"`
	// IsCurrent marks the file of the caller's visible revision.
	IsCurrent bool `json:"is_current"`
	// Availability is available, pending (reclamation requested) or reclaimed.
	// A reclaimed version keeps its identity and metadata; its bytes are gone.
	Availability string `json:"availability"`
}

// FileVersions lists an item's retained files, newest first. Draft readers see
// every version; ordinary readers see only the published file.
func (s *Service) FileVersions(ctx context.Context, subject, id string, after, limit int) (Page[FileVersion], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[FileVersion], error) { return t.FileVersions(ctx, subject, id, after, limit) })
	}
	if after < 0 {
		return Page[FileVersion]{}, invalid("file version cursor must be a positive version number")
	}
	current, err := s.GetBlob(ctx, subject, id, "")
	if errors.Is(err, ErrNotFound) {
		// The item exists and is readable, but has no visible file yet.
		if _, e := s.Get(ctx, subject, id); e != nil {
			return Page[FileVersion]{}, e
		}
		return Page[FileVersion]{Data: []FileVersion{}}, nil
	}
	if err != nil {
		return Page[FileVersion]{}, err
	}
	r, err := s.Client.Resource.Get(ctx, id)
	if err != nil {
		return Page[FileVersion]{}, err
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*ent.Resource{r})
	if err != nil {
		return Page[FileVersion]{}, err
	}
	q := s.Client.Blob.Query().Where(blob.ItemIDEQ(id))
	if !draft[id] {
		q.Where(blob.IDEQ(current.ID))
	}
	if after > 0 {
		q.Where(blob.VersionLT(after))
	}
	rows, err := q.Order(ent.Desc(blob.FieldVersion)).Limit(pageSize(limit) + 1).All(ctx)
	if err != nil {
		return Page[FileVersion]{}, err
	}
	ids := make([]string, len(rows))
	for i, b := range rows {
		ids[i] = b.ID
	}
	// The first revision that pinned a file records who uploaded or restored it.
	revisions, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.BlobIDIn(ids...)).Order(ent.Asc(itemrevision.FieldRevisionNumber)).All(ctx)
	if err != nil {
		return Page[FileVersion]{}, err
	}
	authors := map[string]string{}
	for _, rev := range revisions {
		if _, ok := authors[*rev.BlobID]; !ok {
			authors[*rev.BlobID] = rev.CreatedBy
		}
	}
	out := make([]FileVersion, len(rows))
	for i, b := range rows {
		out[i] = FileVersion{ID: b.ID, Version: b.Version, Filename: b.Filename, ContentType: b.ContentType, Size: b.Size, SHA256: b.Sha256, CreatedAt: b.CreatedAt, CreatedBy: authors[b.ID], IsCurrent: b.ID == current.ID, Availability: string(b.Availability)}
	}
	return numberedPage(out, limit, func(v FileVersion) int { return v.Version }), nil
}
