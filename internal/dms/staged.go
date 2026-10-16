package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/resource"
	"papergo/ent/stagedfile"
	"papergo/internal/model"
	"papergo/internal/storage"
)

// Staged files are files a workflow run produced for an item, such as a
// smaller image or a PDF composed from photos. They wait, with their bytes
// kept, until the run promotes one to the item's current file or discards it.
// A staged file records the file versions it was made from; promotion
// requires each to still be its item's current file, so a candidate made
// from an obsolete upload never replaces a newer one.

// StageInput is a file a run produced from source file versions.
type StageInput struct {
	// ItemID is the item the file would replace the current file of.
	ItemID      string
	RunID       string
	Sources     []model.StagedSource
	ObjectKey   string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	// Metrics describe how the file was made, for reviewers.
	Metrics json.RawMessage
}

// CurrentFile returns the file version of the item's head revision, which a
// workflow acting as subject (with write access) works on.
func (s *Service) CurrentFile(ctx context.Context, subject, itemID string) (*ent.Blob, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.Blob, error) { return t.CurrentFile(ctx, subject, itemID) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
	}
	item, err := s.authorize(ctx, subject, itemID, "read_draft")
	if err != nil {
		return nil, err
	}
	if item.HeadRevisionID == nil {
		return nil, invalid("the item has no file")
	}
	revision, err := s.Client.ItemRevision.Get(ctx, *item.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	if revision.BlobID == nil {
		return nil, invalid("the item has no file")
	}
	file, err := s.Client.Blob.Get(ctx, *revision.BlobID)
	if err == nil {
		file.Filename = revision.Filename
	}
	return file, err
}

func (s *Service) currentBlobID(ctx context.Context, itemID string) (string, error) {
	r, err := s.Client.Resource.Query().Where(resource.IDEQ(itemID), resource.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if r.HeadRevisionID == nil {
		return "", nil
	}
	rev, err := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
	if err != nil {
		return "", err
	}
	if rev.BlobID == nil {
		return "", nil
	}
	return *rev.BlobID, nil
}

// ItemFiles lists the file versions the surfaces of a live item reference:
// its current file, and the published one when it differs. A deleted or
// missing item has none.
func (s *Service) ItemFiles(ctx context.Context, itemID string) ([]string, error) {
	rows, err := s.Client.QueryContext(ctx, `SELECT DISTINCT v.blob_id FROM resources r JOIN item_revisions v ON v.id IN (r.head_revision_id, r.published_revision_id)
 WHERE r.id=? AND r.deleted_at IS NULL AND v.blob_id IS NOT NULL`, itemID)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		out = append(out, id)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

// fresh reports whether every source is still its item's current file.
func (s *Service) fresh(ctx context.Context, sources []model.StagedSource) (bool, error) {
	for _, src := range sources {
		current, err := s.currentBlobID(ctx, src.ItemID)
		if err == ErrNotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if current != src.BlobID {
			return false, nil
		}
	}
	return true, nil
}

// StageFile records a staged file. The subject needs write access to the
// item, and the sources must still be current.
func (s *Service) StageFile(ctx context.Context, subject string, in StageInput) (out *ent.StagedFile, err error) {
	err = s.write(ctx, func(t *Service) error {
		if _, e := t.canWriteFile(ctx, subject, in.ItemID); e != nil {
			return e
		}
		if len(in.Sources) == 0 {
			return invalid("a staged file needs its source file versions")
		}
		for _, src := range in.Sources {
			if _, e := t.authorize(ctx, subject, src.ItemID, "read_draft"); e != nil {
				return e
			}
		}
		if !storage.SupportedDocumentType(in.ContentType) {
			return storage.ErrUnsupportedFile
		}
		if !filenameValid(in.Filename) || in.Size < 0 || len(in.SHA256) != 64 || in.ObjectKey == "" || in.RunID == "" {
			return invalid("invalid staged file")
		}
		ok, e := t.fresh(ctx, in.Sources)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%w: a source file changed", ErrConflict)
		}
		r, e := t.Client.Resource.Get(ctx, in.ItemID)
		if e != nil {
			return e
		}
		metrics := in.Metrics
		if len(metrics) == 0 {
			metrics = json.RawMessage(`{}`)
		}
		out, e = t.Client.StagedFile.Create().SetWorkspaceID(r.WorkspaceID).SetItemID(in.ItemID).SetRunID(in.RunID).SetSources(in.Sources).
			SetObjectKey(in.ObjectKey).SetFilename(in.Filename).SetContentType(in.ContentType).SetSize(in.Size).SetSha256(in.SHA256).
			SetMetrics(metrics).SetCreatedBy(subject).Save(ctx)
		return e
	})
	return
}

// StagedFile returns a staged file to a subject who may read its item.
func (s *Service) StagedFile(ctx context.Context, subject, id string) (*ent.StagedFile, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.StagedFile, error) { return t.StagedFile(ctx, subject, id) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
	}
	f, err := s.Client.StagedFile.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = s.authorize(ctx, subject, f.ItemID, "read_draft"); err != nil {
		return nil, err
	}
	return f, nil
}

