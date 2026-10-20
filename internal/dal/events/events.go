// Package events owns the domain event log shared by every database driver.
// Drivers implement Queries; this package decides what those statements mean.
package events

import (
	"context"
	"time"
)

// Event is one logged domain event. Empty optional identifiers are stored as
// NULL. Data keeps numeric literals exactly as they were appended.
type Event struct {
	ID, Type, WorkspaceID, CollectionID, ResourceID string
	Actor, CauseRunID, FanoutCursor                 string
	Data                                            map[string]any
	Depth                                           int
	CreatedAt                                       time.Time
	DispatchedAt                                    *time.Time
}

// Filter selects events in creation order. Undispatched and
// DispatchedThrough select events by their dispatch.
type Filter struct {
	Types             []string
	ResourceID        string
	Undispatched      bool
	DispatchedThrough *time.Time
	Limit             int
}

// Queries are the statements a driver runs for domain events. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	DomainEvent(ctx context.Context, id string) (Event, error)
	DomainEvents(ctx context.Context, f Filter) ([]Event, error)
	CountDomainEvents(ctx context.Context, f Filter) (int64, error)
	// InsertEvent logs e unless an event with its ID is already logged.
	InsertEvent(ctx context.Context, e Event) error
	SetEventFanout(ctx context.Context, id, cursor string) error
	SetEventsDispatched(ctx context.Context, ids []string, at time.Time) error
	// DeleteDispatchedEvents deletes at most limit events dispatched before
	// the cutoff and reports how many it deleted.
	DeleteDispatchedEvents(ctx context.Context, before time.Time, limit int) (int64, error)
}

// Repo runs event reads and single statements on its connection and appends
// in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds event rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) Find(ctx context.Context, id string) (Event, error) {
	return r.read.DomainEvent(ctx, id)
}

// Undispatched returns the oldest events not yet dispatched.
func (r *Repo) Undispatched(ctx context.Context, limit int) ([]Event, error) {
	return r.read.DomainEvents(ctx, Filter{Undispatched: true, Limit: limit})
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Event, error) {
	return r.read.DomainEvents(ctx, f)
}

func (r *Repo) Count(ctx context.Context, f Filter) (int64, error) {
	return r.read.CountDomainEvents(ctx, f)
}

// AdvanceFanout records the last target a permissions event was delivered to.
func (r *Repo) AdvanceFanout(ctx context.Context, id, cursor string) error {
	return r.read.SetEventFanout(ctx, id, cursor)
}

func (r *Repo) Dispatch(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return r.read.SetEventsDispatched(ctx, ids, at.UTC())
}

// DeleteDispatched deletes one page of events dispatched before the cutoff.
func (r *Repo) DeleteDispatched(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.read.DeleteDispatchedEvents(ctx, before.UTC(), limit)
}

// Append logs immutable events atomically. Repeated event IDs keep the first
// payload so scheduled occurrences are recorded once.
func (r *Repo) Append(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.write(ctx, func(q Queries) error {
		for _, e := range events {
			if e.Data == nil {
				e.Data = map[string]any{}
			}
			if e.CreatedAt.IsZero() {
				e.CreatedAt = now
			}
			e.CreatedAt = e.CreatedAt.UTC()
			e.DispatchedAt, e.FanoutCursor = nil, ""
			if err := q.InsertEvent(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
}
