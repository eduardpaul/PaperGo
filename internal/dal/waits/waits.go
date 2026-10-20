// Package waits owns durable workflow rendezvous shared by every database
// driver. Drivers implement Queries; this package decides what those
// statements mean.
package waits

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/jsonutil"
)

// Status is where a wait is in its lifecycle.
type Status string

const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusClosed    Status = "closed"
)

func (s Status) Valid() bool {
	return s == StatusOpen || s == StatusCompleted || s == StatusClosed
}

// claimTTL is how long a claim holds a wait before others may recover it.
const claimTTL = 10 * time.Minute

// Wait is a stored rendezvous. Empty claim fields and a nil completion are
// stored as NULL.
type Wait struct {
	ID, Kind, Key, WorkspaceID, RunID, Node, ExecutionID string
	Queue, PayloadID                                     string
	Data, Completion                                     json.RawMessage
	Deadline, NotifiedAt                                 *time.Time
	ClaimRunID, ClaimToken                               string
	ClaimUntil                                           *time.Time
	Status                                               Status
	Parking                                              bool
	CreatedAt, UpdatedAt                                 time.Time
}

// PutInput describes one rendezvous. Payload references and file leases are
// retained by the calling application transaction.
type PutInput struct {
	Kind, Key, WorkspaceID, RunID, Node, ExecutionID, Queue, PayloadID string
	Data                                                               json.RawMessage
	Parking                                                            bool
	Deadline                                                           *time.Time
}

// ClaimInput selects open waits of a kind for a holder. Limit is capped at
// 100.
type ClaimInput struct {
	Kind, Key, WorkspaceID, Holder string
	Limit                          int
}

// Filter selects waits ordered by ID. After continues after an ID.
type Filter struct {
	Kind, Key, WorkspaceID, RunID, Queue, After string
	RunIDs                                      []string
	Statuses                                    []Status
	Parking, Unnotified                         bool
	DeadlineBefore                              *time.Time
	Limit                                       int
}

// Claim selects the waits a claim statement may take: open, of a kind and
// either unclaimed, held by the holder or expired at Now.
type Claim struct {
	ClaimInput
	Now time.Time
}

// Authority is the original workflow version's author and the source files
// retained for a wait. Permission decisions stay in the caller.
type Authority struct {
	Author, WorkspaceID, ItemID string
	Sources                     []Source
}

// Source is a file a wait retains through a blob lease.
type Source struct{ ItemID, BlobID string }

// Execution identifies what a run executes.
type Execution struct {
	WorkflowID, WorkspaceID, ItemID string
	WorkflowVersion                 int64
}

// Queries are the statements a driver runs for waits. Each method executes on
// the transaction or connection the driver was bound to. Methods returning a
// count report how many waits they changed.
type Queries interface {
	refs.Queries
	Wait(ctx context.Context, kind, key string) (Wait, error)
	// LockWait returns the current wait after serializing writers of it.
	LockWait(ctx context.Context, kind, key string) (Wait, error)
	Waits(ctx context.Context, f Filter) ([]Wait, error)
	// InsertWait inserts w unless a wait with its kind and key exists.
	InsertWait(ctx context.Context, w Wait) error
	SetWaitParking(ctx context.Context, id string, at time.Time) error
	// CompleteWait completes an open wait and clears its claim.
	CompleteWait(ctx context.Context, kind, key string, completion json.RawMessage, at time.Time) (int64, error)
	// ClaimableWait returns the oldest wait c may take, skipping IDs in seen.
	ClaimableWait(ctx context.Context, c Claim, seen []string) (Wait, error)
	// ClaimWait gives wait id to c's holder until until, when c may still
	// take it.
	ClaimWait(ctx context.Context, id string, c Claim, token string, until time.Time) (Wait, int64, error)
	// ConsumeWait ends parking of a run's settled wait.
	ConsumeWait(ctx context.Context, kind, key, runID string, at time.Time) (int64, error)
	// CloseWait closes a run's open wait and clears its claim.
	CloseWait(ctx context.Context, kind, key, runID string, at time.Time) (int64, error)
	// CloseRunWaits closes a run's open waits, ends parking of its waits
	// and clears the claims it holds on open waits.
	CloseRunWaits(ctx context.Context, runID string, at time.Time) error
	DeleteRunWaits(ctx context.Context, runIDs []string) error
	// UpdateWaitData replaces an open wait's data for its run or for a
	// holder with an unexpired claim token.
	UpdateWaitData(ctx context.Context, kind, key, holder, token string, data json.RawMessage, at time.Time) (int64, error)
	// ReleaseWaitClaim clears the claim a holder's token holds on an open wait.
	ReleaseWaitClaim(ctx context.Context, kind, key, holder, token string, at time.Time) (int64, error)
	SetWaitNotified(ctx context.Context, id string, at time.Time) error

	WaitByID(ctx context.Context, id string) (Wait, error)
	RunExecution(ctx context.Context, runID string) (Execution, error)
	VersionAuthor(ctx context.Context, workflowID string, number int64) (string, error)
	// LeasedSources returns the files leased to an owner.
	LeasedSources(ctx context.Context, ownerID string) ([]Source, error)
}

