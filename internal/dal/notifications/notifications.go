// Package notifications owns the inbox rows every database driver keeps:
// one message to one person, with its link and read state. Drivers
// implement Queries; this package decides what those statements mean.
package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
)

// Notification is a stored notification. Empty optional identifiers are
// stored as NULL.
type Notification struct {
	ID, Recipient, WorkspaceID string
	Type                       Type
	Title, Body                string
	// ItemID, TaskID and RunID link what the notification opens; at most
	// one is set.
	ItemID, TaskID, RunID string
	Source                Source
	// Sender is the person who sent it or caused it, and WorkflowID the
	// workflow that sent it.
	Sender, WorkflowID string
	DedupKey           string
	CreatedAt          time.Time
	ReadAt             *time.Time
}

// Filter selects a recipient's notifications, newest first.
type Filter struct {
	Recipient   string
	Unread      bool
	Type        Type
	WorkspaceID string
	// After continues below a notification already read, so entries read
	// or deleted meanwhile do not shift the page.
	After *Key
	// Until keeps those created at or before it, when set.
	Until time.Time
	Limit int
}

// Key is a notification's place in the newest-first order.
type Key struct {
	CreatedAt time.Time
	ID        string
}

// LiveID is the live change entity of a notification: the recipient's
// digest and the notification's ID, so a stream recognizes changes to its
// subject's notifications, deletions included, without reading them.
func LiveID(recipient, id string) string {
	return RecipientKey(recipient) + ":" + id
}

// RecipientKey is the digest LiveID starts with.
func RecipientKey(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return hex.EncodeToString(sum[:8])
}

// Queries are the statements a driver runs for notifications.
type Queries interface {
	live.Queries
	Notification(ctx context.Context, id string) (Notification, error)
	Notifications(ctx context.Context, f Filter) ([]Notification, error)
	// InsertNotification inserts n unless the recipient already has one
	// with its dedup key, and reports whether it did.
	InsertNotification(ctx context.Context, n Notification) (bool, error)
	CountUnread(ctx context.Context, recipient string) (int64, error)
	// MarkRead marks the recipient's unread notifications read at at: those
	// of ids, or with no ids every one created at or before before. It
	// returns the IDs it marked.
	MarkRead(ctx context.Context, recipient string, ids []string, before time.Time, at time.Time) ([]string, error)
	// DeleteNotification deletes the recipient's notification and reports
	// whether there was one.
	DeleteNotification(ctx context.Context, recipient, id string) (bool, error)
}

// Repo runs notification reads on its connection and changes in
// transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds notification rules to a driver. write runs fn atomically,
// inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) Find(ctx context.Context, id string) (Notification, error) {
	return r.read.Notification(ctx, id)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Notification, error) {
	return r.read.Notifications(ctx, f)
}

func (r *Repo) Unread(ctx context.Context, recipient string) (int64, error) {
	return r.read.CountUnread(ctx, recipient)
}

// Limits of a notification's text.
const (
	MaxTitle    = 300
	MaxBody     = 4000
	MaxDedupKey = 200
)

// Create stores one notification and tells its recipient's live streams.
// It reports false, storing nothing, when the recipient already has a
// notification with n's dedup key.
func (r *Repo) Create(ctx context.Context, n Notification) (Notification, bool, error) {
	if !n.Type.Valid() || !n.Source.Valid() {
		return Notification{}, false, fmt.Errorf("%w: invalid notification type or source", dberr.ErrCheckViolation)
	}
	if n.Recipient == "" || n.Title == "" || utf8.RuneCountInString(n.Title) > MaxTitle || utf8.RuneCountInString(n.Body) > MaxBody || utf8.RuneCountInString(n.DedupKey) > MaxDedupKey {
		return Notification{}, false, fmt.Errorf("%w: a notification needs a recipient and a title of up to %d characters, a body of up to %d", dberr.ErrCheckViolation, MaxTitle, MaxBody)
	}
	links := 0
	for _, id := range []string{n.ItemID, n.TaskID, n.RunID} {
		if id != "" {
			links++
		}
	}
	if links > 1 {
		return Notification{}, false, fmt.Errorf("%w: a notification links one item, task or run", dberr.ErrCheckViolation)
	}
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	n.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	n.ReadAt = nil
	var created bool
	err := r.write(ctx, func(q Queries) error {
		var err error
		if created, err = q.InsertNotification(ctx, n); err != nil || !created {
			return err
		}
		return live.Notify(ctx, q, live.KindNotification, LiveID(n.Recipient, n.ID), live.ActionCreated)
	})
	if err != nil || !created {
		return Notification{}, false, err
	}
	return n, true, nil
}

// MarkRead marks the recipient's notifications read, those of ids or, with
// none, all created at or before before, and tells their live streams.
func (r *Repo) MarkRead(ctx context.Context, recipient string, ids []string, before time.Time) ([]string, error) {
	var marked []string
	err := r.write(ctx, func(q Queries) error {
		var err error
		if marked, err = q.MarkRead(ctx, recipient, ids, before.UTC(), time.Now().UTC().Truncate(time.Microsecond)); err != nil {
			return err
		}
		for _, id := range marked {
			if err = live.Notify(ctx, q, live.KindNotification, LiveID(recipient, id), live.ActionUpdated); err != nil {
				return err
			}
		}
		return nil
	})
	return marked, err
}

// Delete removes the recipient's notification.
func (r *Repo) Delete(ctx context.Context, recipient, id string) error {
	return r.write(ctx, func(q Queries) error {
		found, err := q.DeleteNotification(ctx, recipient, id)
		if err != nil {
			return err
		}
		if !found {
			return dberr.ErrNotFound
		}
		return live.Notify(ctx, q, live.KindNotification, LiveID(recipient, id), live.ActionDeleted)
	})
}
