// Package operations owns tracked background operations shared by every
// database driver: their records, their execution claims and the queue of
// items a bulk operation still has to process. Handlers that run operations
// live outside the DAL; they claim an operation, advance it under the claim
// and finish it. Drivers implement Queries; this package decides what those
// statements mean.
package operations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
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
	StatusCancelled  Status = "cancelled"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusSucceeded, StatusFailed, StatusSuperseded, StatusCancelled:
		return true
	}
	return false
}

// Terminal reports whether the status ends the operation.
func (s Status) Terminal() bool { return s.Valid() && s != StatusPending }

// ErrorCode is the machine-readable reason a failed operation failed. Most
// failures carry only their error text.
type ErrorCode string

const (
	ErrorNone             ErrorCode = ""
	ErrorDeadlineExceeded ErrorCode = "deadline_exceeded"
	ErrorRetriesExhausted ErrorCode = "retries_exhausted"
	ErrorForbidden        ErrorCode = "forbidden"
)

func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorNone, ErrorDeadlineExceeded, ErrorRetriesExhausted, ErrorForbidden:
		return true
	}
	return false
}

// ErrClaimLost means the execution no longer owns the operation: its claim
// expired or was replaced, the operation is no longer pending, or its
// deadline passed before it could succeed.
var ErrClaimLost = errors.New("operation claim was lost")

// ErrIdempotencyConflict means the caller's idempotency key already names an
// operation with another payload.
var ErrIdempotencyConflict = errors.New("idempotency key was used with another payload")

// Claim is an execution's ownership of an operation until Until. Token
// fences every write the execution makes.
type Claim struct {
	Holder, Token string
	Until         time.Time
}

// Operation is a stored operation. An empty container or workspace is
// stored as NULL. Surface, AfterItemID and Prepared are a handler's resume
// point; Payload and the credential constrain what it may do on the
// creator's behalf. AttemptAt, Attempts, Claim and ExecutionID are its
// execution state while pending; GroupKey and InputKey are its admission
// group and input identity.
type Operation struct {
	ID, ContainerID, WorkspaceID, FieldID, Kind, TraceID, CreatedBy string
	CredentialID                                                    string
	CredentialScopes                                                []string
	Payload, Result                                                 json.RawMessage
	Status                                                          Status
	ErrorCode                                                       ErrorCode
	Surface                                                         refs.Surface
	AfterItemID                                                     string
	Prepared                                                        bool
	Processed, Matched, Updated, Failed, Progress                   int
	Failures                                                        []model.OperationFailure
	Error                                                           string
	StartedAt, CompletedAt, Deadline                                *time.Time
	CreatedAt, UpdatedAt, AttemptAt                                 time.Time
	Attempts                                                        int
	Claim                                                           Claim
	ExecutionID, GroupKey, InputKey                                 string
	IdempotencyKey, PayloadDigest                                   string
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
	IDs, Kinds, ExcludeKinds                 []string
	Statuses                                 []Status
	Finished                                 bool
	UpdatedThrough, DeadlineBefore           *time.Time
	OwnedBy                                  string
	SharedKinds                              []string
	PayloadField, PayloadValue               string
	// ExecutionID selects the members of a module execution.
	ExecutionID string
	Limit       int
}

// Ready selects the pending operations of Kinds a worker may claim at Now:
// unclaimed or with an expired claim, not parked and due. IDs limits them to
// particular operations. A nil WorkspaceID or GroupKey selects any; an empty
// WorkspaceID selects organization work. Through limits them to those
// created by then. Group selections are ordered by creation, the others by
// attempt time.
type Ready struct {
	Kinds, IDs  []string
	WorkspaceID *string
	GroupKey    *string
	Through     *time.Time
	Now         time.Time
	Limit       int
}

// Fence selects the pending operations an execution may still change: those
// it claims with Token until after Now, or those parked in ExecutionID.
type Fence struct {
	IDs         []string
	Token       string
	ExecutionID string
	Now         time.Time
}

