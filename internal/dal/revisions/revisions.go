// Package revisions owns the immutable content revisions of items and
// folders, the numbered history of recorded versions and the publication
// events that expose a revision to readers. Revisions freeze their values,
// keywords, file and the schema they were validated against. Drivers
// implement Queries; this package decides what those statements mean.
package revisions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// Revision is an immutable snapshot of an item's or folder's content. A nil
// BlobID is a revision without a file.
type Revision struct {
	ID               string          `json:"id"`
	CreatedAt        time.Time       `json:"created_at"`
	ItemID           string          `json:"item_id"`
	ContainerID      string          `json:"container_id"`
	ContentTypeID    string          `json:"content_type_id"`
	SchemaRevisionID string          `json:"schema_revision_id"`
	BlobID           *string         `json:"blob_id,omitempty"`
	Filename         string          `json:"filename"`
	RevisionNumber   int             `json:"revision_number"`
	Name             string          `json:"name"`
	Keywords         []string        `json:"keywords"`
	Payload          json.RawMessage `json:"payload"`
	CreatedBy        string          `json:"created_by"`
}

// History is a recorded version of an item: the revision it shows and the
// fields that changed from the version before.
type History struct {
	ID, RevisionID, ItemID string
	Number                 int
	ChangedFields          []string
	CreatedAt              time.Time
}

// Entry is a history entry with its revision.
type Entry struct {
	History
	Revision Revision
}

// PublicationAction is what a publication event did.
type PublicationAction string

const (
	ActionPublish   PublicationAction = "publish"
	ActionUnpublish PublicationAction = "unpublish"
)

func (a PublicationAction) Valid() bool { return a == ActionPublish || a == ActionUnpublish }

// Publication records a revision becoming, or ceasing to be, what readers
// see, with the snapshot readers saw. A nil RevisionID was collected.
type Publication struct {
	ID          string            `json:"id"`
	CreatedAt   time.Time         `json:"created_at"`
	ItemID      string            `json:"item_id"`
	Version     int               `json:"version"`
	Action      PublicationAction `json:"action"`
	RevisionID  *string           `json:"revision_id,omitempty"`
	PublishedBy string            `json:"published_by"`
	Snapshot    json.RawMessage   `json:"snapshot"`
}

// Filter selects revisions ordered by item and number. A nil list does not
// filter; an empty one selects nothing.
type Filter struct {
	IDs, BlobIDs []string
	ItemID       string
	Number       int
	// HistoryNumber selects the revision a history entry records.
	HistoryNumber int
}

// HistoryFilter selects history entries, newest first. Zero fields do not
// filter.
type HistoryFilter struct {
	ItemID               string
	Number, BeforeNumber int
	Limit                int
}

// PublicationFilter selects publication events ordered by version. Zero
// fields do not filter.
type PublicationFilter struct {
	ID, ItemID, RevisionID string
	Action                 PublicationAction
	AfterVersion           int
	Limit                  int
}

// Queries are the statements a driver runs for revisions. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	SchemaRevision(ctx context.Context, id string) (*catalog.SchemaRevision, error)
	Blob(ctx context.Context, id string) (blobs.Blob, error)
	Revision(ctx context.Context, id string) (Revision, error)
	Revisions(ctx context.Context, f Filter) ([]Revision, error)
	InsertRevision(ctx context.Context, r Revision) error
	Histories(ctx context.Context, f HistoryFilter) ([]History, error)
	// LastHistoryNumber is the number of an item's newest history entry, or 0.
	LastHistoryNumber(ctx context.Context, itemID string) (int, error)
	InsertHistory(ctx context.Context, h History) error
	// DeleteHistoryBelow deletes an item's history entries numbered below n.
	DeleteHistoryBelow(ctx context.Context, itemID string, n int) error
	Publications(ctx context.Context, f PublicationFilter) ([]Publication, error)
	CountPublications(ctx context.Context) (int, error)
	InsertPublication(ctx context.Context, p Publication) error
	// CollectRevisions deletes the revisions of an item, or of every item
	// when itemID is empty, that nothing references: no resource pointer,
	// surface or history entry, and no undispatched event about the
	// revision or the one after it.
	CollectRevisions(ctx context.Context, itemID string) error
}

// Repo applies the revision rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Revision returns a revision, or dberr.ErrNotFound.
func (r *Repo) Revision(ctx context.Context, id string) (Revision, error) {
	return r.read.Revision(ctx, id)
}

func (r *Repo) Revisions(ctx context.Context, f Filter) ([]Revision, error) {
	if f.IDs != nil && len(f.IDs) == 0 || f.BlobIDs != nil && len(f.BlobIDs) == 0 {
		return []Revision{}, nil
	}
	return r.read.Revisions(ctx, f)
}

// Numbered returns an item's revision with a number, or dberr.ErrNotFound.
func (r *Repo) Numbered(ctx context.Context, itemID string, number int) (Revision, error) {
	return one(r.read.Revisions(ctx, Filter{ItemID: itemID, Number: number}))
}

// Recorded returns the revision an item's history entry records, or
// dberr.ErrNotFound.
func (r *Repo) Recorded(ctx context.Context, itemID string, number int) (Revision, error) {
	return one(r.read.Revisions(ctx, Filter{ItemID: itemID, HistoryNumber: number}))
}

