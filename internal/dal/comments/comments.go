// Package comments owns the discussion rows every database driver keeps:
// threads of comments on items and the people they mention. Drivers
// implement Queries; this package decides what those statements mean.
package comments

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
)

// Source is who wrote a comment.
type Source string

const (
	SourceUser     Source = "user"
	SourceWorkflow Source = "workflow"
)

func (s Source) Valid() bool { return s == SourceUser || s == SourceWorkflow }

// Limits of a discussion.
const (
	MaxBody = 10000
	// MaxReplies bounds a thread, so a thread reads at once.
	MaxReplies = 500
)

// Comment is a stored comment. A thread's root has ThreadID = ID; a deleted
// comment keeps its place with an empty body.
type Comment struct {
	ID, ItemID, ThreadID, Author string
	Source                       Source
	WorkflowID                   string
	Body                         string
	// Mentions are the people the comment mentions.
	Mentions             []string
	CreatedAt, UpdatedAt time.Time
	EditedAt, DeletedAt  *time.Time
	DeletedBy            string
	ResolvedAt           *time.Time
	ResolvedBy           string
	Version              int64
}

// Root reports whether c starts a thread.
func (c Comment) Root() bool { return c.ThreadID == c.ID }

// Key is a thread's place in the oldest-first order of an item's threads.
type Key struct {
	CreatedAt time.Time
	ID        string
}

// Threads selects an item's thread roots, oldest first.
type Threads struct {
	ItemID string
	// Resolved keeps resolved threads when true, open ones when false, and
	// both when nil.
	Resolved *bool
	After    *Key
	Limit    int
}

// Count is how many threads an item has, by state; deleted roots count
// while their threads have replies.
type Count struct {
	Open, Resolved int
}

// Queries are the statements a driver runs for comments.
type Queries interface {
	refs.Queries
	live.Queries
	Comment(ctx context.Context, id string) (Comment, error)
	Roots(ctx context.Context, f Threads) ([]Comment, error)
	// Replies returns the replies of threads, oldest first.
	Replies(ctx context.Context, threadIDs []string) ([]Comment, error)
	CountReplies(ctx context.Context, threadID string) (int, error)
	InsertComment(ctx context.Context, c Comment) error
	// UpdateComment writes c's mutable state when the stored version is from.
	UpdateComment(ctx context.Context, c Comment, from int64) error
	// Mentions returns the people each comment mentions.
	Mentions(ctx context.Context, commentIDs []string) (map[string][]string, error)
	AddMentions(ctx context.Context, commentID string, subjects []string, at time.Time) error
	RemoveMentions(ctx context.Context, commentID string, subjects []string) error
	CountThreads(ctx context.Context, itemIDs []string) (map[string]Count, error)
}

// Repo runs comment reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds comment rules to a driver. write runs fn atomically, inside
// the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Find returns a comment with its mentions.
func (r *Repo) Find(ctx context.Context, id string) (Comment, error) {
	c, err := r.read.Comment(ctx, id)
	if err != nil {
		return c, err
	}
	m, err := r.read.Mentions(ctx, []string{id})
	c.Mentions = m[id]
	return c, err
}

// Roots returns an item's thread roots with their mentions.
func (r *Repo) Roots(ctx context.Context, f Threads) ([]Comment, error) {
	rows, err := r.read.Roots(ctx, f)
	if err != nil {
		return nil, err
	}
	return rows, r.mentions(ctx, rows)
}

// Replies returns the replies of threads with their mentions, oldest first.
func (r *Repo) Replies(ctx context.Context, threadIDs []string) ([]Comment, error) {
	if len(threadIDs) == 0 {
		return nil, nil
	}
	rows, err := r.read.Replies(ctx, threadIDs)
	if err != nil {
		return nil, err
	}
	return rows, r.mentions(ctx, rows)
}

// mentionBatch is how many comments one mentions statement names, well
// inside the bound databases set on a statement's parameters: a page of
// threads holds up to 100 threads of up to 500 replies.
const mentionBatch = 1000

// mentions reads the mentions of rows, in batches.
func (r *Repo) mentions(ctx context.Context, rows []Comment) error {
	for batch := range slices.Chunk(rows, mentionBatch) {
		ids := make([]string, len(batch))
		for i, c := range batch {
			ids[i] = c.ID
		}
		m, err := r.read.Mentions(ctx, ids)
		if err != nil {
			return err
		}
		for i := range batch {
			batch[i].Mentions = m[batch[i].ID]
		}
	}
	return nil
}

// Counts returns the thread counts of items; items without threads have
// none.
func (r *Repo) Counts(ctx context.Context, itemIDs []string) (map[string]Count, error) {
	if len(itemIDs) == 0 {
		return map[string]Count{}, nil
	}
	return r.read.CountThreads(ctx, itemIDs)
}

