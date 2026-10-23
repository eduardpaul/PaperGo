package dms

import (
	"context"
	"papergo/internal/dal"
	eventstore "papergo/internal/dal/events"

	"github.com/google/uuid"
)

// Event types raised by committed writes. Each type is a contract: its Data
// keys are documented in docs/workflows.md.
const (
	EventItemCreated     = "item.created"
	EventItemUpdated     = "item.updated"
	EventItemArchived    = "item.archived"
	EventItemUnarchived  = "item.unarchived"
	EventItemPublished   = "item.published"
	EventItemUnpublished = "item.unpublished"
	EventItemDeleted     = "item.deleted"
	EventItemRestored    = "item.restored"
	EventItemPurged      = "item.purged"
	// EventFileAdded is raised for an item's first file attachment.
	EventFileAdded          = "file.added"
	EventFileUpdated        = "file.updated"
	EventPermissionsChanged = "permissions.changed"
	EventTermChanged        = "term.changed"
	// Folder events mirror item events for folders: their name, keywords and
	// place change, they are deleted, restored and purged.
	EventFolderCreated  = "folder.created"
	EventFolderUpdated  = "folder.updated"
	EventFolderDeleted  = "folder.deleted"
	EventFolderRestored = "folder.restored"
	EventFolderPurged   = "folder.purged"
	// EventWorkflowDeleted is raised when a workflow is deleted, also with
	// its workspace or collection, with its workflow_id: its active runs,
	// open tasks and staged files go with it.
	EventWorkflowDeleted = "workflow.deleted"
)

// File sources describe file.added and file.updated events.
const (
	FileUpload   = "upload"
	FileWebDAV   = "webdav"
	FileRestore  = "restore"
	FileWorkflow = "workflow"
	// FileReindex raises file.updated again for a current file, when a search
	// reindex asks the workflows that read files to read it.
	FileReindex = "reindex"
)

// FileSources lists every file source.
var FileSources = []string{FileUpload, FileWebDAV, FileRestore, FileWorkflow, FileReindex}

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
	// CauseRunID is the workflow run whose step made the change, if any.
	CauseRunID string `json:"cause_run_id,omitempty"`
}

// recordEvents appends events to the domain event log in the write's
// transaction: they exist exactly when the change committed. An event ID
// that is already logged (a schedule occurrence raised twice) is kept once.
func recordEvents(ctx context.Context, store *dal.Store, events []Event) error {
	records := make([]eventstore.Event, len(events))
	for i, event := range events {
		records[i] = eventstore.Event{ID: event.ID, Type: event.Type, WorkspaceID: event.WorkspaceID, CollectionID: event.CollectionID, ResourceID: event.ResourceID, Actor: event.Actor, CauseRunID: event.CauseRunID, Data: event.Data, Depth: event.Depth}
	}
	return store.Events().Append(ctx, records)
}

// NewEventID returns a time-ordered event ID.
func NewEventID() string { return uuid.Must(uuid.NewV7()).String() }

type causeKey struct{}

type cause struct {
	depth int
	runID string
}

// WithCause marks writes made with ctx as made by workflow run runID, which
// an event at depth started, so their own events are one deeper and name
// the run.
func WithCause(ctx context.Context, depth int, runID string) context.Context {
	return context.WithValue(ctx, causeKey{}, cause{depth: depth, runID: runID})
}

// emit records an event for delivery when the surrounding write commits.
func (s *Service) emit(ctx context.Context, typ, actor, workspaceID string, collectionID *string, resourceID string, data map[string]any) {
	if s.pending == nil {
		return
	}
	e := Event{ID: NewEventID(), Type: typ, WorkspaceID: workspaceID, ResourceID: resourceID, Actor: actor, Data: data}
	if c, ok := ctx.Value(causeKey{}).(cause); ok {
		e.Depth, e.CauseRunID = c.depth+1, c.runID
	}
	if collectionID != nil {
		e.CollectionID = *collectionID
	}
	*s.pending = append(*s.pending, e)
}