// Record stores a revision of a live item or folder with q, inside the
// caller's transaction. It is validated against a schema revision of its
// collection that has its content type, and its file belongs to the item.
func Record(ctx context.Context, q Queries, rev Revision) (Revision, error) {
	item, err := q.Resource(ctx, rev.ItemID)
	if err != nil {
		return Revision{}, err
	}
	if item.Kind != refs.KindItem && item.Kind != refs.KindFolder {
		return Revision{}, fmt.Errorf("%w: revisions belong to items and folders", dberr.ErrInvalidContainment)
	}
	if item.Deleted {
		return Revision{}, fmt.Errorf("%w: deleted resources cannot have new revisions", dberr.ErrImmutable)
	}
	if item.ContainerID != rev.ContainerID {
		return Revision{}, fmt.Errorf("%w: a revision belongs to its item's collection", dberr.ErrInvalidReference)
	}
	schema, err := q.SchemaRevision(ctx, rev.SchemaRevisionID)
	if err != nil && !dberr.IsNotFound(err) {
		return Revision{}, err
	}
	if err != nil || schema.ContainerID != rev.ContainerID || !hasContentType(schema.Definition, rev.ContentTypeID) {
		return Revision{}, fmt.Errorf("%w: the schema revision does not hold the item's content type", dberr.ErrInvalidReference)
	}
	if rev.BlobID != nil {
		b, err := q.Blob(ctx, *rev.BlobID)
		if err != nil && !dberr.IsNotFound(err) {
			return Revision{}, err
		}
		if err != nil || b.ItemID != rev.ItemID {
			return Revision{}, fmt.Errorf("%w: the file belongs to another item", dberr.ErrInvalidReference)
		}
	}
	if rev.ID == "" {
		rev.ID = uuid.NewString()
	}
	if rev.Keywords == nil {
		rev.Keywords = []string{}
	}
	if len(rev.Payload) == 0 {
		rev.Payload = json.RawMessage(`{}`)
	}
	rev.CreatedAt = time.Now().UTC()
	return rev, q.InsertRevision(ctx, rev)
}

func hasContentType(definition json.RawMessage, id string) bool {
	var parsed struct {
		ContentTypes []struct {
			ID string `json:"id"`
		} `json:"content_types"`
	}
	if json.Unmarshal(definition, &parsed) != nil {
		return false
	}
	for _, t := range parsed.ContentTypes {
		if t.ID == id {
			return true
		}
	}
	return false
}

// Entries returns an item's history entries with their revisions.
func (r *Repo) Entries(ctx context.Context, f HistoryFilter) ([]Entry, error) {
	history, err := r.read.Histories(ctx, f)
	if err != nil || len(history) == 0 {
		return []Entry{}, err
	}
	ids := make([]string, len(history))
	for i, h := range history {
		ids[i] = h.RevisionID
	}
	revs, err := r.read.Revisions(ctx, Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]Revision{}
	for _, rev := range revs {
		byID[rev.ID] = rev
	}
	out := make([]Entry, len(history))
	for i, h := range history {
		out[i] = Entry{History: h, Revision: byID[h.RevisionID]}
	}
	return out, nil
}

// LastNumber is the number of an item's newest history entry, or 0.
func (r *Repo) LastNumber(ctx context.Context, itemID string) (int, error) {
	return r.read.LastHistoryNumber(ctx, itemID)
}

// Retain records a revision as its item's next history entry and keeps the
// newest max entries.
func (r *Repo) Retain(ctx context.Context, rev Revision, changed []string, max int) error {
	return r.write(ctx, func(q Queries) error {
		last, err := q.LastHistoryNumber(ctx, rev.ItemID)
		if err != nil {
			return err
		}
		if changed == nil {
			changed = []string{}
		}
		h := History{ID: uuid.NewString(), RevisionID: rev.ID, ItemID: rev.ItemID, Number: last + 1, ChangedFields: changed, CreatedAt: time.Now().UTC()}
		if err := q.InsertHistory(ctx, h); err != nil {
			return err
		}
		return q.DeleteHistoryBelow(ctx, rev.ItemID, h.Number+1-max)
	})
}

// Collect deletes the unreferenced revisions of an item, or of every item
// when itemID is empty.
func (r *Repo) Collect(ctx context.Context, itemID string) error {
	return r.read.CollectRevisions(ctx, itemID)
}

// Publications lists an item's publication events.
func (r *Repo) Publications(ctx context.Context, f PublicationFilter) ([]Publication, error) {
	return r.read.Publications(ctx, f)
}

// CountPublications counts every publication event.
func (r *Repo) CountPublications(ctx context.Context) (int, error) {
	return r.read.CountPublications(ctx)
}

// Publish records a publication event of an item's revision.
func (r *Repo) Publish(ctx context.Context, p Publication) (Publication, error) {
	if !p.Action.Valid() {
		return Publication{}, fmt.Errorf("%w: invalid publication action %q", dberr.ErrCheckViolation, p.Action)
	}
	err := r.write(ctx, func(q Queries) error {
		item, err := q.Resource(ctx, p.ItemID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || item.Kind != refs.KindItem {
			return fmt.Errorf("%w: publications belong to items", dberr.ErrInvalidContainment)
		}
		if item.Deleted {
			return fmt.Errorf("%w: deleted resources cannot be published", dberr.ErrImmutable)
		}
		if p.RevisionID != nil {
			rev, err := q.Revision(ctx, *p.RevisionID)
			if err != nil && !dberr.IsNotFound(err) {
				return err
			}
			if err != nil || rev.ItemID != p.ItemID {
				return fmt.Errorf("%w: the revision belongs to another item", dberr.ErrInvalidReference)
			}
		}
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		if len(p.Snapshot) == 0 {
			p.Snapshot = json.RawMessage(`{}`)
		}
		p.CreatedAt = time.Now().UTC()
		return q.InsertPublication(ctx, p)
	})
	return p, err
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
