// Package audit owns the immutable audit log shared by every database driver.
// Drivers implement Queries; this package decides what those statements mean.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/live"
)

// Event is one audit record. Empty optional identifiers are stored as NULL.
// Scope and target identifiers outlive the records whose deletion they
// describe.
type Event struct {
	ID, WorkspaceID, ResourceID, Subject, Action string
	EntityType, EntityID, TraceID                string
	// Properties names the entity properties the action changed.
	Properties []string
	Details    map[string]any
	CreatedAt  time.Time
}

// Filter selects audit events, newest first. AfterAt and AfterID continue
// a page after the last event returned.
type Filter struct {
	Actor, EntityType, EntityID, WorkspaceID, AfterID string
	ResourceID, Action                                string
	From, To, AfterAt                                 *time.Time
	Limit                                             int
}

// Queries are the statements a driver runs for the audit log. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	live.Queries
	AuditEvents(ctx context.Context, f Filter) ([]Event, error)
	CountAuditEvents(ctx context.Context, f Filter) (int64, error)
	InsertAuditEvent(ctx context.Context, e Event) error
	// AuditRecorded reports whether an action on an entity was recorded.
	AuditRecorded(ctx context.Context, entityType, entityID, action string) (bool, error)
}

// Repo runs audit reads on its connection and appends in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds audit rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Event, error) {
	return r.read.AuditEvents(ctx, f)
}

// Count counts the events f matches, ignoring its page.
func (r *Repo) Count(ctx context.Context, f Filter) (int64, error) {
	return r.read.CountAuditEvents(ctx, f)
}

// Recorded reports whether an action on an entity was recorded.
func (r *Repo) Recorded(ctx context.Context, entityType, entityID, action string) (bool, error) {
	return r.read.AuditRecorded(ctx, entityType, entityID, action)
}

// accessActions change who can do what; clients reload access when one is
// appended.
var accessActions = map[string]bool{
	"permissions.replace": true, "workspace.member.set": true, "workspace.member.remove": true,
	"group.member": true, "role.assignment": true, "role.update": true, "role.delete": true,
	"user.status": true, "user.delete": true, "resource.delete": true, "resource.purge": true,
}

// Append records e and returns it as stored. A missing ID is generated and
// a missing creation time is now.
func (r *Repo) Append(ctx context.Context, e Event) (Event, error) {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Properties == nil {
		e.Properties = []string{}
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	e.CreatedAt = e.CreatedAt.UTC().Truncate(time.Microsecond)
	err := r.write(ctx, func(q Queries) error {
		if err := q.InsertAuditEvent(ctx, e); err != nil {
			return err
		}
		if accessActions[e.Action] {
			return live.Notify(ctx, q, live.KindAccess, e.ID, live.ActionChanged)
		}
		return nil
	})
	if err != nil {
		return Event{}, err
	}
	return e, nil
}
