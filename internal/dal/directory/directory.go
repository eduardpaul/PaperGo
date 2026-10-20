// Package directory owns users, groups, roles, access tokens, organization
// settings, preferences and group inboxes. Drivers implement Queries; this
// package decides what those statements mean.
package directory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// User is an enrolled user.
type User struct {
	ID, DisplayName, Email string
	SyncHash, PasswordHash string
	Active, Service        bool
	DeletedAt              *time.Time
	CreatedAt              time.Time
}

// Session is a browser session of an account with a local password. The
// secret itself is never stored.
type Session struct {
	ID, Subject, SecretHash string
	CreatedAt, ExpiresAt    time.Time
}

// Live reports whether the user is active and not deleted.
func (u User) Live() bool { return u.Active && u.DeletedAt == nil }

// Group is a directory group.
type Group struct {
	ID, Name, Description string
	Source                GroupSource
	Version               int
	CreatedAt             time.Time
}

// Inbox is the library a group receives documents in.
type Inbox struct {
	ID, GroupID, LibraryID string
	CreatedAt              time.Time
}

// UserFilter selects users ordered by ID. Active selects active users.
type UserFilter struct {
	IDs    []string
	Active bool
}

// Queries are the statements a driver runs for the directory. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	IdentityQueries
	InsertSession(ctx context.Context, s Session) error
	FindSession(ctx context.Context, id string) (Session, error)
	DeleteSession(ctx context.Context, id string) error
	DirectoryUser(ctx context.Context, id string) (User, error)
	DirectoryUsers(ctx context.Context, f UserFilter) ([]User, error)
	DirectoryGroup(ctx context.Context, id string) (Group, error)
	GroupInbox(ctx context.Context, groupID string) (Inbox, error)
	InsertGroupInbox(ctx context.Context, i Inbox) error
	SetGroupInboxLibrary(ctx context.Context, id, libraryID string) error
	DeleteGroupInbox(ctx context.Context, id string) error
}

// Repo runs directory reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds directory rules to a driver. write runs fn atomically,
// inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) User(ctx context.Context, id string) (User, error) {
	return r.read.DirectoryUser(ctx, id)
}

// ActiveUser reports whether a user is enrolled and active.
func (r *Repo) ActiveUser(ctx context.Context, id string) (bool, error) {
	u, err := r.read.DirectoryUser(ctx, id)
	if dberr.IsNotFound(err) {
		return false, nil
	}
	return err == nil && u.Active, err
}

func (r *Repo) Users(ctx context.Context, f UserFilter) ([]User, error) {
	if f.IDs != nil && len(f.IDs) == 0 {
		return []User{}, nil
	}
	return r.read.DirectoryUsers(ctx, f)
}

func (r *Repo) Group(ctx context.Context, id string) (Group, error) {
	return r.read.DirectoryGroup(ctx, id)
}

// GroupExists reports whether a group exists.
func (r *Repo) GroupExists(ctx context.Context, id string) (bool, error) {
	return r.read.PrincipalExists(ctx, string(PrincipalGroup), id)
}

// Inbox returns a group's inbox, or dberr.ErrNotFound.
func (r *Repo) Inbox(ctx context.Context, groupID string) (Inbox, error) {
	return r.read.GroupInbox(ctx, groupID)
}

// BindInbox makes a live library a group's inbox and reports whether the
// binding changed.
func (r *Repo) BindInbox(ctx context.Context, groupID, libraryID string) (bool, error) {
	changed := false
	err := r.write(ctx, func(q Queries) error {
		if _, err := q.DirectoryGroup(ctx, groupID); err != nil {
			return err
		}
		lib, err := q.Resource(ctx, libraryID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || lib.Kind != refs.KindLibrary || lib.Deleted {
			return fmt.Errorf("%w: a group inbox must be a live library", dberr.ErrInvalidContainment)
		}
		current, err := q.GroupInbox(ctx, groupID)
		switch {
		case dberr.IsNotFound(err):
			changed = true
			return q.InsertGroupInbox(ctx, Inbox{ID: uuid.NewString(), GroupID: groupID, LibraryID: libraryID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)})
		case err != nil:
			return err
		case current.LibraryID == libraryID:
			return nil
		}
		changed = true
		return q.SetGroupInboxLibrary(ctx, current.ID, libraryID)
	})
	return changed, err
}

// UnbindInbox removes a group's inbox.
func (r *Repo) UnbindInbox(ctx context.Context, groupID string) error {
	return r.write(ctx, func(q Queries) error {
		current, err := q.GroupInbox(ctx, groupID)
		if err != nil {
			return err
		}
		return q.DeleteGroupInbox(ctx, current.ID)
	})
}

// StartSession records a session.
func (r *Repo) StartSession(ctx context.Context, s Session) error {
	s.CreatedAt, s.ExpiresAt = time.Now().UTC(), s.ExpiresAt.UTC()
	return r.read.InsertSession(ctx, s)
}

// Session returns a session, or dberr.ErrNotFound.
func (r *Repo) Session(ctx context.Context, id string) (Session, error) {
	return r.read.FindSession(ctx, id)
}

// EndSession deletes a session; ending a missing one does nothing.
func (r *Repo) EndSession(ctx context.Context, id string) error {
	return r.read.DeleteSession(ctx, id)
}