// Repo runs wait reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds wait rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (r *Repo) Find(ctx context.Context, kind, key string) (Wait, error) {
	return r.read.Wait(ctx, kind, key)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Wait, error) {
	return r.read.Waits(ctx, f)
}

// Put inserts a wait. A replay of the same run, execution, workspace, node and
// payload keeps the first data; other identities conflict. Parking can be
// acquired on replay unless the wait is closed.
func (r *Repo) Put(ctx context.Context, in PutInput) (Wait, error) {
	var w Wait
	err := r.write(ctx, func(q Queries) error {
		// Validate the run's workspace even on replay; both references must agree.
		ok, err := q.RunInWorkspace(ctx, in.RunID, in.WorkspaceID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: wait run must belong to its workspace", dberr.ErrInvalidReference)
		}
		at := now()
		data := in.Data
		if len(data) == 0 {
			data = json.RawMessage(`{}`)
		}
		var deadline *time.Time
		if in.Deadline != nil && !in.Deadline.IsZero() {
			d := in.Deadline.UTC()
			deadline = &d
		}
		if err := q.InsertWait(ctx, Wait{
			ID: uuid.NewString(), Kind: in.Kind, Key: in.Key, WorkspaceID: in.WorkspaceID, RunID: in.RunID, Node: in.Node,
			ExecutionID: in.ExecutionID, Queue: in.Queue, PayloadID: in.PayloadID, Data: data, Deadline: deadline,
			Status: StatusOpen, Parking: in.Parking, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			return err
		}
		if w, err = q.LockWait(ctx, in.Kind, in.Key); err != nil {
			return err
		}
		if w.RunID != in.RunID || w.ExecutionID != in.ExecutionID || w.WorkspaceID != in.WorkspaceID || w.Node != in.Node || w.PayloadID != in.PayloadID {
			return dberr.ErrConcurrentUpdate
		}
		// A completed wait can park during replay, but a closed wait stays closed.
		if in.Parking && w.Status != StatusClosed && !w.Parking {
			if err := q.SetWaitParking(ctx, w.ID, at); err != nil {
				return err
			}
			w.Parking, w.UpdatedAt = true, at
		}
		return nil
	})
	return w, err
}

// Complete transitions an open wait once and accepts an equivalent completion
// on replay, regardless of object order and numeric spelling.
func (r *Repo) Complete(ctx context.Context, kind, key string, completion json.RawMessage) error {
	n, err := r.read.CompleteWait(ctx, kind, key, completion, now())
	if err != nil || n == 1 {
		return err
	}
	w, err := r.read.Wait(ctx, kind, key)
	if err != nil {
		return err
	}
	if w.Status == StatusCompleted && jsonutil.Equal(w.Completion, completion) {
		return nil
	}
	return dberr.ErrConcurrentUpdate
}

// Claim takes open waits for a holder with one conditional write per
// candidate, so competing holders cannot receive the same wait. A holder may
// refresh its own claim, and expired claims are recovered. It returns the
// claimed waits and their new token.
func (r *Repo) Claim(ctx context.Context, in ClaimInput) ([]Wait, string, error) {
	token := uuid.NewString()
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 100
	}
	c := Claim{ClaimInput: in, Now: now()}
	var out []Wait
	err := r.write(ctx, func(q Queries) error {
		var seen []string
		for len(out) < in.Limit {
			w, err := q.ClaimableWait(ctx, c, seen)
			if dberr.IsNotFound(err) {
				break
			}
			if err != nil {
				return err
			}
			seen = append(seen, w.ID)
			// The claim rechecks availability: on PostgreSQL another holder
			// may have taken the wait while this one waited for its row lock.
			claimed, n, err := q.ClaimWait(ctx, w.ID, c, token, c.Now.Add(claimTTL))
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			out = append(out, claimed)
			if in.Key != "" {
				break
			}
		}
		return nil
	})
	return out, token, err
}

