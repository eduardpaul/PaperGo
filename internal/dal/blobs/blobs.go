// Package blobs owns stored file bytes shared by every database driver: the
// previews rendered from them and the queue of objects to delete. Drivers
// implement Queries; this package decides what those statements mean.
package blobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

// Availability is whether a blob's bytes can be read.
type Availability string

const (
	AvailabilityAvailable Availability = "available"
	AvailabilityPending   Availability = "pending"
	AvailabilityReclaimed Availability = "reclaimed"
)

func (a Availability) Valid() bool {
	switch a {
	case AvailabilityAvailable, AvailabilityPending, AvailabilityReclaimed:
		return true
	}
	return false
}

// PreviewStatus is whether rendering a preview succeeded.
type PreviewStatus string

const (
	PreviewReady  PreviewStatus = "ready"
	PreviewFailed PreviewStatus = "failed"
)

func (s PreviewStatus) Valid() bool { return s == PreviewReady || s == PreviewFailed }

// Preview is the rendition of the bytes with a SHA-256. A failed preview
// has no object.
type Preview struct {
	ID, SHA256              string
	Status                  PreviewStatus
	ObjectKey, ObjectSHA256 string
	ObjectSize              int64
	Width, Height           int
	CreatedAt               time.Time
}

// Deletion is a stored object queued for deletion once nothing references it.
type Deletion struct {
	ID, ObjectKey string
	CreatedAt     time.Time
}

// Lease keeps a blob's bytes for an owner, such as a wait or an operation,
// until the owner releases it or the run or operation it belongs to ends or
// is deleted. Exactly one of RunID and OperationID is set.
type Lease struct {
	ID, OwnerID, RunID, OperationID, BlobID string
	CreatedAt                               time.Time
}

// Packages names the operation kinds whose payload or result holds a stored
// object: a pending import reads its package, and a succeeded export keeps
// its package until the result's expires_at.
type Packages struct {
	ImportKind, ExportKind string
}

// Queries are the statements a driver runs for blobs. Each method executes
// on the transaction or connection the driver was bound to.
type Queries interface {
	FileQueries
	TextQueries
	// BytesHeld reports whether an unreclaimed blob holds the bytes.
	BytesHeld(ctx context.Context, sha256 string) (bool, error)
	// StoredBytes sums the sizes of the distinct available contents.
	StoredBytes(ctx context.Context) (int64, error)
	Preview(ctx context.Context, sha256 string) (Preview, error)
	// SavePreview inserts p, replacing the preview of its bytes unless that
	// one is ready.
	SavePreview(ctx context.Context, p Preview) error
	// OrphanPreviews returns previews whose bytes no unreclaimed blob holds.
	OrphanPreviews(ctx context.Context, limit int) ([]Preview, error)
	DeletePreview(ctx context.Context, id string) error
	// QueueDeletion queues d unless its object is already queued.
	QueueDeletion(ctx context.Context, d Deletion) error
	// QueueItemObjects queues the objects of an item's blobs, keyed by
	// their blob IDs.
	QueueItemObjects(ctx context.Context, itemID string, at time.Time) error
	Deletions(ctx context.Context, limit int) ([]Deletion, error)
	DeleteDeletion(ctx context.Context, id string) error
	// InsertBlobLease inserts l unless its owner already leases the blob.
	InsertBlobLease(ctx context.Context, l Lease) error
	DeleteOwnerBlobLeases(ctx context.Context, ownerID string) error
	DeleteRunBlobLeases(ctx context.Context, runID string) error
	// ObjectReferenced reports whether anything still references a stored
	// object: an unreclaimed or leased blob, an extracted text, a preview, a
	// pending staged file or a package operation.
	ObjectReferenced(ctx context.Context, key string, p Packages) (bool, error)
}