// Change is what an execution update writes. Progress writes the
// operation's status, result, resume point, counters, error and timings;
// nil fields stay as they are.
type Change struct {
	Progress    *Operation
	Claim       *Claim
	ClearClaim  bool
	AttemptAt   *time.Time
	Attempts    int
	ExecutionID *string
	Error       *string
	UpdatedAt   time.Time
}

// Termination ends pending operations without an execution.
type Termination struct {
	Status Status
	Code   ErrorCode
	Error  string
	At     time.Time
}

// Group is the ready work of one admission group: how many distinct inputs
// it holds and when its oldest operation was created.
type Group struct {
	Kind, Key string
	Inputs    int
	Oldest    time.Time
}

// Totals counts pending operations for acceptance quotas.
type Totals struct {
	Creator, Workspace, Installation, WorkspacePayloadBytes int64
}

// Queries are the statements a driver runs for operations. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	live.Queries
	FindOperation(ctx context.Context, id string) (Operation, error)
	// FindIdempotent returns the operation a creator's idempotency key names
	// for a kind and container.
	FindIdempotent(ctx context.Context, createdBy, kind, containerID, key string) (Operation, error)
	Operations(ctx context.Context, f Filter) ([]Operation, error)
	CountOperations(ctx context.Context, f Filter) (int64, error)
	InsertOperation(ctx context.Context, op Operation) error
	// ReadyOperations returns the operations r selects, locking them against
	// other claimers where the engine can.
	ReadyOperations(ctx context.Context, r Ready) ([]Operation, error)
	// ClaimOperations claims those of ids that are still ready at now and
	// counts an attempt for each.
	ClaimOperations(ctx context.Context, ids []string, c Claim, now time.Time) (int64, error)
	// UpdateFenced writes c to the operations f selects and counts them.
	UpdateFenced(ctx context.Context, f Fence, c Change) (int64, error)
	// TerminateOperations ends the pending operations among ids, dropping
	// their claims and execution membership, and counts them.
	TerminateOperations(ctx context.Context, ids []string, t Termination) (int64, error)
	// ReleaseOperationRetention deletes the blob leases and payload
	// references the operations own.
	ReleaseOperationRetention(ctx context.Context, ids []string) error
	ReadyGroups(ctx context.Context, kinds []string, now time.Time) ([]Group, error)
	// ReadyWorkspaces returns up to limit workspaces with ready work, ""
	// for organization work.
	ReadyWorkspaces(ctx context.Context, kinds []string, now time.Time, limit int) ([]string, error)
	// NextAttempt returns the earliest time a pending, unparked operation of
	// kinds becomes ready, or nil when there is none.
	NextAttempt(ctx context.Context, kinds []string, now time.Time) (*time.Time, error)
	// PendingTotals counts pending operations, except those of exempt
	// kinds.
	PendingTotals(ctx context.Context, createdBy, workspaceID string, exempt []string) (Totals, error)
	DeleteOperations(ctx context.Context, ids []string) error
	OperationItems(ctx context.Context, operationID string, limit int) ([]Item, error)
	CountOperationItems(ctx context.Context, operationID string) (int64, error)
	DeleteOperationItem(ctx context.Context, id string) error
	DeleteOperationItems(ctx context.Context, operationIDs []string) error
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

