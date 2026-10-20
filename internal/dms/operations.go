package dms

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/surfaces"
	"papergo/internal/model"
	"slices"
	"sync"
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
// renewals; an execution renews at a third of it. inlineClaim covers a
// transactional step, whose claim never outlives its transaction.
const (
	operationClaim = 2 * time.Minute
	inlineClaim    = 24 * time.Hour
)

// OperationKinds lists the kinds this process executes; kinds a domain
// action completes are not among them.
func (s *Service) OperationKinds() []string {
	kinds := make([]string, 0, len(s.ops.handlers))
	for kind, handler := range s.ops.handlers {
		if !handler.External {
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

// OperationRunsInGroups reports whether kind's handler executes admission
// groups, which only a group policy runs.
func (s *Service) OperationRunsInGroups(kind string) bool {
	return s.ops.handlers[kind].ExecuteGroup != nil
}

// NextOperationAttempt returns when pending work of kinds next becomes
// ready, or nil when nothing waits for time.
func (s *Service) NextOperationAttempt(ctx context.Context, kinds []string) (*time.Time, error) {
	return s.DAL.Operations().NextAttempt(ctx, kinds, time.Now())
}

// ReadyOperationGroups returns the admission groups of kinds with ready
// work, for workers that admit operations in groups.
func (s *Service) ReadyOperationGroups(ctx context.Context, kinds []string) ([]operations.Group, error) {
	return s.DAL.Operations().ReadyGroups(ctx, kinds, time.Now())
}

// RunOperation claims the oldest ready operation of kinds and advances it
// by one index batch or bulk item in the claim's transaction, or claims it
// for an execution outside the writer. An operation error fails it; item
// errors are bulk results; a Retry error defers it. It reports whether it
// found work.
func (s *Service) RunOperation(ctx context.Context, kinds []string) (bool, error) {
	var op *operations.Operation
	var execute func(context.Context, *Service, *operations.Operation) error
	err := s.write(ctx, func(t *Service) error {
		op, execute = nil, nil
		claimed, e := t.DAL.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: kinds, Now: time.Now(), Limit: 1}, Holder: t.ops.holder, TTL: operationClaim})
		if e != nil || len(claimed) == 0 {
			return e
		}
		op = &claimed[0]
		handler := t.ops.handlers[op.Kind]
		if op.Attempts > handler.maxAttempts() {
			// Work that keeps stopping its process or asking to retry ends
			// here instead of coming back after every claim expiry.
			cause := errors.New("the operation's attempts ran out")
			completed := time.Now().UTC()
			op.Status, op.ErrorCode, op.Error, op.CompletedAt = operations.StatusFailed, operations.ErrorRetriesExhausted, cause.Error(), &completed
			if e = t.SaveOperation(ctx, op); e != nil || handler.Failed == nil {
				return e
			}
			return handler.Failed(ctx, t, op, cause)
		}
		if handler.Execute == nil {
			// A step runs inside the claim's own transaction, which nobody
			// else sees until it ends, so its claim must outlast the step
			// however long it takes; it ends with the transaction.
			now := time.Now()
			until := now.Add(inlineClaim)
			if e = t.DAL.Operations().Renew(ctx, op.ID, op.Claim, until, now); e != nil {
				return e
			}
			op.Claim.Until = operations.UTC(until)
		}
		ctx, e = t.operationContext(ctx, op)
		if e != nil {
			return e
		}
		t.traceID = op.TraceID
		if actsForSomeone(op) {
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
	if err != nil && dberr.IsRetryable(err) {
		// The database refused the claim's transaction, not the work: the
		// operation stays ready for the next pass.
		return true, err
	}
	// A failed claim transaction rolled its claim back with the step.
	committed := err == nil
	if committed {
		err = s.executeClaimed(ctx, []*operations.Operation{op}, func(run context.Context) error { return executeExternalOperation(run, s, execute, op) })
	}
	if err == nil {
		return true, nil
	}
	return true, s.endAttempt(ctx, op, committed, err)
}

// executeClaimed runs an execution of claimed operations outside the
// writer, renewing their claims until it returns. A member whose operation
// ends meanwhile, or whose claim is lost, leaves the execution; the
// execution is cancelled once no member is left, and then reports
// operations.ErrClaimLost.
func (s *Service) executeClaimed(ctx context.Context, ops []*operations.Operation, execute func(context.Context) error) error {
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var mu sync.Mutex
	live := map[string]operations.Claim{}
	for _, op := range ops {
		live[op.ID] = op.Claim
	}
	leave := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		delete(live, id)
		if len(live) == 0 {
			cancel(operations.ErrClaimLost)
		}
	}
	for _, op := range ops {
		s.ops.running.Store(op.ID, func() { leave(op.ID) })
		defer s.ops.running.Delete(op.ID)
	}
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
				mu.Lock()
				claims := maps.Clone(live)
				mu.Unlock()
				now := time.Now()
				until := now.Add(operationClaim)
				// The tokens fence the execution's writes; their expiry lives
				// only in the database.
				for id, claim := range claims {
					if err := s.write(run, func(t *Service) error { return t.DAL.Operations().Renew(run, id, claim, until, now) }); errors.Is(err, operations.ErrClaimLost) {
						leave(id)
					}
				}
			}
		}
	}()
	err := execute(run)
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
	ParentID      string                   `json:"parent_id,omitempty"`
	// Replayed is set when an idempotency key returned an operation
	// accepted earlier.
	Replayed bool `json:"-"`
}

// actsForSomeone reports whether op runs with its creator's permissions:
// field index builds and the operations PaperGo starts itself act for no
// one.
func actsForSomeone(op *operations.Operation) bool {
	return op.Kind != OperationKindFieldIndex && op.CreatedBy != systemSubject
}

func viewOperation(op operations.Operation, now time.Time) *OperationView {
	v := &OperationView{
		ID: op.ID, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, ContainerID: op.ContainerID, WorkspaceID: op.WorkspaceID, FieldID: op.FieldID, Kind: op.Kind,
		TraceID: op.TraceID, Status: op.Status, Attempts: op.Attempts, Deadline: op.Deadline, Result: op.Result, StartedAt: op.StartedAt, CompletedAt: op.CompletedAt,
		Processed: op.Processed, Matched: op.Matched, Updated: op.Updated, Failed: op.Failed, Progress: op.Progress,
		Failures: op.Failures, Error: op.Error, ErrorCode: op.ErrorCode, CreatedBy: op.CreatedBy, ParentID: op.ParentID,
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