// Consume ends parking of a run's completed or closed wait.
func (r *Repo) Consume(ctx context.Context, kind, key, runID string) error {
	return found(r.read.ConsumeWait(ctx, kind, key, runID, now()))
}

// Close closes a run's open wait.
func (r *Repo) Close(ctx context.Context, kind, key, runID string) error {
	return found(r.read.CloseWait(ctx, kind, key, runID, now()))
}

// CloseRun closes a run's open waits, ends parking of all its waits and
// releases the claims it holds.
func (r *Repo) CloseRun(ctx context.Context, runID string) error {
	return r.write(ctx, func(q Queries) error { return q.CloseRunWaits(ctx, runID, now()) })
}

func (r *Repo) DeleteRuns(ctx context.Context, runIDs []string) error {
	if len(runIDs) == 0 {
		return nil
	}
	return r.read.DeleteRunWaits(ctx, runIDs)
}

// UpdateData replaces an open wait's data for its run, or for a holder whose
// claim token has not expired.
func (r *Repo) UpdateData(ctx context.Context, kind, key, holder, token string, data json.RawMessage) error {
	return found(r.read.UpdateWaitData(ctx, kind, key, holder, token, data, now()))
}

// ReleaseClaim clears the claim a holder's token holds on an open wait.
func (r *Repo) ReleaseClaim(ctx context.Context, kind, key, holder, token string) error {
	return found(r.read.ReleaseWaitClaim(ctx, kind, key, holder, token, now()))
}

func (r *Repo) MarkNotified(ctx context.Context, id string, at time.Time) error {
	return r.read.SetWaitNotified(ctx, id, at.UTC())
}

// Authority returns the authority a wait executes with: the author of its
// run's workflow version and the files leased to the wait.
func (r *Repo) Authority(ctx context.Context, id string) (Authority, error) {
	w, err := r.read.WaitByID(ctx, id)
	if err != nil {
		return Authority{}, err
	}
	run, err := r.read.RunExecution(ctx, w.RunID)
	if err != nil {
		return Authority{}, err
	}
	if run.WorkspaceID != w.WorkspaceID {
		return Authority{}, fmt.Errorf("%w: wait and run must share a workspace", dberr.ErrInvalidReference)
	}
	author, err := r.read.VersionAuthor(ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil {
		return Authority{}, err
	}
	sources, err := r.read.LeasedSources(ctx, w.ID)
	if err != nil {
		return Authority{}, err
	}
	return Authority{Author: author, WorkspaceID: run.WorkspaceID, ItemID: run.ItemID, Sources: sources}, nil
}

// found reports a conditional change that matched no wait as not found.
func found(n int64, err error) error {
	if err == nil && n == 0 {
		return dberr.ErrNotFound
	}
	return err
}