// UTC truncates t to the precision both engines store.
func UTC(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// claimScan bounds the ready operations one group claim reads to find its
// distinct inputs.
const claimScan = 1000

// Accept records a pending operation and notifies clients; a missing ID is
// generated. An operation accepted with an ExecutionID starts parked: that execution, such as a
// task, ends it. An operation with an idempotency key returns the one the
// key already names with the same payload digest, reporting it as
// replayed, and ErrIdempotencyConflict when the digest differs.
func (r *Repo) Accept(ctx context.Context, in Operation, now time.Time) (op Operation, replayed bool, err error) {
	at := UTC(now)
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	op = Operation{
		ID: id, ContainerID: in.ContainerID, WorkspaceID: in.WorkspaceID, FieldID: in.FieldID, Kind: in.Kind, TraceID: in.TraceID,
		CreatedBy: in.CreatedBy, CredentialID: in.CredentialID, CredentialScopes: in.CredentialScopes, Payload: in.Payload, Result: json.RawMessage(`{}`),
		Status: StatusPending, Surface: refs.SurfaceHead, Failures: []model.OperationFailure{}, CreatedAt: at, UpdatedAt: at, AttemptAt: at,
		GroupKey: in.GroupKey, InputKey: in.InputKey, Deadline: in.Deadline, IdempotencyKey: in.IdempotencyKey, PayloadDigest: in.PayloadDigest,
		ExecutionID: in.ExecutionID,
	}
	if op.CredentialScopes == nil {
		op.CredentialScopes = []string{}
	}
	if len(op.Payload) == 0 {
		op.Payload = json.RawMessage(`{}`)
	}
	if op.Deadline != nil {
		deadline := UTC(*op.Deadline)
		op.Deadline = &deadline
	}
	err = r.write(ctx, func(q Queries) error {
		if op.IdempotencyKey != "" {
			existing, e := q.FindIdempotent(ctx, op.CreatedBy, op.Kind, op.ContainerID, op.IdempotencyKey)
			if e == nil {
				if existing.PayloadDigest != op.PayloadDigest {
					return ErrIdempotencyConflict
				}
				op, replayed = existing, true
				return nil
			}
			if !dberr.IsNotFound(e) {
				return e
			}
		}
		if e := q.InsertOperation(ctx, op); e != nil {
			return e
		}
		return live.Notify(ctx, q, live.KindOperation, op.ID, live.ActionCreated)
	})
	if err != nil {
		return Operation{}, false, err
	}
	return op, replayed, nil
}

// Delete removes operations with their items, whatever their status; what
// they own goes with them.
func (r *Repo) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.write(ctx, func(q Queries) error {
		if err := q.DeleteOperationItems(ctx, ids); err != nil {
			return err
		}
		return q.DeleteOperations(ctx, ids)
	})
}

