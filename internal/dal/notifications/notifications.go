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
	// one is set. CommentID also opens a comment's thread on ItemID.
	ItemID, TaskID, RunID, CommentID string
	Source                           Source
	// Sender is the person who sent it or caused it, and WorkflowID the
	// workflow that sent it.
	Sender, WorkflowID string
	DedupKey           string
	// CoalesceKey folds the next notification with the key into this one
	// while it is unread; Count is how many it stands for.
	CoalesceKey string
	Count       int64
	CreatedAt   time.Time
	ReadAt      *time.Time
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
	// CoalesceNotification folds n into the recipient's unread notification
	// with its coalesce key, or inserts it, and returns the stored ID and
	// count.
	CoalesceNotification(ctx context.Context, n Notification) (id string, count int64, err error)
	CountUnread(ctx context.Context, recipient string) (int64, error)
	// MarkRead marks the recipient's unread notifications read at at: those
	// of ids, or with no ids every one created at or before before. It
	// returns the IDs it marked.
	MarkRead(ctx context.Context, recipient string, ids []string, before time.Time, at time.Time) ([]string, error)
	// DeleteNotification deletes the recipient's notification and reports
	// whether there was one.
	DeleteNotification(ctx context.Context, recipient, id string) (bool, error)
	// CommentNotifications returns the notifications that show a comment.
	CommentNotifications(ctx context.Context, commentID string) ([]Notification, error)
	// ReviseNotification stores a notification's body, comment and count.
	ReviseNotification(ctx context.Context, n Notification) error
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
// notification with n's dedup key. With a coalesce key, an unread
// notification with that key takes n's text, link and sender, the source
// and workflow included, and counts one more instead.
func (r *Repo) Create(ctx context.Context, n Notification) (Notification, bool, error) {
	if !n.Type.Valid() || !n.Source.Valid() {
		return Notification{}, false, fmt.Errorf("%w: invalid notification type or source", dberr.ErrCheckViolation)
	}
	if n.Recipient == "" || n.Title == "" || utf8.RuneCountInString(n.Title) > MaxTitle || utf8.RuneCountInString(n.Body) > MaxBody || utf8.RuneCountInString(n.DedupKey) > MaxDedupKey || utf8.RuneCountInString(n.CoalesceKey) > MaxDedupKey {
		return Notification{}, false, fmt.Errorf("%w: a notification needs a recipient and a title of up to %d characters, a body of up to %d", dberr.ErrCheckViolation, MaxTitle, MaxBody)
	}
	links := 0
	for _, id := range []string{n.ItemID, n.TaskID, n.RunID} {
		if id != "" {
			links++
		}
	}
	if links > 1 || (n.CommentID != "" && n.ItemID == "") {
		return Notification{}, false, fmt.Errorf("%w: a notification links one item, task or run, and a comment with its item", dberr.ErrCheckViolation)
	}
	if n.DedupKey != "" && n.CoalesceKey != "" {
		return Notification{}, false, fmt.Errorf("%w: a notification is deduplicated or coalesced, not both", dberr.ErrCheckViolation)
	}
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	n.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	n.ReadAt, n.Count = nil, 1
	var created bool
	err := r.write(ctx, func(q Queries) error {
		if n.CoalesceKey != "" {
			id, count, err := q.CoalesceNotification(ctx, n)
			if err != nil {
				return err
			}
			action := live.ActionCreated
			if id != n.ID {
				action = live.ActionUpdated
			}
			n.ID, n.Count, created = id, count, true
			return live.Notify(ctx, q, live.KindNotification, LiveID(n.Recipient, n.ID), action)
		}
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

// Retext gives the notifications that show a comment its edited text and
// tells their live streams.
func (r *Repo) Retext(ctx context.Context, commentID, body string) error {
	return r.write(ctx, func(q Queries) error {
		rows, err := q.CommentNotifications(ctx, commentID)
		if err != nil {
			return err
		}
		for _, n := range rows {
			n.Body = body
			if err = q.ReviseNotification(ctx, n); err != nil {
				return err
			}
			if err = live.Notify(ctx, q, live.KindNotification, LiveID(n.Recipient, n.ID), live.ActionUpdated); err != nil {
				return err
			}
		}
		return nil
	})
}

// Withdraw takes a deleted comment out of the notifications that show it,
// so its text stays nowhere: one that stands for it alone is deleted, and
// one that folds several replies counts one fewer and opens the thread,
// threadID, without its text.
func (r *Repo) Withdraw(ctx context.Context, commentID, threadID string) error {
	return r.write(ctx, func(q Queries) error {
		rows, err := q.CommentNotifications(ctx, commentID)
		if err != nil {
			return err
		}
		for _, n := range rows {
			action := live.ActionUpdated
			if n.Count > 1 && threadID != commentID {
				n.Body, n.CommentID, n.Count = "", threadID, n.Count-1
				err = q.ReviseNotification(ctx, n)
			} else {
				action = live.ActionDeleted
				_, err = q.DeleteNotification(ctx, n.Recipient, n.ID)
			}
			if err != nil {
				return err
			}
			if err = live.Notify(ctx, q, live.KindNotification, LiveID(n.Recipient, n.ID), action); err != nil {
				return err
			}
		}
		return nil
	})
}
