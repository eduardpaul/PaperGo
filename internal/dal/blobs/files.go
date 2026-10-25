package blobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

// MediaType is the format of a file's bytes, named by storage.Identify.
type MediaType string

const (
	MediaPDF  MediaType = "application/pdf"
	MediaTIFF MediaType = "image/tiff"
	MediaJPEG MediaType = "image/jpeg"
	MediaPNG  MediaType = "image/png"
	MediaWebP MediaType = "image/webp"
)

// Valid reports whether m has the shape the schema checks. Files of every
// type are stored; the constants name the types core processes.
func (m MediaType) Valid() bool {
	return len(m) >= 3 && len(m) <= 127 && strings.Count(string(m), "/") == 1
}

// StagedState is where a staged file stands.
type StagedState string

const (
	StagedPending   StagedState = "pending"
	StagedPromoted  StagedState = "promoted"
	StagedDiscarded StagedState = "discarded"
)

func (s StagedState) Valid() bool {
	return s == StagedPending || s == StagedPromoted || s == StagedDiscarded
}

// Blob is one version of an item's file. Items sharing bytes share the
// stored object; filenames, media types and language hints stay per blob.
type Blob struct {
	ID           string       `json:"id,omitempty"`
	CreatedAt    time.Time    `json:"created_at,omitempty"`
	ItemID       string       `json:"item_id,omitempty"`
	Version      int          `json:"version,omitempty"`
	ObjectKey    string       `json:"object_key,omitempty"`
	Filename     string       `json:"filename,omitempty"`
	ContentType  MediaType    `json:"content_type,omitempty"`
	Languages    string       `json:"languages,omitempty"`
	Size         int64        `json:"size,omitempty"`
	Sha256       string       `json:"sha256,omitempty"`
	Availability Availability `json:"availability,omitempty"`
	ReclaimAfter *time.Time   `json:"reclaim_after,omitempty"`
}

// BlobOrder sorts selected blobs.
type BlobOrder uint8

const (
	ByID BlobOrder = iota
	// NewestFirst sorts by version, newest first.
	NewestFirst
)

// BlobFilter selects blobs. Zero fields do not filter.
type BlobFilter struct {
	IDs           []string
	ItemID        string
	Version       int
	BeforeVersion int
	// DueBy selects pending blobs whose grace period is unset or over.
	DueBy   *time.Time
	AfterID string
	Order   BlobOrder
	Limit   int
}

// Artifact is a file an operation made from file versions for an item,
// such as a smaller image or a PDF composed from photos. It is kept until
// ExpiresAt, and longer while a pending staged file holds it, so identical
// requests (the same InputKey for the item) reuse it.
type Artifact struct {
	ID, WorkspaceID, ItemID, OperationID, InputKey string
	Sources                                        []model.ArtifactSource
	ObjectKey, Filename                            string
	ContentType                                    string
	Size                                           int64
	Sha256                                         string
	Metrics                                        json.RawMessage
	CreatedBy                                      string
	CreatedAt, ExpiresAt                           time.Time
}

// StagedFile is a consumer's claim on an artifact for an item: a workflow
// run's, or without RunID a person's or a client's, until it promotes the
// artifact to the item's next file version or discards it.
type StagedFile struct {
	ID, WorkspaceID, ItemID, ArtifactID, RunID string
	State                                      StagedState
	CreatedBy                                  string
	CreatedAt, UpdatedAt                       time.Time
}

