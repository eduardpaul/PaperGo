package dms

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/surfaces"
	"papergo/internal/model"
	"slices"
	"time"
)

// Collection-wide index maintenance runs as tracked operations. Changing a
// field's indexing commits at once and marks the field building; queries reject
// it until RunOperations has converged its field_values rows, one operationBatch
// per write transaction, and marked it ready. Item writes between batches index
// the field themselves (indexedDefinitions follows the live definition), and a
// batch re-derives rows from the surface it reads, so the result is exact.

// enqueueFieldIndex supersedes the field's earlier operation and queues one
// that converges its index rows. A collection without items needs no rows.
func (s *Service) enqueueFieldIndex(ctx context.Context, subject string, d *catalog.FieldDefinition) error {
	ended, err := s.DAL.Operations().Supersede(ctx, operations.Filter{FieldID: d.ID}, time.Now())
	if err != nil {
		return err
	}
	s.operationsFinished(ended...)
	collections, err := s.fieldCollections(ctx, d.ID)
	if err != nil {
		return err
	}
	queued := false
	for _, c := range collections {
		populated, e := s.DAL.Surfaces().Exists(ctx, surfaces.Filter{ContainerID: c.ID})
		if e != nil {
			return e
		}
		if !populated {
			continue
		}
		if _, _, e = s.accept(ctx, operations.Operation{ContainerID: c.ID, FieldID: d.ID, Kind: OperationKindFieldIndex, CreatedBy: subject}, false); e != nil {
			return e
		}
		queued = true
	}
	status := catalog.IndexReady
	if queued {
		status = catalog.IndexBuilding
	}
	return s.DAL.Catalog().SetIndexStatus(ctx, []string{d.ID}, status)
}

// operationClaim is how long an execution owns an operation between
// renewals; an execution renews at a third of it.
const operationClaim = 2 * time.Minute

// RunOperations processes pending operations until ctx ends, resuming any left
// by an earlier process.
func (s *Service) RunOperations(ctx context.Context, log *slog.Logger) {
	nextCleanup, nextExpiry := time.Time{}, time.Time{}
	for {
		if now := time.Now().UTC(); !now.Before(nextCleanup) {
			if err := s.CollectExpiredOperations(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("operation cleanup failed", "error", err)
			}
			if err := s.CollectExpiredDeltas(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("delta cleanup failed", "error", err)
			}
			if err := s.CollectExpiredLiveChanges(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("live change cleanup failed", "error", err)
			}
			nextCleanup = now.Add(time.Hour)
		}
		if now := time.Now(); !now.Before(nextExpiry) {
			if err := s.ExpireOperations(ctx, now); err != nil && ctx.Err() == nil {
				log.Error("operation deadlines failed", "error", err)
			}
			nextExpiry = now.Add(time.Minute)
		}
		worked, err := s.operationStep(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("background operation failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if worked {
			continue
		}
		wait := time.Minute
		if next, e := s.DAL.Operations().NextAttempt(ctx, s.operationKinds(), time.Now()); e == nil && next != nil {
			wait = min(wait, max(time.Until(*next), 0))
		}
		select {
		case <-ctx.Done():
			return
		case <-s.operations:
		case <-time.After(wait):
		}
	}
}

// runOperations drains every pending operation that is ready now.
func (s *Service) runOperations(ctx context.Context) error {
	for {
		worked, err := s.operationStep(ctx)
		if err != nil || !worked {
			return err
		}
	}
}

// operationKinds lists the kinds this process runs.
func (s *Service) operationKinds() []string {
	kinds := make([]string, 0, len(s.ops.handlers))
	for kind := range s.ops.handlers {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// operationStep claims the oldest ready operation and advances it by one
// index batch or bulk item in the claim's transaction, or claims it for an
// execution outside the writer. An operation error fails it; item errors are
// bulk results; a Retry error defers it.
func (s *Service) operationStep(ctx context.Context) (bool, error) {
	var op *operations.Operation
	var execute func(context.Context, *Service, *operations.Operation) error
	err := s.write(ctx, func(t *Service) error {
		claimed, e := t.DAL.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: t.operationKinds(), Now: time.Now(), Limit: 1}, Holder: t.ops.holder, TTL: operationClaim})
		if e != nil || len(claimed) == 0 {
			op = nil
			return e
		}
		op = &claimed[0]
		handler := t.ops.handlers[op.Kind]
		ctx, e = t.operationContext(ctx, op)
		if e != nil {
			return e
		}
		t.traceID = op.TraceID
		// Field index builds and PaperGo's own search rebuilds act for no one.
		if op.Kind != OperationKindFieldIndex && (op.Kind != OperationKindSearchReindex || op.CreatedBy != systemSubject) {
			if e = t.operationPermission(ctx, op.CreatedBy, op.ContainerID, handler.Scope); e != nil {
				return e
			}
		}
		if op.StartedAt == nil {
			started := time.Now().UTC()
			op.StartedAt = &started
			if e = t.SaveOperation(ctx, op); e != nil {
				return e
			}
		}
		if handler.Execute != nil {
			execute = handler.Execute
			return nil
		}
		previousState := operationCheckpoint(op)
		if e = executeOperation(ctx, t, handler, op); e != nil {
			return e
		}
		current, e := t.DAL.Operations().Find(ctx, op.ID)
		if e != nil {
			return e
		}
		if current.Status != operations.StatusPending {
			return nil
		}
		if operationCheckpoint(&current) == previousState {
			return invalid("operation handler did not persist progress")
		}
		// The next batch is claimed again, so other work can go between.
		return t.DAL.Operations().Release(ctx, op.ID, op.Claim, time.Now())
	})
	if op == nil || err == nil && execute == nil {
		return op != nil, err
	}
	// A failed claim transaction rolled its claim back with the step.
	committed := err == nil
	if committed {
		err = s.executeClaimed(ctx, execute, op)
	}
	if err == nil {
		return true, nil
	}
	return true, s.endAttempt(ctx, op, committed, err)
}