func check(c Comment) error {
	if c.Body == "" || utf8.RuneCountInString(c.Body) > MaxBody {
		return fmt.Errorf("%w: a comment has 1 to %d characters", dberr.ErrCheckViolation, MaxBody)
	}
	return nil
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Create stores a comment on a live item: a thread's root when ThreadID is
// empty, else a reply to that thread's live root.
func (r *Repo) Create(ctx context.Context, c Comment) (Comment, error) {
	if !c.Source.Valid() || c.Author == "" {
		return Comment{}, fmt.Errorf("%w: invalid comment source or author", dberr.ErrCheckViolation)
	}
	if err := check(c); err != nil {
		return Comment{}, err
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	c.CreatedAt = now()
	c.UpdatedAt, c.Version = c.CreatedAt, 1
	c.EditedAt, c.DeletedAt, c.ResolvedAt, c.DeletedBy, c.ResolvedBy = nil, nil, nil, "", ""
	c.Mentions = sortedSet(c.Mentions)
	err := r.write(ctx, func(q Queries) error {
		item, err := q.Resource(ctx, c.ItemID)
		if err != nil {
			return err
		}
		if item.Kind != refs.KindItem || item.Deleted {
			return fmt.Errorf("%w: comments are on live items", dberr.ErrInvalidContainment)
		}
		if c.ThreadID == "" {
			c.ThreadID = c.ID
		} else {
			root, err := q.Comment(ctx, c.ThreadID)
			if err != nil {
				return err
			}
			if !root.Root() || root.ItemID != c.ItemID || root.DeletedAt != nil {
				return fmt.Errorf("%w: a reply answers a live thread of its item", dberr.ErrInvalidContainment)
			}
			n, err := q.CountReplies(ctx, root.ID)
			if err != nil {
				return err
			}
			if n >= MaxReplies {
				return fmt.Errorf("%w: a thread has at most %d replies", dberr.ErrCheckViolation, MaxReplies)
			}
		}
		if err := q.InsertComment(ctx, c); err != nil {
			return err
		}
		if err := q.AddMentions(ctx, c.ID, c.Mentions, c.CreatedAt); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindComment, c.ID, live.ActionCreated)
	})
	return c, err
}

// change applies fn to the live comment id at version from and stores it.
func (r *Repo) change(ctx context.Context, id string, from int64, fn func(*Comment, Queries) error) (Comment, error) {
	var out Comment
	err := r.write(ctx, func(q Queries) error {
		c, err := q.Comment(ctx, id)
		if err != nil {
			return err
		}
		if c.Version != from {
			return dberr.ErrConcurrentUpdate
		}
		if c.DeletedAt != nil {
			return fmt.Errorf("%w: the comment was deleted", dberr.ErrInvalidStateTransition)
		}
		m, err := q.Mentions(ctx, []string{id})
		if err != nil {
			return err
		}
		c.Mentions = m[id]
		if err = fn(&c, q); err != nil {
			return err
		}
		c.UpdatedAt, c.Version = now(), from+1
		if err = q.UpdateComment(ctx, c, from); err != nil {
			return err
		}
		out = c
		return live.Notify(ctx, q, live.KindComment, c.ID, live.ActionUpdated)
	})
	return out, err
}

// Edit replaces a comment's text and mentions, and returns the people it
// mentions now that it did not before.
func (r *Repo) Edit(ctx context.Context, id string, from int64, body string, mentions []string) (Comment, []string, error) {
	var added []string
	c, err := r.change(ctx, id, from, func(c *Comment, q Queries) error {
		next := sortedSet(mentions)
		var removed []string
		for _, s := range c.Mentions {
			if !slices.Contains(next, s) {
				removed = append(removed, s)
			}
		}
		for _, s := range next {
			if !slices.Contains(c.Mentions, s) {
				added = append(added, s)
			}
		}
		edited := now()
		c.Body, c.Mentions, c.EditedAt = body, next, &edited
		if err := check(*c); err != nil {
			return err
		}
		if err := q.RemoveMentions(ctx, c.ID, removed); err != nil {
			return err
		}
		return q.AddMentions(ctx, c.ID, added, edited)
	})
	return c, added, err
}

// Delete empties a comment and marks it deleted; its thread keeps its
// place.
func (r *Repo) Delete(ctx context.Context, id string, from int64, by string) (Comment, error) {
	return r.change(ctx, id, from, func(c *Comment, q Queries) error {
		deleted := now()
		if err := q.RemoveMentions(ctx, c.ID, c.Mentions); err != nil {
			return err
		}
		c.Body, c.Mentions, c.DeletedAt, c.DeletedBy = "", nil, &deleted, by
		c.ResolvedAt, c.ResolvedBy = nil, ""
		return nil
	})
}

// Resolve marks a thread's root resolved, or open again.
func (r *Repo) Resolve(ctx context.Context, id string, from int64, resolved bool, by string) (Comment, error) {
	return r.change(ctx, id, from, func(c *Comment, _ Queries) error {
		if !c.Root() {
			return fmt.Errorf("%w: only a thread's first comment is resolved", dberr.ErrInvalidStateTransition)
		}
		if (c.ResolvedAt != nil) == resolved {
			return fmt.Errorf("%w: the thread is already %s", dberr.ErrInvalidStateTransition, map[bool]string{true: "resolved", false: "open"}[resolved])
		}
		c.ResolvedAt, c.ResolvedBy = nil, ""
		if resolved {
			at := now()
			c.ResolvedAt, c.ResolvedBy = &at, by
		}
		return nil
	})
}

func sortedSet(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}
