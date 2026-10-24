package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/storage"
	"time"
)

func (s *Service) Publish(ctx context.Context, subject, id string, version int) (out *revisions.Publication, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.publish(ctx, subject, id, version)
		return e
	})
	return
}

// publish applies the transition inside the caller's write transaction.
func (s *Service) publish(ctx context.Context, subject, id string, version int) (*revisions.Publication, error) {
	r, e := s.authorize(ctx, subject, id, "publish")
	if e != nil {
		return nil, e
	}
	if r.Kind != refs.KindItem || r.ContainerID == nil || r.HeadRevisionID == nil {
		return nil, invalid("only items can be published")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	c, e := s.DAL.Resources().Resource(ctx, *r.ContainerID)
	if e != nil {
		return nil, e
	}
	if !c.PublishingEnabled {
		return nil, invalid("publishing is automatic for this container")
	}
	rev, e := s.revision(ctx, *r.HeadRevisionID)
	if e != nil {
		return nil, e
	}
	if c.Kind == refs.KindLibrary && rev.BlobID == nil {
		return nil, invalid("library items need a blob before explicit publishing")
	}
	out, e := s.publishRevision(ctx, subject, r, rev, true, nil)
	if e != nil {
		return nil, e
	}
	return out, s.audit(ctx, subject, "item.publish", r, map[string]any{"revision_id": rev.ID, "publication_id": out.ID})
}
func (s *Service) Unpublish(ctx context.Context, subject, id string, version int) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		var e error
		out, e = t.unpublish(ctx, subject, id, version)
		return e
	})
	return
}