// executeClaimed runs an execution outside the writer, renewing its claim
// until it returns. A lost claim cancels it.
func (s *Service) executeClaimed(ctx context.Context, execute func(context.Context, *Service, *operations.Operation) error, op *operations.Operation) error {
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(operationClaim / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				until := now.Add(operationClaim)
				// The token fences the execution's writes; its expiry lives
				// only in the database.
				if err := s.write(run, func(t *Service) error { return t.DAL.Operations().Renew(run, op.ID, op.Claim, until, now) }); errors.Is(err, operations.ErrClaimLost) {
					cancel(err)
					return
				}
			}
		}
	}()
	err := executeExternalOperation(run, s, execute, op)
	if cause := context.Cause(run); errors.Is(cause, operations.ErrClaimLost) {
		return cause
	}
	return err
}

// endAttempt settles an attempt that returned err: it gives a cancelled
// execution's claim back, defers a retry and fails anything else, with the
// handler's failure cleanup. An attempt whose claim rolled back claims the
// operation again first; an execution that lost its claim leaves the
// operation to its new owner.
func (s *Service) endAttempt(ctx context.Context, op *operations.Operation, committed bool, err error) error {
	if errors.Is(err, operations.ErrClaimLost) {
		return nil
	}
	cancelled := ctx.Err() != nil
	if cancelled {
		if !committed {
			return nil
		}
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		ctx = release
	}
	settled := s.write(ctx, func(t *Service) error {
		now := time.Now()
		current := *op
		if !committed {
			claimed, e := t.DAL.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{op.Kind}, IDs: []string{op.ID}, Now: now, Limit: 1}, Holder: t.ops.holder, TTL: operationClaim})
			if e != nil || len(claimed) == 0 {
				return e
			}
			current = claimed[0]
		}
		if cancelled {
			return t.DAL.Operations().Release(ctx, op.ID, current.Claim, now)
		}
		var retry *retryError
		if errors.As(err, &retry) {
			return t.DAL.Operations().Defer(ctx, op.ID, current.Claim, now.Add(retry.after), retry.Error(), now)
		}
		completed := now.UTC()
		current.Status, current.CompletedAt, current.Error = operations.StatusFailed, &completed, err.Error()
		if errors.Is(err, ErrForbidden) {
			current.ErrorCode = operations.ErrorForbidden
		}
		if current.StartedAt == nil {
			current.StartedAt = op.StartedAt
		}
		if e := t.SaveOperation(ctx, &current); e != nil {
			return e
		}
		if handler := t.ops.handlers[op.Kind]; handler.Failed != nil {
			return handler.Failed(ctx, t, &current, err)
		}
		return nil
	})
	if cancelled || errors.Is(settled, operations.ErrClaimLost) {
		return nil
	}
	var retry *retryError
	if settled != nil || errors.As(err, &retry) {
		return settled
	}
	return err
}

