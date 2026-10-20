// Package operations owns tracked background operations shared by every
// database driver: their records and the queue of items a bulk operation
// still has to process. Handlers that run operations live outside the DAL;
// they read an operation, advance it and save it. Drivers implement Queries;
// this package decides what those statements mean.
package operations

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

// Status is where an operation is in its lifecycle.
type Status string

const (
	StatusPending    Status = "pending"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusSuperseded Status = "superseded"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusSucceeded, StatusFailed, StatusSuperseded:
		return true
	}
	return false
}

// Operation is a stored operation. An empty container is stored as NULL.
// Surface, AfterItemID and Prepared are a handler's resume point; Payload and
// the credential constrain what it may do on the creator's behalf.
type Operation struct {
	ID, ContainerID, FieldID, Kind, TraceID, CreatedBy string
	CredentialID                                       string
	CredentialScopes                                   []string
	Payload, Result                                    json.RawMessage
	Status                                             Status
	Surface                                            refs.Surface
	AfterItemID                                        string
	Prepared                                           bool
	Processed, Matched, Updated, Failed, Progress      int
	Failures                                           []model.OperationFailure
	Error                                              string
	StartedAt, CompletedAt                             *time.Time
	CreatedAt, UpdatedAt                               time.Time
}

// Item is an item a bulk operation has still to process.
type Item struct {
	ID, OperationID, ItemID string
	CreatedAt               time.Time
}

// Filter selects operations ordered by ID. OwnedBy with SharedKinds selects
// the operations someone created and those of kinds everyone may see.
// PayloadField and PayloadValue match a top-level payload string.
type Filter struct {
	ContainerID, FieldID, CreatedBy, AfterID string
	Kinds                                    []string
	Statuses                                 []Status
	Finished                                 bool
	UpdatedThrough                           *time.Time
	OwnedBy                                  string
	SharedKinds                              []string
	PayloadField, PayloadValue               string
	Limit                                    int
}

// Queries are the statements a driver runs for operations. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	live.Queries
	FindOperation(ctx context.Context, id string) (Operation, error)
	// NextOperation returns the oldest pending operation.
	NextOperation(ctx context.Context) (Operation, error)
	Operations(ctx context.Context, f Filter) ([]Operation, error)
	CountOperations(ctx context.Context, f Filter) (int64, error)
	InsertOperation(ctx context.Context, op Operation) error
	// UpdateOperation writes op's progress, result and status.
	UpdateOperation(ctx context.Context, op Operation) error
	// SupersedeOperations supersedes the pending operations f selects.
	SupersedeOperations(ctx context.Context, f Filter, at time.Time) error
	DeleteOperations(ctx context.Context, ids []string) error
	OperationItems(ctx context.Context, operationID string, limit int) ([]Item, error)
	CountOperationItems(ctx context.Context, operationID string) (int64, error)
	DeleteOperationItem(ctx context.Context, id string) error
	DeleteOperationItems(ctx context.Context, operationIDs []string) error
	// ReleaseWaits completes the pending script wait steps that wait for any
	// of the operations once none of the operations they wait for is
	// pending.
	ReleaseWaits(ctx context.Context, operationIDs []string, at time.Time) error
}

// Repo runs operation reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds operation rules to a driver. write runs fn atomically,
// inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Enqueue records a pending operation from its kind, container, creator,
// trace, payload and credential, and notifies clients.
func (r *Repo) Enqueue(ctx context.Context, in Operation) (Operation, error) {
	at := now()
	op := Operation{
		ID: uuid.NewString(), ContainerID: in.ContainerID, FieldID: in.FieldID, Kind: in.Kind, TraceID: in.TraceID, CreatedBy: in.CreatedBy,
		CredentialID: in.CredentialID, CredentialScopes: in.CredentialScopes, Payload: in.Payload, Result: json.RawMessage(`{}`),
		Status: StatusPending, Surface: refs.SurfaceHead, Failures: []model.OperationFailure{}, CreatedAt: at, UpdatedAt: at,
	}
	if op.CredentialScopes == nil {
		op.CredentialScopes = []string{}
	}
	if len(op.Payload) == 0 {
		op.Payload = json.RawMessage(`{}`)
	}
	err := r.write(ctx, func(q Queries) error {
		if err := q.InsertOperation(ctx, op); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindOperation, op.ID, live.ActionCreated)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func (r *Repo) Find(ctx context.Context, id string) (Operation, error) {
	return r.read.FindOperation(ctx, id)
}

// Next returns the oldest pending operation, or dberr.ErrNotFound.
func (r *Repo) Next(ctx context.Context) (Operation, error) {
	return r.read.NextOperation(ctx)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Operation, error) {
	return r.read.Operations(ctx, f)
}

func (r *Repo) Count(ctx context.Context, f Filter) (int64, error) {
	return r.read.CountOperations(ctx, f)
}

func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	f.Limit = 1
	rows, err := r.read.Operations(ctx, f)
	return len(rows) > 0, err
}

// Save writes op's progress, result and status as of now and notifies
// clients. Finishing an operation releases the script runs waiting for it
// in the same transaction.
func (r *Repo) Save(ctx context.Context, op *Operation) error {
	op.UpdatedAt = now()
	return r.write(ctx, func(q Queries) error {
		if err := q.UpdateOperation(ctx, *op); err != nil {
			return err
		}
		if op.Status != StatusPending {
			if err := q.ReleaseWaits(ctx, []string{op.ID}, op.UpdatedAt); err != nil {
				return err
			}
		}
		return live.Notify(ctx, q, live.KindOperation, op.ID, live.ActionUpdated)
	})
}

// Supersede ends the pending operations f selects and releases the script
// runs waiting for them.
func (r *Repo) Supersede(ctx context.Context, f Filter) error {
	at := now()
	return r.write(ctx, func(q Queries) error {
		f.Statuses, f.Limit = []Status{StatusPending}, 0
		pending, err := q.Operations(ctx, f)
		if err != nil || len(pending) == 0 {
			return err
		}
		if err = q.SupersedeOperations(ctx, f, at); err != nil {
			return err
		}
		ids := make([]string, len(pending))
		for i, op := range pending {
			ids[i] = op.ID
		}
		return q.ReleaseWaits(ctx, ids, at)
	})
}

// CollectExpired deletes at most limit finished operations not updated since
// the cutoff, with their item queues, and reports how many it deleted.
func (r *Repo) CollectExpired(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	n := 0
	err := r.write(ctx, func(q Queries) error {
		through := cutoff.UTC()
		rows, err := q.Operations(ctx, Filter{Finished: true, UpdatedThrough: &through, Limit: limit})
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		n = len(ids)
		if err := q.DeleteOperationItems(ctx, ids); err != nil {
			return err
		}
		return q.DeleteOperations(ctx, ids)
	})
	return n, err
}

// Items returns the next items a bulk operation has to process, by item ID.
func (r *Repo) Items(ctx context.Context, operationID string, limit int) ([]Item, error) {
	return r.read.OperationItems(ctx, operationID, limit)
}

func (r *Repo) CountItems(ctx context.Context, operationID string) (int64, error) {
	return r.read.CountOperationItems(ctx, operationID)
}

// RemoveItem removes a processed item from its operation's queue.
func (r *Repo) RemoveItem(ctx context.Context, id string) error {
	return r.read.DeleteOperationItem(ctx, id)
}

// ClearItems empties an operation's queue.
func (r *Repo) ClearItems(ctx context.Context, operationID string) error {
	return r.read.DeleteOperationItems(ctx, []string{operationID})
}