// FileQueries are the statements a driver runs for blobs, artifacts and
// staged files.
type FileQueries interface {
	refs.Queries
	Blob(ctx context.Context, id string) (Blob, error)
	Blobs(ctx context.Context, f BlobFilter) ([]Blob, error)
	// SharedBlob returns the oldest unreclaimed blob holding the bytes.
	SharedBlob(ctx context.Context, sha256 string, size int64) (Blob, error)
	// LatestBlobVersion is the highest version of an item's blobs, or 0.
	LatestBlobVersion(ctx context.Context, itemID string) (int, error)
	InsertBlob(ctx context.Context, b Blob) error
	SetBlobAvailability(ctx context.Context, id string, a Availability, reclaimAfter *time.Time) error
	Artifact(ctx context.Context, id string) (Artifact, error)
	InsertArtifact(ctx context.Context, a Artifact) error
	// ReusableArtifact returns the newest artifact of the item with the
	// input key that expires after now.
	ReusableArtifact(ctx context.Context, itemID, inputKey string, now time.Time) (Artifact, error)
	// DeleteExpiredArtifacts deletes up to limit artifacts that expired by
	// now and no pending staged file holds, and returns how many.
	DeleteExpiredArtifacts(ctx context.Context, now time.Time, limit int) (int, error)
	StagedFile(ctx context.Context, id string) (StagedFile, error)
	InsertStagedFile(ctx context.Context, f StagedFile) error
	SetStagedState(ctx context.Context, id string, state StagedState, at time.Time) error
	// BlobStaged reports whether the artifact of a pending staged file
	// stores its bytes in the object or was made from the blob.
	BlobStaged(ctx context.Context, objectKey, blobID string) (bool, error)
	BlobLeased(ctx context.Context, blobID string) (bool, error)
	// DiscardStaged discards the pending staged files s selects.
	DiscardStaged(ctx context.Context, s StagedSelection, at time.Time) error
}

// StagedSelection selects staged files: those of runs, of an item, or of
// the runs of a workflow, except those of ExceptRunID. It selects at least
// one of them.
type StagedSelection struct {
	RunIDs                          []string
	ItemID, WorkflowID, ExceptRunID string
}

// Blob returns a blob, or dberr.ErrNotFound.
func (r *Repo) Blob(ctx context.Context, id string) (Blob, error) { return r.read.Blob(ctx, id) }

func (r *Repo) Blobs(ctx context.Context, f BlobFilter) ([]Blob, error) {
	if f.IDs != nil && len(f.IDs) == 0 {
		return []Blob{}, nil
	}
	return r.read.Blobs(ctx, f)
}

// Count counts the blobs f selects.
func (r *Repo) Count(ctx context.Context, f BlobFilter) (int, error) {
	rows, err := r.Blobs(ctx, f)
	return len(rows), err
}

// ItemBlob returns an item's blob, or dberr.ErrNotFound.
func (r *Repo) ItemBlob(ctx context.Context, itemID, id string) (Blob, error) {
	return one(r.read.Blobs(ctx, BlobFilter{ItemID: itemID, IDs: []string{id}, Limit: 1}))
}

// ItemVersion returns an item's blob with a version, or dberr.ErrNotFound.
func (r *Repo) ItemVersion(ctx context.Context, itemID string, version int) (Blob, error) {
	return one(r.read.Blobs(ctx, BlobFilter{ItemID: itemID, Version: version, Limit: 1}))
}

// AddBlob records an item's next file version. The item is a live library
// item. Bytes another unreclaimed blob holds reuse that blob's object, and
// the object b brought is queued for deletion.
func (r *Repo) AddBlob(ctx context.Context, b Blob) (Blob, error) {
	if !b.ContentType.Valid() {
		return Blob{}, fmt.Errorf("%w: unsupported media type %q", dberr.ErrCheckViolation, b.ContentType)
	}
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, b.ItemID); err != nil {
			return err
		}
		item, err := q.Resource(ctx, b.ItemID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || item.Kind != refs.KindItem {
			return fmt.Errorf("%w: blobs belong to library items", dberr.ErrInvalidContainment)
		}
		if item.Deleted {
			return fmt.Errorf("%w: deleted resources cannot have new blobs", dberr.ErrImmutable)
		}
		library, err := q.Resource(ctx, item.ContainerID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || library.Kind != refs.KindLibrary {
			return fmt.Errorf("%w: blobs belong to library items", dberr.ErrInvalidContainment)
		}
		shared, err := q.SharedBlob(ctx, b.Sha256, b.Size)
		if err == nil && shared.ObjectKey != b.ObjectKey {
			if err := q.QueueDeletion(ctx, Deletion{ID: uuid.Must(uuid.NewV7()).String(), ObjectKey: b.ObjectKey, CreatedAt: now()}); err != nil {
				return err
			}
			b.ObjectKey = shared.ObjectKey
		} else if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		latest, err := q.LatestBlobVersion(ctx, b.ItemID)
		if err != nil {
			return err
		}
		if b.ID == "" {
			b.ID = uuid.NewString()
		}
		b.Version, b.Availability, b.ReclaimAfter, b.CreatedAt = latest+1, AvailabilityAvailable, nil, now()
		return q.InsertBlob(ctx, b)
	})
	return b, err
}