// operationBatch is the surfaces one background batch reindexes while holding
// the writer. Measured with BenchmarkWriteDuringIndexBuild at 2000 items, a
// concurrent edit waits about 50 ms extra behind 500-surface batches, 27 ms
// behind 100 and 20 ms behind 50, where per-commit costs start to dominate.
var operationBatch = 100

// fieldIndexBatch converges the field's rows on the next operationBatch surfaces
// and records the resume point, finishing after the published surfaces.
func (s *Service) fieldIndexBatch(ctx context.Context, op *operations.Operation) error {
	d, err := s.DAL.Catalog().Field(ctx, op.FieldID)
	if dberr.IsNotFound(err) {
		op.Status = operations.StatusSuperseded
		return s.SaveOperation(ctx, op)
	}
	if err != nil {
		return err
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: op.ContainerID, Surface: op.Surface, AfterItemID: op.AfterItemID, Limit: operationBatch})
	if err != nil {
		return err
	}
	if len(projections) > 0 {
		if err = s.reindexBatch(ctx, projections, d, map[string][]*catalog.FieldDefinition{}); err != nil {
			return err
		}
	}
	op.Processed += len(projections)
	switch {
	case len(projections) == operationBatch:
		op.AfterItemID = projections[len(projections)-1].ItemID
		return s.SaveOperation(ctx, op)
	case op.Surface == refs.SurfaceHead:
		op.Surface, op.AfterItemID = refs.SurfacePublished, ""
		return s.SaveOperation(ctx, op)
	}
	op.Status = operations.StatusSucceeded
	if err = s.SaveOperation(ctx, op); err != nil {
		return err
	}
	pending, err := s.DAL.Operations().Exists(ctx, operations.Filter{FieldID: d.ID, Statuses: []operations.Status{operations.StatusPending, operations.StatusFailed}})
	if err != nil {
		return err
	}
	if pending {
		return nil
	}
	return s.DAL.Catalog().SetIndexStatus(ctx, []string{d.ID}, catalog.IndexReady)
}

// Operations lists index maintenance and the caller's own bulk operations.
func (s *Service) Operations(ctx context.Context, subject, containerID, after string, limit int) (Page[*OperationView], error) {
	return read(ctx, s, func(t *Service) (Page[*OperationView], error) {
		if _, e := t.authorize(ctx, subject, containerID, "read"); e != nil {
			return Page[*OperationView]{}, e
		}
		rows, e := t.DAL.Operations().List(ctx, operations.Filter{ContainerID: containerID, AfterID: after, OwnedBy: subject, SharedKinds: []string{OperationKindFieldIndex}, Limit: pageSize(limit) + 1})
		views := make([]*OperationView, len(rows))
		now := time.Now()
		for i, row := range rows {
			views[i] = viewOperation(row, now)
		}
		return entityPage(views, limit, func(v *OperationView) string { return v.ID }), e
	})
}