// Repo runs blob reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds blob rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Preview returns the preview of the bytes with a SHA-256, or nil when none
// was rendered.
func (r *Repo) Preview(ctx context.Context, sha256 string) (*Preview, error) {
	p, err := r.read.Preview(ctx, sha256)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SavePreview pins a rendition of bytes a blob still holds. An empty object
// key records a failed rendering; a ready preview wins over a failed one.
func (r *Repo) SavePreview(ctx context.Context, p Preview) error {
	held, err := r.read.BytesHeld(ctx, p.SHA256)
	if err != nil {
		return err
	}
	if !held {
		return dberr.ErrNotFound
	}
	p.ID, p.CreatedAt, p.Status = uuid.Must(uuid.NewV7()).String(), now(), PreviewReady
	if p.ObjectKey == "" {
		p.Status = PreviewFailed
	}
	return r.read.SavePreview(ctx, p)
}

// collectPage bounds the previews one collection removes.
const collectPage = 100

// CollectPreviews removes previews of bytes no blob holds any more and
// queues their objects for deletion.
func (r *Repo) CollectPreviews(ctx context.Context) error {
	return r.write(ctx, func(q Queries) error {
		orphans, err := q.OrphanPreviews(ctx, collectPage)
		if err != nil {
			return err
		}
		at := now()
		for _, p := range orphans {
			if p.ObjectKey != "" {
				if err := q.QueueDeletion(ctx, Deletion{ID: uuid.Must(uuid.NewV7()).String(), ObjectKey: p.ObjectKey, CreatedAt: at}); err != nil {
					return err
				}
			}
			if err := q.DeletePreview(ctx, p.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueDeletion queues an object for deletion once nothing references it.
func (r *Repo) QueueDeletion(ctx context.Context, objectKey string) error {
	return r.read.QueueDeletion(ctx, Deletion{ID: uuid.Must(uuid.NewV7()).String(), ObjectKey: objectKey, CreatedAt: now()})
}

// QueueItemObjects queues the objects of every blob of an item.
func (r *Repo) QueueItemObjects(ctx context.Context, itemID string) error {
	return r.read.QueueItemObjects(ctx, itemID, now())
}

// Deletions returns queued deletions.
func (r *Repo) Deletions(ctx context.Context, limit int) ([]Deletion, error) {
	return r.read.Deletions(ctx, limit)
}

// Dequeue removes a deletion whose object was deleted or is still in use.
func (r *Repo) Dequeue(ctx context.Context, id string) error {
	return r.read.DeleteDeletion(ctx, id)
}

// Lease keeps blobs for an owner within a run.
func (r *Repo) Lease(ctx context.Context, ownerID, runID string, blobIDs []string) error {
	if len(blobIDs) == 0 {
		return nil
	}
	at := now()
	return r.write(ctx, func(q Queries) error {
		for _, id := range blobIDs {
			if err := q.InsertBlobLease(ctx, Lease{ID: uuid.NewString(), OwnerID: ownerID, RunID: runID, BlobID: id, CreatedAt: at}); err != nil {
				return err
			}
		}
		return nil
	})
}

// LeaseOperation keeps blobs for an operation until it ends.
func (r *Repo) LeaseOperation(ctx context.Context, operationID string, blobIDs []string) error {
	if len(blobIDs) == 0 {
		return nil
	}
	at := now()
	return r.write(ctx, func(q Queries) error {
		for _, id := range blobIDs {
			if err := q.InsertBlobLease(ctx, Lease{ID: uuid.NewString(), OwnerID: operationID, OperationID: operationID, BlobID: id, CreatedAt: at}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Release ends an owner's leases.
func (r *Repo) Release(ctx context.Context, ownerID string) error {
	return r.read.DeleteOwnerBlobLeases(ctx, ownerID)
}

// ReleaseRun ends every lease of a run.
func (r *Repo) ReleaseRun(ctx context.Context, runID string) error {
	return r.read.DeleteRunBlobLeases(ctx, runID)
}

// Referenced reports whether anything still references a stored object, so
// deleting it would lose data.
func (r *Repo) Referenced(ctx context.Context, key string, p Packages) (bool, error) {
	return r.read.ObjectReferenced(ctx, key, p)
}

// StoredBytes is the size of the distinct contents available file versions
// hold, each stored once.
func (r *Repo) StoredBytes(ctx context.Context) (int64, error) { return r.read.StoredBytes(ctx) }