func (r *Repo) Find(ctx context.Context, id string) (Operation, error) {
	return r.read.FindOperation(ctx, id)
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

// PendingTotals counts the pending operations of a creator, a workspace
// and the installation for acceptance quotas, leaving out exempt kinds.
func (r *Repo) PendingTotals(ctx context.Context, createdBy, workspaceID string, exempt []string) (Totals, error) {
	return r.read.PendingTotals(ctx, createdBy, workspaceID, exempt)
}

// FindIdempotent returns the operation a creator's idempotency key names
// for a kind and container.
func (r *Repo) FindIdempotent(ctx context.Context, createdBy, kind, containerID, key string) (Operation, error) {
	return r.read.FindIdempotent(ctx, createdBy, kind, containerID, key)
}

// ClaimRequest asks for up to Limit ready operations for Holder until
// Now+TTL. With a GroupKey, Limit counts distinct inputs: operations
// sharing an input are claimed together.
type ClaimRequest struct {
	Ready
	Holder string
	TTL    time.Duration
}

// Claim atomically selects and claims ready operations. Each claimed
// operation carries the claim that fences its later writes.
func (r *Repo) Claim(ctx context.Context, in ClaimRequest) ([]Operation, error) {
	if in.Holder == "" || in.TTL <= 0 || in.Limit < 1 || len(in.Kinds) == 0 {
		return nil, errors.New("a claim needs kinds, a holder, a lifetime and a limit")
	}
	in.Now = UTC(in.Now)
	c := Claim{Holder: in.Holder, Token: uuid.NewString(), Until: UTC(in.Now.Add(in.TTL))}
	var out []Operation
	err := r.write(ctx, func(q Queries) error {
		ready := in.Ready
		if ready.GroupKey != nil {
			ready.Limit = claimScan
		}
		rows, err := q.ReadyOperations(ctx, ready)
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := pick(rows, in.Limit, ready.GroupKey != nil)
		if _, err = q.ClaimOperations(ctx, ids, c, in.Now); err != nil {
			return err
		}
		claimed, err := q.Operations(ctx, Filter{IDs: ids})
		if err != nil {
			return err
		}
		byID := map[string]Operation{}
		for _, op := range claimed {
			byID[op.ID] = op
		}
		// In the order they were ready.
		for _, id := range ids {
			if op, ok := byID[id]; ok && op.Claim.Token == c.Token {
				out = append(out, op)
			}
		}
		return nil
	})
	return out, err
}

// pick returns the IDs of the first limit operations or, by input, of the
// operations of the first limit distinct inputs.
func pick(rows []Operation, limit int, byInput bool) []string {
	var ids []string
	inputs := map[string]bool{}
	n := 0
	for _, op := range rows {
		switch {
		case !byInput || op.InputKey == "":
			if n == limit {
				continue
			}
			n++
		case !inputs[op.InputKey]:
			if n == limit {
				continue
			}
			inputs[op.InputKey] = true
			n++
		}
		ids = append(ids, op.ID)
	}
	return ids
}

// fenced writes c under the execution's claim, or ErrClaimLost when it no
// longer owns every operation.
func (r *Repo) fenced(ctx context.Context, q Queries, f Fence, c Change) error {
	f.Now = UTC(f.Now)
	c.UpdatedAt = f.Now
	n, err := q.UpdateFenced(ctx, f, c)
	if err != nil {
		return err
	}
	if n != int64(len(f.IDs)) {
		return ErrClaimLost
	}
	return nil
}

// Renew extends a claim to until.
func (r *Repo) Renew(ctx context.Context, id string, c Claim, until, now time.Time) error {
	renewed := Claim{Holder: c.Holder, Token: c.Token, Until: UTC(until)}
	return r.write(ctx, func(q Queries) error {
		return r.fenced(ctx, q, Fence{IDs: []string{id}, Token: c.Token, Now: now}, Change{Claim: &renewed})
	})
}

// Release gives the operation back without counting the attempt, for an
// execution that stops before finishing, such as at shutdown.
func (r *Repo) Release(ctx context.Context, id string, c Claim, now time.Time) error {
	return r.write(ctx, func(q Queries) error {
		return r.fenced(ctx, q, Fence{IDs: []string{id}, Token: c.Token, Now: now}, Change{ClearClaim: true, Attempts: -1})
	})
}

// Defer gives the operation back until at, recording why.
func (r *Repo) Defer(ctx context.Context, id string, c Claim, at time.Time, cause string, now time.Time) error {
	next := UTC(at)
	return r.write(ctx, func(q Queries) error {
		if err := r.fenced(ctx, q, Fence{IDs: []string{id}, Token: c.Token, Now: now}, Change{ClearClaim: true, AttemptAt: &next, Error: &cause}); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindOperation, id, live.ActionUpdated)
	})
}

// Checkpoint writes a pending operation's progress under its claim.
func (r *Repo) Checkpoint(ctx context.Context, op *Operation, now time.Time) error {
	if op.Status != StatusPending {
		return errors.New("a checkpoint keeps the operation pending")
	}
	op.UpdatedAt = UTC(now)
	return r.write(ctx, func(q Queries) error {
		if err := r.fenced(ctx, q, Fence{IDs: []string{op.ID}, Token: op.Claim.Token, Now: now}, Change{Progress: op}); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindOperation, op.ID, live.ActionUpdated)
	})
}

// Sleep gives the operation back until at, after an execution that made
// progress and continues then, without counting the attempt.
func (r *Repo) Sleep(ctx context.Context, id string, c Claim, at, now time.Time) error {
	next := UTC(at)
	return r.write(ctx, func(q Queries) error {
		return r.fenced(ctx, q, Fence{IDs: []string{id}, Token: c.Token, Now: now}, Change{ClearClaim: true, AttemptAt: &next, Attempts: -1})
	})
}