// Operation requires collection read access; bulk results also require ownership.
func (s *Service) Operation(ctx context.Context, subject, id string) (*OperationView, error) {
	return read(ctx, s, func(t *Service) (*OperationView, error) {
		op, e := t.DAL.Operations().Find(ctx, id)
		if dberr.IsNotFound(e) {
			return nil, ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if op.Kind != OperationKindFieldIndex && op.CreatedBy != subject {
			return nil, ErrNotFound
		}
		if op.ContainerID == "" {
			if e = t.RequireScope(ctx, subject, "organization.manage"); e != nil {
				return nil, e
			}
			return viewOperation(op, time.Now()), nil
		}
		if _, e = t.authorize(ctx, subject, op.ContainerID, "read"); e != nil {
			return nil, e
		}
		return viewOperation(op, time.Now()), nil
	})
}

// FinishedOperations returns those of ids that ended, for consumers that
// reconcile what they wait for.
func (s *Service) FinishedOperations(ctx context.Context, ids []string) ([]operations.Operation, error) {
	return s.DAL.Operations().Finished(ctx, ids)
}

// queryable reports whether queries may filter, sort or group by a field.
func queryable(d *catalog.FieldDefinition) bool {
	return d.Indexed && d.IndexStatus == catalog.IndexReady
}

// unqueryable explains why a custom field cannot be queried.
func unqueryable(key string, d *catalog.FieldDefinition) error {
	if d != nil && d.Indexed {
		return invalid("field index is " + string(d.IndexStatus) + ": " + key)
	}
	return invalid("query fields must exist and be indexed: " + key)
}

// OperationWaiting is why a pending operation has not finished.
type OperationWaiting string

const (
	WaitingQueued    OperationWaiting = "queued"
	WaitingRunning   OperationWaiting = "running"
	WaitingRetry     OperationWaiting = "retry"
	WaitingScheduled OperationWaiting = "scheduled"
	WaitingRemote    OperationWaiting = "remote"
)

// OperationView is an operation as the API shows it. Payloads, credential
// constraints, claims and resume points stay private.
type OperationView struct {
	ID            string                   `json:"id"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
	ContainerID   string                   `json:"container_id,omitempty"`
	WorkspaceID   string                   `json:"workspace_id,omitempty"`
	FieldID       string                   `json:"field_id"`
	Kind          string                   `json:"kind"`
	TraceID       string                   `json:"trace_id,omitempty"`
	Status        operations.Status        `json:"status"`
	Waiting       OperationWaiting         `json:"waiting,omitempty"`
	Attempts      int                      `json:"attempts,omitempty"`
	NextAttemptAt *time.Time               `json:"next_attempt_at,omitempty"`
	Deadline      *time.Time               `json:"deadline,omitempty"`
	Result        json.RawMessage          `json:"result,omitempty"`
	StartedAt     *time.Time               `json:"started_at,omitempty"`
	CompletedAt   *time.Time               `json:"completed_at,omitempty"`
	Processed     int                      `json:"processed,omitempty"`
	Matched       int                      `json:"matched,omitempty"`
	Updated       int                      `json:"updated,omitempty"`
	Failed        int                      `json:"failed,omitempty"`
	Progress      int                      `json:"progress,omitempty"`
	Failures      []model.OperationFailure `json:"failures,omitempty"`
	Error         string                   `json:"error,omitempty"`
	ErrorCode     operations.ErrorCode     `json:"error_code,omitempty"`
	CreatedBy     string                   `json:"created_by,omitempty"`
	// Replayed is set when an idempotency key returned an operation
	// accepted earlier.
	Replayed bool `json:"-"`
}

func viewOperation(op operations.Operation, now time.Time) *OperationView {
	v := &OperationView{
		ID: op.ID, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, ContainerID: op.ContainerID, WorkspaceID: op.WorkspaceID, FieldID: op.FieldID, Kind: op.Kind,
		TraceID: op.TraceID, Status: op.Status, Attempts: op.Attempts, Deadline: op.Deadline, Result: op.Result, StartedAt: op.StartedAt, CompletedAt: op.CompletedAt,
		Processed: op.Processed, Matched: op.Matched, Updated: op.Updated, Failed: op.Failed, Progress: op.Progress,
		Failures: op.Failures, Error: op.Error, ErrorCode: op.ErrorCode, CreatedBy: op.CreatedBy,
	}
	if op.Status != operations.StatusPending {
		return v
	}
	switch {
	case op.ExecutionID != "":
		v.Waiting = WaitingRemote
	case op.Claim.Token != "" && op.Claim.Until.After(now):
		v.Waiting = WaitingRunning
	case op.AttemptAt.After(now):
		v.Waiting = WaitingScheduled
		if op.Attempts > 0 {
			v.Waiting = WaitingRetry
		}
		next := op.AttemptAt
		v.NextAttemptAt = &next
	default:
		v.Waiting = WaitingQueued
	}
	return v
}
