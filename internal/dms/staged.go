package dms

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/model"
	"papergo/internal/storage"
)

// Artifacts are files operations make from file versions for an item, such
// as a smaller image or a PDF composed from photos. An artifact belongs to
// no workflow: it is kept for ArtifactRetention, so an identical request
// reuses it, and longer while a staged file holds it. A consumer, a
// workflow run or anyone with write access to the item, stages an artifact
// to review it, then promotes the staged file to the item's current file
// or discards it; a file a run staged answers only that run. An artifact records the file versions it was made from;
// staging and promotion require each to still be its item's current file,
// so a candidate made from an obsolete upload never replaces a newer one.

// ArtifactRetention is how long an unstaged artifact is kept.
const ArtifactRetention = 7 * 24 * time.Hour

// ArtifactInput is a file an operation made from source file versions.
type ArtifactInput struct {
	// ItemID is the item the file would replace the current file of.
	ItemID      string
	OperationID string
	// InputKey identifies what the file was made from and how, for reuse.
	InputKey    string
	Sources     []model.ArtifactSource
	ObjectKey   string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	// Metrics describe how the file was made, for reviewers.
	Metrics json.RawMessage
}

// Staged is a staged file with the artifact it holds.
type Staged struct {
	blobs.StagedFile
	Artifact blobs.Artifact
}

// CurrentFile returns the file version of the item's head revision, which a
// workflow acting as subject (with write access) works on.
func (s *Service) CurrentFile(ctx context.Context, subject, itemID string) (*blobs.Blob, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*blobs.Blob, error) { return t.CurrentFile(ctx, subject, itemID) })
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
	revision, err := s.revision(ctx, *item.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	if revision.BlobID == nil {
		return nil, invalid("the item has no file")
	}
	file, err := s.DAL.Blobs().Blob(ctx, *revision.BlobID)
	if err != nil {
		return nil, err
	}
	file.Filename = revision.Filename
	return &file, nil
}

func (s *Service) currentBlobID(ctx context.Context, itemID string) (string, error) {
	r, err := s.DAL.Resources().Resource(ctx, itemID)
	if dberr.IsNotFound(err) || err == nil && r.DeletedAt != nil {
		return "", ErrNotFound
	}
	if err != nil || r.HeadRevisionID == nil {
		return "", err
	}
	rev, err := s.revision(ctx, *r.HeadRevisionID)
	if err != nil || rev.BlobID == nil {
		return "", err
	}
	return *rev.BlobID, nil
}

// ItemFiles lists the file versions the surfaces of a live item reference:
// its current file, and the published one when it differs. A deleted or
// missing item has none.
func (s *Service) ItemFiles(ctx context.Context, itemID string) ([]string, error) {
	r, err := s.DAL.Resources().Resource(ctx, itemID)
	if dberr.IsNotFound(err) || err == nil && r.DeletedAt != nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range []*string{r.HeadRevisionID, r.PublishedRevisionID} {
		if id == nil {
			continue
		}
		rev, err := s.revision(ctx, *id)
		if err != nil {
			return nil, err
		}
		if rev.BlobID != nil && !slices.Contains(out, *rev.BlobID) {
			out = append(out, *rev.BlobID)
		}
	}
	return out, nil
}

// fresh reports whether every source is still its item's current file.
func (s *Service) fresh(ctx context.Context, sources []model.ArtifactSource) (bool, error) {
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

// AddArtifact records an artifact the subject's operation made. The
// subject needs write access to the item and read access to the sources,
// which must still be current.
func (s *Service) AddArtifact(ctx context.Context, subject string, in ArtifactInput) (out *blobs.Artifact, err error) {
	err = s.write(ctx, func(t *Service) error {
		if _, e := t.canWriteFile(ctx, subject, in.ItemID); e != nil {
			return e
		}
		if len(in.Sources) == 0 {
			return invalid("an artifact needs its source file versions")
		}
		for _, src := range in.Sources {
			if _, e := t.authorize(ctx, subject, src.ItemID, "read_draft"); e != nil {
				return e
			}
		}
		if !storage.SupportedDocumentType(in.ContentType) {
			return storage.ErrUnsupportedFile
		}
		if !filenameValid(in.Filename) || in.Size < 0 || len(in.SHA256) != 64 || in.ObjectKey == "" || len(in.InputKey) != 64 {
			return invalid("invalid artifact")
		}
		ok, e := t.fresh(ctx, in.Sources)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%w: a source file changed", ErrConflict)
		}
		r, e := t.DAL.Resources().Resource(ctx, in.ItemID)
		if e != nil {
			return e
		}
		a, e := t.DAL.Blobs().AddArtifact(ctx, blobs.Artifact{WorkspaceID: r.WorkspaceID, ItemID: in.ItemID, OperationID: in.OperationID, InputKey: in.InputKey,
			Sources: in.Sources, ObjectKey: in.ObjectKey, Filename: in.Filename, ContentType: in.ContentType, Size: in.Size, Sha256: in.SHA256,
			Metrics: in.Metrics, CreatedBy: subject, ExpiresAt: time.Now().Add(ArtifactRetention)})
		out = &a
		return e
	})
	return
}