// SetAvailability moves a blob towards reclamation: available becomes
// pending, pending becomes reclaimed, and reclaimed stays. A pending blob
// keeps or changes its grace period, ending at reclaimAfter.
func (r *Repo) SetAvailability(ctx context.Context, id string, a Availability, reclaimAfter *time.Time) (Blob, error) {
	var out Blob
	err := r.write(ctx, func(q Queries) error {
		b, err := q.Blob(ctx, id)
		if err != nil {
			return err
		}
		if b.Availability == AvailabilityReclaimed {
			return fmt.Errorf("%w: reclaimed file versions stay reclaimed", dberr.ErrInvalidStateTransition)
		}
		if b.Availability != a && !(b.Availability == AvailabilityAvailable && a == AvailabilityPending || b.Availability == AvailabilityPending && a == AvailabilityReclaimed) {
			return fmt.Errorf("%w: invalid file availability transition %s -> %s", dberr.ErrInvalidStateTransition, b.Availability, a)
		}
		if reclaimAfter != nil {
			at := reclaimAfter.UTC()
			reclaimAfter = &at
		}
		b.Availability, b.ReclaimAfter = a, reclaimAfter
		out = b
		return q.SetBlobAvailability(ctx, id, a, reclaimAfter)
	})
	return out, err
}

// Staged reports whether a pending staged file needs a blob: its artifact
// stores its bytes in the blob's object or was made from the blob.
func (r *Repo) Staged(ctx context.Context, b Blob) (bool, error) {
	return r.read.BlobStaged(ctx, b.ObjectKey, b.ID)
}

// Leased reports whether an owner such as an open wait keeps a blob.
func (r *Repo) Leased(ctx context.Context, blobID string) (bool, error) {
	return r.read.BlobLeased(ctx, blobID)
}

// Artifact returns an artifact, or dberr.ErrNotFound.
func (r *Repo) Artifact(ctx context.Context, id string) (Artifact, error) {
	return r.read.Artifact(ctx, id)
}

// AddArtifact records an artifact.
func (r *Repo) AddArtifact(ctx context.Context, a Artifact) (Artifact, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Sources == nil {
		a.Sources = []model.ArtifactSource{}
	}
	if len(a.Metrics) == 0 {
		a.Metrics = json.RawMessage(`{}`)
	}
	a.CreatedAt, a.ExpiresAt = now(), a.ExpiresAt.UTC()
	return a, r.read.InsertArtifact(ctx, a)
}

// ReusableArtifact returns the newest unexpired artifact of the item made
// from the same input, or dberr.ErrNotFound.
func (r *Repo) ReusableArtifact(ctx context.Context, itemID, inputKey string) (Artifact, error) {
	return r.read.ReusableArtifact(ctx, itemID, inputKey, now())
}

// DeleteExpiredArtifacts deletes up to limit expired artifacts no pending
// staged file holds, with their staged files, and returns how many.
func (r *Repo) DeleteExpiredArtifacts(ctx context.Context, at time.Time, limit int) (int, error) {
	return r.read.DeleteExpiredArtifacts(ctx, at.UTC(), limit)
}

// StagedFile returns a staged file, or dberr.ErrNotFound.
func (r *Repo) StagedFile(ctx context.Context, id string) (StagedFile, error) {
	return r.read.StagedFile(ctx, id)
}

// Stage records a pending staged file of an artifact.
func (r *Repo) Stage(ctx context.Context, f StagedFile) (StagedFile, error) {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	f.State, f.CreatedAt = StagedPending, now()
	f.UpdatedAt = f.CreatedAt
	return f, r.read.InsertStagedFile(ctx, f)
}

// SetStagedState promotes or discards a staged file.
func (r *Repo) SetStagedState(ctx context.Context, id string, state StagedState) error {
	if !state.Valid() {
		return fmt.Errorf("%w: invalid staged state %q", dberr.ErrCheckViolation, state)
	}
	return r.read.SetStagedState(ctx, id, state, now())
}

// DiscardStaged discards the pending staged files s selects.
func (r *Repo) DiscardStaged(ctx context.Context, s StagedSelection) error {
	if len(s.RunIDs) == 0 && s.ItemID == "" && s.WorkflowID == "" {
		return nil
	}
	return r.read.DiscardStaged(ctx, s, now())
}

func one[T any](rows []T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, dberr.ErrNotFound
	}
	return rows[0], nil
}