// StagedFresh reports whether a pending staged file's sources are current.
func (s *Service) StagedFresh(ctx context.Context, f *ent.StagedFile) (bool, error) {
	if f.State != stagedfile.StatePending {
		return false, nil
	}
	if !s.transaction {
		return read(ctx, s, func(t *Service) (bool, error) { return t.fresh(ctx, f.Sources) })
	}
	return s.fresh(ctx, f.Sources)
}

func (s *Service) runStaged(ctx context.Context, subject, id, runID string) (*ent.StagedFile, error) {
	f, err := s.Client.StagedFile.Get(ctx, id)
	if ent.IsNotFound(err) || err == nil && f.RunID != runID {
		return nil, invalid("the staged file is not this run's")
	}
	if err != nil {
		return nil, err
	}
	if _, err = s.canWriteFile(ctx, subject, f.ItemID); err != nil {
		return nil, err
	}
	return f, nil
}

// PromoteStagedFile makes a pending staged file of run the item's current
// file: a new file version from source workflow, with the item's
// metadata, revision, audit and events like an upload. It fails with
// ErrConflict when a source is no longer current. With recycleSources, the
// other source items go to the recycle bin in the same transaction, so a
// newer upload cannot arrive between the freshness check and their deletion.
func (s *Service) PromoteStagedFile(ctx context.Context, subject, id, runID string, recycleSources bool) (out *ent.Blob, err error) {
	err = s.write(ctx, func(t *Service) error {
		f, e := t.runStaged(ctx, subject, id, runID)
		if e != nil {
			return e
		}
		if f.State != stagedfile.StatePending {
			return fmt.Errorf("%w: the staged file is %s", ErrConflict, f.State)
		}
		ok, e := t.fresh(ctx, f.Sources)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%w: a source file changed after the file was made", ErrConflict)
		}
		r, e := t.Client.Resource.Get(ctx, f.ItemID)
		if e != nil {
			return e
		}
		// The new version keeps the replaced file's language hints.
		languages := ""
		for _, src := range f.Sources {
			if src.ItemID == f.ItemID {
				b, e := t.Client.Blob.Query().Where(blob.IDEQ(src.BlobID)).Only(ctx)
				if e != nil {
					return e
				}
				languages = b.Languages
			}
		}
		in := BlobInput{ObjectKey: f.ObjectKey, Filename: f.Filename, ContentType: f.ContentType, Size: f.Size, SHA256: f.Sha256, Languages: languages, Source: FileWorkflow}
		if out, e = t.writeBlob(ctx, subject, f.ItemID, r.Version, in, "blob.promote", nil); e != nil {
			return e
		}
		if recycleSources {
			recycled := map[string]bool{f.ItemID: true}
			for _, src := range f.Sources {
				if recycled[src.ItemID] {
					continue
				}
				r, e := t.authorize(ctx, subject, src.ItemID, "write")
				if e != nil {
					return e
				}
				if e = t.Delete(ctx, subject, r.ID, r.Version, DeleteOptions{}); e != nil {
					return e
				}
				recycled[src.ItemID] = true
			}
		}
		return t.Client.StagedFile.UpdateOne(f).SetState(stagedfile.StatePromoted).Exec(ctx)
	})
	return
}

// DiscardStagedFile discards a pending staged file of run; its bytes are
// freed after the storage grace period. Discarding twice is harmless.
func (s *Service) DiscardStagedFile(ctx context.Context, subject, id, runID string) error {
	return s.write(ctx, func(t *Service) error {
		f, e := t.runStaged(ctx, subject, id, runID)
		if e != nil {
			return e
		}
		switch f.State {
		case stagedfile.StateDiscarded:
			return nil
		case stagedfile.StatePromoted:
			return fmt.Errorf("%w: the staged file was promoted", ErrConflict)
		}
		return t.Client.StagedFile.UpdateOne(f).SetState(stagedfile.StateDiscarded).Exec(ctx)
	})
}

// DiscardRunStagedFiles discards the pending staged files of finished runs,
// inside the caller's write transaction.
func (s *Service) DiscardRunStagedFiles(ctx context.Context, runIDs ...string) error {
	if len(runIDs) == 0 {
		return nil
	}
	_, err := s.Client.StagedFile.Update().Where(stagedfile.RunIDIn(runIDs...), stagedfile.StateEQ(stagedfile.StatePending)).
		SetState(stagedfile.StateDiscarded).SetUpdatedAt(time.Now().UTC()).Save(ctx)
	return err
}