// ReusableArtifact returns an unexpired artifact of the item made from the
// same input whose sources are still current, or nil.
func (s *Service) ReusableArtifact(ctx context.Context, itemID, inputKey string) (*blobs.Artifact, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*blobs.Artifact, error) { return t.ReusableArtifact(ctx, itemID, inputKey) })
	}
	a, err := s.DAL.Blobs().ReusableArtifact(ctx, itemID, inputKey)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.fresh(ctx, a.Sources); err != nil || !ok {
		return nil, err
	}
	return &a, nil
}

// Artifact returns an artifact to a subject who may read its item.
func (s *Service) Artifact(ctx context.Context, subject, id string) (*blobs.Artifact, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*blobs.Artifact, error) { return t.Artifact(ctx, subject, id) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
	}
	a, err := s.artifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err = s.authorize(ctx, subject, a.ItemID, "read_draft"); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) artifact(ctx context.Context, id string) (*blobs.Artifact, error) {
	a, err := s.DAL.Blobs().Artifact(ctx, id)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// StageArtifact stages an artifact for the subject: for the workflow run
// runID, or without one for anyone with write access to the item. Its
// sources must still be current.
func (s *Service) StageArtifact(ctx context.Context, subject, id, runID string) (out *Staged, err error) {
	err = s.write(ctx, func(t *Service) error {
		a, e := t.artifact(ctx, id)
		if e != nil {
			return e
		}
		if _, e = t.canWriteFile(ctx, subject, a.ItemID); e != nil {
			return e
		}
		ok, e := t.fresh(ctx, a.Sources)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%w: a source file changed after the file was made", ErrConflict)
		}
		f, e := t.DAL.Blobs().Stage(ctx, blobs.StagedFile{WorkspaceID: a.WorkspaceID, ItemID: a.ItemID, ArtifactID: a.ID, RunID: runID, CreatedBy: subject})
		out = &Staged{StagedFile: f, Artifact: *a}
		return e
	})
	return
}

// StagedFile returns a staged file to a subject who may read its item.
func (s *Service) StagedFile(ctx context.Context, subject, id string) (*Staged, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*Staged, error) { return t.StagedFile(ctx, subject, id) })
	}
	if err := s.RequireScope(ctx, subject, "document.read"); err != nil {
		return nil, err
	}
	f, err := s.stagedFile(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err = s.authorize(ctx, subject, f.ItemID, "read_draft"); err != nil {
		return nil, err
	}
	return f, nil
}

// StagedFresh reports whether a pending staged file's sources are current.
func (s *Service) StagedFresh(ctx context.Context, id string) (bool, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (bool, error) { return t.StagedFresh(ctx, id) })
	}
	f, err := s.stagedFile(ctx, id)
	if err != nil {
		return false, err
	}
	if f.State != blobs.StagedPending {
		return false, nil
	}
	return s.fresh(ctx, f.Artifact.Sources)
}

func (s *Service) stagedFile(ctx context.Context, id string) (*Staged, error) {
	f, err := s.DAL.Blobs().StagedFile(ctx, id)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a, err := s.DAL.Blobs().Artifact(ctx, f.ArtifactID)
	if err != nil {
		return nil, err
	}
	return &Staged{StagedFile: f, Artifact: a}, nil
}