// Finish ends an operation under its claim with its terminal status and
// result, and releases what it retained.
func (r *Repo) Finish(ctx context.Context, op *Operation, now time.Time) error {
	return r.finish(ctx, op, Fence{IDs: []string{op.ID}, Token: op.Claim.Token, Now: now}, now)
}

// Park makes claimed operations members of the durable module execution
// executionID, releasing their claims. Parking does not count as an
// attempt: the execution made progress.
func (r *Repo) Park(ctx context.Context, ids []string, c Claim, executionID string, now time.Time) error {
	if executionID == "" {
		return errors.New("parking needs an execution")
	}
	return r.write(ctx, func(q Queries) error {
		return r.fenced(ctx, q, Fence{IDs: ids, Token: c.Token, Now: now}, Change{ClearClaim: true, ExecutionID: &executionID, Attempts: -1})
	})
}

// Unpark returns the pending members of executionID among ids to the
// queue, ready at at. Members that ended meanwhile stay ended.
func (r *Repo) Unpark(ctx context.Context, executionID string, ids []string, at, now time.Time) error {
	next, none := UTC(at), ""
	return r.write(ctx, func(q Queries) error {
		_, err := q.UpdateFenced(ctx, Fence{IDs: ids, ExecutionID: executionID, Now: UTC(now)}, Change{ExecutionID: &none, AttemptAt: &next, UpdatedAt: UTC(now)})
		return err
	})
}

// FinishMember ends a pending member of executionID. ErrClaimLost means the
// member ended or left the execution meanwhile.
func (r *Repo) FinishMember(ctx context.Context, op *Operation, executionID string, now time.Time) error {
	if executionID == "" {
		return errors.New("a member needs its execution")
	}
	return r.finish(ctx, op, Fence{IDs: []string{op.ID}, ExecutionID: executionID, Now: now}, now)
}

func (r *Repo) finish(ctx context.Context, op *Operation, f Fence, now time.Time) error {
	if !op.Status.Terminal() || !op.ErrorCode.Valid() {
		return errors.New("finishing needs a terminal status")
	}
	at := UTC(now)
	if op.CompletedAt == nil {
		op.CompletedAt = &at
	}
	if op.Status == StatusSucceeded {
		op.Progress = 100
	}
	op.UpdatedAt = at
	none := ""
	err := r.write(ctx, func(q Queries) error {
		if err := r.fenced(ctx, q, f, Change{Progress: op, ClearClaim: true, ExecutionID: &none}); err != nil {
			return err
		}
		if err := q.ReleaseOperationRetention(ctx, []string{op.ID}); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindOperation, op.ID, live.ActionUpdated)
	})
	if err == nil {
		op.Claim, op.ExecutionID = Claim{}, ""
	}
	return err
}

// Fail ends a pending operation that no execution owns, such as one whose
// claim cannot start.
func (r *Repo) Fail(ctx context.Context, id string, code ErrorCode, cause string, now time.Time) (bool, error) {
	ids, err := r.terminate(ctx, []string{id}, Termination{Status: StatusFailed, Code: code, Error: cause, At: now})
	return len(ids) == 1, err
}

// Cancel ends a pending operation for its caller. It reports false when the
// operation was not pending.
func (r *Repo) Cancel(ctx context.Context, id string, now time.Time) (bool, error) {
	ids, err := r.terminate(ctx, []string{id}, Termination{Status: StatusCancelled, At: now})
	return len(ids) == 1, err
}

// Supersede ends the pending operations f selects and returns their IDs.
func (r *Repo) Supersede(ctx context.Context, f Filter, now time.Time) ([]string, error) {
	var ended []string
	err := r.write(ctx, func(q Queries) error {
		f.Statuses = []Status{StatusPending}
		rows, err := q.Operations(ctx, f)
		if err != nil || len(rows) == 0 {
			return err
		}
		ended, err = r.terminateIn(ctx, q, ids(rows), Termination{Status: StatusSuperseded, At: now})
		return err
	})
	return ended, err
}