// unpublish applies the transition inside the caller's write transaction.
func (s *Service) unpublish(ctx context.Context, subject, id string, version int) (*resources.Resource, error) {
	r, e := s.authorize(ctx, subject, id, "publish")
	if e != nil {
		return nil, e
	}
	if r.Kind != refs.KindItem || r.ContainerID == nil {
		return nil, invalid("only items can be unpublished")
	}
	if r.Version != version {
		return nil, ErrPreconditionFailed
	}
	if r.PublishedRevisionID == nil {
		return nil, ErrConflict
	}
	c, e := s.DAL.Resources().Resource(ctx, *r.ContainerID)
	if e != nil {
		return nil, e
	}
	if !c.PublishingEnabled {
		return nil, invalid("enable publishing before unpublishing items")
	}
	rev, e := s.revision(ctx, *r.PublishedRevisionID)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(map[string]any{"revision_id": rev.ID})
	if e != nil {
		return nil, e
	}
	if _, e = s.DAL.Revisions().Publish(ctx, revisions.Publication{ItemID: id, RevisionID: &rev.ID, Version: version, Action: revisions.ActionUnpublish, PublishedBy: subject, Snapshot: raw}); e != nil {
		return nil, e
	}
	if e = s.DAL.Surfaces().Remove(ctx, id, refs.SurfacePublished); e != nil {
		return nil, e
	}
	e = s.DAL.Resources().Unpublish(ctx, id, version)
	if e != nil && !errors.Is(e, dberr.ErrConcurrentUpdate) {
		return nil, e
	}
	if e != nil {
		return nil, ErrPreconditionFailed
	}
	out, e := s.DAL.Resources().Resource(ctx, id)
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
func (s *Service) Publications(ctx context.Context, subject, id string, after, limit int) (Page[*revisions.Publication], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*revisions.Publication], error) {
			return t.Publications(ctx, subject, id, after, limit)
		})
	}
	r, err := s.Get(ctx, subject, id)
	if err != nil {
		return Page[*revisions.Publication]{}, err
	}
	f := revisions.PublicationFilter{ItemID: id, AfterVersion: after, Limit: pageSize(limit) + 1}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*resources.Resource{r})
	if err != nil {
		return Page[*revisions.Publication]{}, err
	}
	if !draft[id] {
		if r.PublishedRevisionID == nil {
			return Page[*revisions.Publication]{Data: []*revisions.Publication{}}, nil
		}
		f.RevisionID, f.Action = *r.PublishedRevisionID, revisions.ActionPublish
	}
	found, err := s.DAL.Revisions().Publications(ctx, f)
	if err != nil {
		return Page[*revisions.Publication]{}, err
	}
	rows := make([]*revisions.Publication, len(found))
	for i := range found {
		rows[i] = &found[i]
	}
	return numberedPage(rows, limit, func(v *revisions.Publication) int { return v.Version }), nil
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
func (s *Service) emitFileChange(ctx context.Context, actor string, r *resources.Resource, b *blobs.Blob, rev *revisions.Revision, source string, previousBlobID *string) {
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
func (s *Service) canWriteFile(ctx context.Context, subject, id string) (*resources.Resource, error) {
	if err := s.RequireScope(ctx, subject, "document.write"); err != nil {
		return nil, err
	}
	r, err := s.authorize(ctx, subject, id, "write")
	if err != nil {
		return nil, err
	}
	if r.Kind != refs.KindItem || r.ContainerID == nil {
		return nil, invalid("blob must belong to a library item")
	}
	c, err := s.DAL.Resources().Resource(ctx, *r.ContainerID)
	if err != nil {
		return nil, err
	}
	if c.Kind != refs.KindLibrary {
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
func (s *Service) attachBlob(ctx context.Context, subject, id string, version int, in BlobInput) (*blobs.Blob, error) {
	return s.writeBlob(ctx, subject, id, version, in, "blob.attach", nil)
}

func (s *Service) writeBlob(ctx context.Context, subject, id string, version int, in BlobInput, action string, sourceVersion *int) (*blobs.Blob, error) {
	if e := s.CanUpload(ctx, subject, id, version); e != nil {
		return nil, e
	}
	r, e := s.DAL.Resources().Resource(ctx, id)
	if e != nil {
		return nil, e
	}
	if e = s.overlayHead(ctx, r); e != nil {
		return nil, e
	}
	e = s.DAL.Resources().Touch(ctx, id, version, "")
	if e != nil && !errors.Is(e, dberr.ErrConcurrentUpdate) {
		return nil, e
	}
	if e != nil {
		return nil, ErrPreconditionFailed
	}
	r.Version++
	out, e := s.createBlob(ctx, subject, id, in, sourceVersion == nil)
	if e != nil {
		return nil, e
	}
	var previousBlobID *string
	if r.HeadRevisionID != nil {
		previous, err := s.revision(ctx, *r.HeadRevisionID)
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
func (s *Service) RestoreBlob(ctx context.Context, subject, id string, number, version int) (out *blobs.Blob, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.CanUpload(ctx, subject, id, version); e != nil {
			return e
		}
		source, e := t.DAL.Blobs().ItemVersion(ctx, id, number)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if source.Availability == blobs.AvailabilityReclaimed {
			return ErrContentUnavailable
		}
		out, e = t.writeBlob(ctx, subject, id, version, BlobInput{ObjectKey: source.ObjectKey, Filename: source.Filename, ContentType: string(source.ContentType), Size: source.Size, SHA256: source.Sha256, Source: FileRestore}, "blob.restore", &number)
		return e
	})
	return
}

// blob returns a file version.
func (s *Service) blob(ctx context.Context, id string) (*blobs.Blob, error) {
	b, err := s.DAL.Blobs().Blob(ctx, id)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// createBlob stores the metadata of the item's next blob version.
func (s *Service) createBlob(ctx context.Context, subject, itemID string, in BlobInput, checkDuplicates bool) (*blobs.Blob, error) {
	if err := s.RequireScope(ctx, subject, "document.write"); err != nil {
		return nil, err
	}
	if err := ValidateDocumentLanguages(in.Languages); err != nil {
		return nil, err
	}
	if !filenameValid(in.Filename) || in.Size < 0 || len(in.SHA256) != 64 || len(in.ContentType) > 255 || in.ContentType == "" || in.ObjectKey == "" {
		return nil, invalid("invalid blob metadata")
	}
	if !storage.ValidMediaType(in.ContentType) {
		return nil, invalid("invalid media type")
	}
	if checkDuplicates {
		if _, err := s.checkDuplicatePolicy(ctx, subject, itemID, in.SHA256); err != nil {
			return nil, err
		}
	}
	// Physical bytes are shared across the organization; filenames, media types,
	// language hints and file-version permissions remain specific to each item.
	b, err := s.DAL.Blobs().AddBlob(ctx, blobs.Blob{ItemID: itemID, ObjectKey: in.ObjectKey, Filename: in.Filename, ContentType: blobs.MediaType(in.ContentType),
		Languages: in.Languages, Size: in.Size, Sha256: in.SHA256})
	if err != nil {
		return nil, err
	}
	return &b, nil
}
func (s *Service) GetBlob(ctx context.Context, subject, id, blobID string) (*blobs.Blob, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*blobs.Blob, error) { return t.GetBlob(ctx, subject, id, blobID) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
	}
	r, err := s.Get(ctx, subject, id)
	if err != nil {
		return nil, err
	}
	if r.Kind != refs.KindItem {
		return nil, ErrNotFound
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*resources.Resource{r})
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
	rev, err := s.revision(ctx, *selected)
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
	b, err := s.DAL.Blobs().ItemBlob(ctx, id, blobID)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if b.Availability == blobs.AvailabilityReclaimed {
		return nil, ErrContentUnavailable
	}
	if rev.BlobID != nil && *rev.BlobID == b.ID {
		b.Filename = rev.Filename
	}
	return &b, nil
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
	r, err := s.DAL.Resources().Resource(ctx, id)
	if err != nil {
		return Page[FileVersion]{}, err
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*resources.Resource{r})
	if err != nil {
		return Page[FileVersion]{}, err
	}
	f := blobs.BlobFilter{ItemID: id, BeforeVersion: after, Order: blobs.NewestFirst, Limit: pageSize(limit) + 1}
	if !draft[id] {
		f.IDs = []string{current.ID}
	}
	rows, err := s.DAL.Blobs().Blobs(ctx, f)
	if err != nil {
		return Page[FileVersion]{}, err
	}
	ids := make([]string, len(rows))
	for i, b := range rows {
		ids[i] = b.ID
	}
	// The first revision that pinned a file records who uploaded or restored it.
	pinned, err := s.DAL.Revisions().Revisions(ctx, revisions.Filter{ItemID: id, BlobIDs: ids})
	if err != nil {
		return Page[FileVersion]{}, err
	}
	authors := map[string]string{}
	for _, rev := range pinned {
		if _, ok := authors[*rev.BlobID]; !ok {
			authors[*rev.BlobID] = rev.CreatedBy
		}
	}
	out := make([]FileVersion, len(rows))
	for i, b := range rows {
		if b.ID == current.ID {
			b.Filename = current.Filename
		}
		out[i] = FileVersion{ID: b.ID, Version: b.Version, Filename: b.Filename, ContentType: string(b.ContentType), Size: b.Size, SHA256: b.Sha256, CreatedAt: b.CreatedAt, CreatedBy: authors[b.ID], IsCurrent: b.ID == current.ID, Availability: string(b.Availability)}
	}
	return numberedPage(out, limit, func(v FileVersion) int { return v.Version }), nil
}