// consumerStaged returns a staged file the subject may promote or discard:
// a workflow run's only to that run, one staged without a run to callers
// without one, and to either only with write access to the item.
func (s *Service) consumerStaged(ctx context.Context, subject, id, runID string) (*Staged, error) {
	f, err := s.stagedFile(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.RunID != runID {
		return nil, invalid("the staged file is another consumer's")
	}
	if _, err = s.canWriteFile(ctx, subject, f.ItemID); err != nil {
		return nil, err
	}
	return f, nil
}

// PromoteStagedFile makes a pending staged file's artifact the item's
// current file: a new file version from source workflow, with the item's
// metadata, revision, audit and events like an upload. It fails with
// ErrConflict when a source is no longer current. With recycleSources, the
// other source items go to the recycle bin in the same transaction, so a
// newer upload cannot arrive between the freshness check and their deletion.
func (s *Service) PromoteStagedFile(ctx context.Context, subject, id, runID string, recycleSources bool) (out *blobs.Blob, err error) {
	err = s.write(ctx, func(t *Service) error {
		f, e := t.consumerStaged(ctx, subject, id, runID)
		if e != nil {
			return e
		}
		if f.State != blobs.StagedPending {
			return fmt.Errorf("%w: the staged file is %s", ErrConflict, f.State)
		}
		a := f.Artifact
		ok, e := t.fresh(ctx, a.Sources)
		if e != nil {
			return e
		}
		if !ok {
			return fmt.Errorf("%w: a source file changed after the file was made", ErrConflict)
		}
		r, e := t.DAL.Resources().Resource(ctx, f.ItemID)
		if e != nil {
			return e
		}
		// The new version keeps the replaced file's language hints.
		languages := ""
		for _, src := range a.Sources {
			if src.ItemID == f.ItemID {
				b, e := t.DAL.Blobs().Blob(ctx, src.BlobID)
				if e != nil {
					return e
				}
				languages = b.Languages
			}
		}
		in := BlobInput{ObjectKey: a.ObjectKey, Filename: a.Filename, ContentType: a.ContentType, Size: a.Size, SHA256: a.Sha256, Languages: languages, Source: FileWorkflow}
		if out, e = t.writeBlob(ctx, subject, f.ItemID, r.Version, in, "blob.promote", nil); e != nil {
			return e
		}
		if recycleSources {
			recycled := map[string]bool{f.ItemID: true}
			for _, src := range a.Sources {
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
		return t.DAL.Blobs().SetStagedState(ctx, f.ID, blobs.StagedPromoted)
	})
	return
}

// DiscardStagedFile discards a pending staged file; its artifact stays for
// reuse until it expires. Discarding twice is harmless.
func (s *Service) DiscardStagedFile(ctx context.Context, subject, id, runID string) error {
	return s.write(ctx, func(t *Service) error {
		f, e := t.consumerStaged(ctx, subject, id, runID)
		if e != nil {
			return e
		}
		switch f.State {
		case blobs.StagedDiscarded:
			return nil
		case blobs.StagedPromoted:
			return fmt.Errorf("%w: the staged file was promoted", ErrConflict)
		}
		return t.DAL.Blobs().SetStagedState(ctx, f.ID, blobs.StagedDiscarded)
	})
}

// DiscardStagedFiles discards the pending staged files sel selects, such
// as those of runs that ended or of a deleted item, inside the caller's
// write transaction.
func (s *Service) DiscardStagedFiles(ctx context.Context, sel blobs.StagedSelection) error {
	return s.DAL.Blobs().DiscardStaged(ctx, sel)
}

// artifactPage is how many expired artifacts one retention pass deletes at
// a time.
const artifactPage = 200

// CollectArtifacts deletes the artifacts that expired by now and no
// pending staged file holds; the object sweep then frees their bytes. It
// is a maintenance task.
func (s *Service) CollectArtifacts(ctx context.Context, now time.Time) error {
	for {
		var n int
		err := s.write(ctx, func(t *Service) error {
			var err error
			n, err = t.DAL.Blobs().DeleteExpiredArtifacts(ctx, now, artifactPage)
			return err
		})
		if err != nil || n < artifactPage {
			return err
		}
	}
}