// Expire fails up to limit pending operations of kinds whose deadline
// passed and returns their IDs.
func (r *Repo) Expire(ctx context.Context, kinds []string, now time.Time, limit int) ([]string, error) {
	var ended []string
	at := UTC(now)
	err := r.write(ctx, func(q Queries) error {
		rows, err := q.Operations(ctx, Filter{Kinds: kinds, Statuses: []Status{StatusPending}, DeadlineBefore: &at, Limit: limit})
		if err != nil || len(rows) == 0 {
			return err
		}
		ended, err = r.terminateIn(ctx, q, ids(rows), Termination{Status: StatusFailed, Code: ErrorDeadlineExceeded, Error: "the operation's deadline passed", At: at})
		return err
	})
	return ended, err
}

func (r *Repo) terminate(ctx context.Context, ids []string, t Termination) ([]string, error) {
	var ended []string
	err := r.write(ctx, func(q Queries) (err error) {
		ended, err = r.terminateIn(ctx, q, ids, t)
		return err
	})
	return ended, err
}

// terminateIn ends the pending operations among ids and returns those it
// ended.
func (r *Repo) terminateIn(ctx context.Context, q Queries, ids []string, t Termination) ([]string, error) {
	t.At = UTC(t.At)
	rows, err := q.Operations(ctx, Filter{IDs: ids, Statuses: []Status{StatusPending}})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	pending := make([]string, len(rows))
	for i, op := range rows {
		pending[i] = op.ID
	}
	if _, err = q.TerminateOperations(ctx, pending, t); err != nil {
		return nil, err
	}
	if err = q.ReleaseOperationRetention(ctx, pending); err != nil {
		return nil, err
	}
	for _, id := range pending {
		if err = live.Notify(ctx, q, live.KindOperation, id, live.ActionUpdated); err != nil {
			return nil, err
		}
	}
	return pending, nil
}

// Finished returns the operations among ids that ended.
func (r *Repo) Finished(ctx context.Context, ids []string) ([]Operation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return r.read.Operations(ctx, Filter{IDs: ids, Finished: true})
}

// ReadyGroups returns the ready admission groups of kinds.
func (r *Repo) ReadyGroups(ctx context.Context, kinds []string, now time.Time) ([]Group, error) {
	return r.read.ReadyGroups(ctx, kinds, UTC(now))
}

// ReadyWorkspaces returns up to limit workspaces with ready work of kinds.
func (r *Repo) ReadyWorkspaces(ctx context.Context, kinds []string, now time.Time, limit int) ([]string, error) {
	return r.read.ReadyWorkspaces(ctx, kinds, UTC(now), limit)
}

// NextAttempt returns when pending work of kinds next becomes ready, or nil.
func (r *Repo) NextAttempt(ctx context.Context, kinds []string, now time.Time) (*time.Time, error) {
	return r.read.NextAttempt(ctx, kinds, UTC(now))
}

// CollectExpired deletes at most limit finished operations not updated since
// the cutoff, with their item queues, and reports how many it deleted.
func (r *Repo) CollectExpired(ctx context.Context, cutoff time.Time, keep []string, limit int) (int, error) {
	n := 0
	err := r.write(ctx, func(q Queries) error {
		through := cutoff.UTC()
		rows, err := q.Operations(ctx, Filter{Finished: true, UpdatedThrough: &through, ExcludeKinds: keep, Limit: limit})
		if err != nil || len(rows) == 0 {
			return err
		}
		n = len(rows)
		if err := q.DeleteOperationItems(ctx, ids(rows)); err != nil {
			return err
		}
		return q.DeleteOperations(ctx, ids(rows))
	})
	return n, err
}

func ids(rows []Operation) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.ID
	}
	return out
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
