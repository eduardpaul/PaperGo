// Package live owns the change notifications that clients follow. Domains
// that notify embed Queries in their own and insert a change in the
// transaction of the write it reports.
package live

import (
	"context"
	"time"
)

// Kind is the type of entity a change reports.
type Kind string

const (
	KindItem      Kind = "item"
	KindOperation Kind = "operation"
	KindRun       Kind = "run"
	KindAccess    Kind = "access"
	// KindNotification reaches only the notification's recipient.
	KindNotification Kind = "notification"
	// KindComment reaches the streams of people who may read the
	// comment's discussion.
	KindComment Kind = "comment"
)

func (k Kind) Valid() bool {
	switch k {
	case KindItem, KindOperation, KindRun, KindAccess, KindNotification, KindComment:
		return true
	}
	return false
}

// Action is what happened to the entity.
type Action string

const (
	ActionCreated Action = "created"
	ActionUpdated Action = "updated"
	ActionDeleted Action = "deleted"
	ActionChanged Action = "changed"
)

// Change is one notification. The database assigns its ID, which orders
// changes for clients that follow them.
type Change struct {
	ID        int64
	Kind      Kind
	EntityID  string
	Action    Action
	CreatedAt time.Time
}

// Queries are the statements a driver runs for change notifications.
type Queries interface {
	InsertLiveChange(ctx context.Context, c Change) error
	// LiveChanges returns at most limit changes after an ID, in ID order.
	LiveChanges(ctx context.Context, after int64, limit int) ([]Change, error)
	// LiveBounds returns the lowest stored ID and the highest committed ID,
	// or zero for each when there is none.
	LiveBounds(ctx context.Context) (earliest, latest int64, err error)
	// CountLiveChanges counts changes created at or before through.
	CountLiveChanges(ctx context.Context, through time.Time) (int64, error)
	// DeleteLiveChanges deletes at most limit changes created at or before
	// through and reports how many it deleted.
	DeleteLiveChanges(ctx context.Context, through time.Time, limit int) (int64, error)
}

// Notify inserts a change for an entity now.
func Notify(ctx context.Context, q Queries, kind Kind, entityID string, action Action) error {
	return q.InsertLiveChange(ctx, Change{Kind: kind, EntityID: entityID, Action: action, CreatedAt: time.Now().UTC()})
}

// Repo reads and maintains the change log.
type Repo struct {
	q Queries
}

func NewRepo(q Queries) *Repo {
	return &Repo{q: q}
}

// pageSize bounds the changes one read returns.
const pageSize = 500

// After returns the next page of changes after an ID.
func (r *Repo) After(ctx context.Context, after int64) ([]Change, error) {
	return r.q.LiveChanges(ctx, after, pageSize)
}

// Bounds returns the lowest stored ID and the highest committed ID.
func (r *Repo) Bounds(ctx context.Context) (earliest, latest int64, err error) {
	return r.q.LiveBounds(ctx)
}

// CountExpired counts changes created at or before the cutoff.
func (r *Repo) CountExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.q.CountLiveChanges(ctx, cutoff.UTC())
}

// DeleteExpired deletes one page of changes created at or before the cutoff.
func (r *Repo) DeleteExpired(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return r.q.DeleteLiveChanges(ctx, cutoff.UTC(), limit)
}
