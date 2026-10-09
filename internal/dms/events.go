package dms

import (
	"context"

	"github.com/google/uuid"
)

// Event types raised by committed writes. Each type is a contract: its Data
// keys are documented in docs/workflows.md.
const (
	EventItemCreated     = "item.created"
	EventItemUpdated     = "item.updated"
	EventItemPublished   = "item.published"
	EventItemUnpublished = "item.unpublished"
	EventItemDeleted     = "item.deleted"
)

// Event is a domain event: something that happened in a committed write and
// that workflows can react to. Events are separate from audit events, which
// record what changed for managers.
type Event struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	WorkspaceID  string         `json:"workspace_id"`
	CollectionID string         `json:"collection_id,omitempty"`
	ResourceID   string         `json:"resource_id"`
	Actor        string         `json:"actor"`
	Data         map[string]any `json:"data,omitempty"`
	// Depth counts how many workflow runs led to this event: a write made by
	// a run raises events one deeper than the event that started the run.
	Depth int `json:"depth"`
}

// Events receives the events of a write inside the write's transaction, so
// what reacts to them starts only if the write commits. t is bound to that
// transaction.
type Events interface {
	Publish(ctx context.Context, t *Service, events []Event) error
}

type depthKey struct{}

// WithEventDepth marks writes made with ctx as caused by an event at depth,
// so their own events are one deeper.
func WithEventDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, depthKey{}, depth)
}

// emit records an event for delivery when the surrounding write commits.
func (s *Service) emit(ctx context.Context, typ, actor, workspaceID string, collectionID *string, resourceID string, data map[string]any) {
	if s.pending == nil {
		return
	}
	depth := 0
	if d, ok := ctx.Value(depthKey{}).(int); ok {
		depth = d + 1
	}
	e := Event{ID: uuid.NewString(), Type: typ, WorkspaceID: workspaceID, ResourceID: resourceID, Actor: actor, Data: data, Depth: depth}
	if collectionID != nil {
		e.CollectionID = *collectionID
	}
	*s.pending = append(*s.pending, e)
}
