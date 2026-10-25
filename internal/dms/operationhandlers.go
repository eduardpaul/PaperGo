package dms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"regexp"
	"slices"
	"sync"
	"time"
)

const OperationKindFieldIndex = "field_index"
const OperationKindBulkUpdate = "bulk_update"

// OperationKindRun executes a workflow run; the workflow runtime
// registers its handler.
const OperationKindRun = "script.run"
const OperationRetention = 30 * 24 * time.Hour

// A step performs one bounded, transactional batch and persists its resume
// point or terminal status with SaveOperation. Register handlers before
// serving requests/workers.
type OperationHandler struct {
	Scope    string
	Validate func(context.Context, *Service, string, string, json.RawMessage) error
	// Deadline is the completion deadline of accepted operations; zero
	// means none.
	Deadline time.Duration
	// Admission returns the admission group and input identity of an
	// accepted payload, for kinds a worker admits in groups: operations
	// of one group run together, and operations with the same input count
	// once toward a threshold.
	Admission func(json.RawMessage) (group, input string, err error)
	Step      func(context.Context, *Service, *operations.Operation) error
	// Execute runs outside the database writer under the operation's claim,
	// and owns its checkpoint transactions. Network integrations must never
	// fetch in a write transaction. A Retry error defers the operation.
	Execute func(context.Context, *Service, *operations.Operation) error
	// ExecuteGroup runs claimed members of one admission group together
	// outside the writer, for workers that admit the kind in groups. It
	// finishes or parks each member under its claim; a member it leaves
	// pending is given back. A Retry error defers them all.
	ExecuteGroup func(context.Context, *Service, []*operations.Operation) error
	// Failed and Cancelled run in the transaction that ends the operation.
	Failed    func(context.Context, *Service, *operations.Operation, error) error
	Cancelled func(context.Context, *Service, *operations.Operation) error
	// External operations are never executed: a domain action ends them,
	// such as the response that completes a task. They are accepted parked.
	External bool
	// KeepFinished keeps finished operations past OperationRetention:
	// their owner deletes them, such as workflow runs, whose history has
	// its own retention.
	KeepFinished bool
	// MaxAttempts bounds the executions an operation may begin, including
	// those a stopped process or a retry ended; zero means
	// DefaultMaxAttempts. Past it the operation fails with
	// retries_exhausted.
	MaxAttempts int
}

// DefaultMaxAttempts bounds an operation's executions when its handler
// sets no bound.
const DefaultMaxAttempts = 5

func (h OperationHandler) maxAttempts() int {
	if h.MaxAttempts > 0 {
		return h.MaxAttempts
	}
	return DefaultMaxAttempts
}

func builtInOperationHandlers() map[string]OperationHandler {
	return map[string]OperationHandler{
		OperationKindIntegrationRefresh: integrationOperationHandler(),
		OperationKindFieldIndex: {Step: func(ctx context.Context, s *Service, op *operations.Operation) error {
			return s.fieldIndexBatch(ctx, op)
		}, Failed: func(ctx context.Context, s *Service, op *operations.Operation, cause error) error {
			err := s.DAL.Catalog().SetIndexStatus(ctx, []string{op.FieldID}, catalog.IndexFailed)
			if dberr.IsNotFound(err) {
				return nil
			}
			return err
		}},
		OperationKindBulkUpdate: {Scope: "item.write", Step: func(ctx context.Context, s *Service, op *operations.Operation) error {
			return s.bulkUpdateStep(ctx, op)
		}, Failed: func(ctx context.Context, s *Service, op *operations.Operation, cause error) error {
			return s.DAL.Operations().ClearItems(ctx, op.ID)
		}, Cancelled: func(ctx context.Context, s *Service, op *operations.Operation) error {
			return s.DAL.Operations().ClearItems(ctx, op.ID)
		}},
	}
}

var operationKind = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func (s *Service) RegisterOperationHandler(kind string, handler OperationHandler) error {
	executions := 0
	for _, set := range []bool{handler.Step != nil, handler.Execute != nil, handler.ExecuteGroup != nil, handler.External} {
		if set {
			executions++
		}
	}
	if !operationKind.MatchString(kind) || executions != 1 || handler.Validate == nil || handler.Deadline < 0 || handler.MaxAttempts < 0 {
		return invalid("operation handlers require a kind, a validator and exactly one way to run: a step, an execution, a group execution or an external end")
	}
	known := false
	for _, scope := range identity.Catalog {
		if scope.Name == handler.Scope {
			known = true
		}
	}
	if !known {
		return invalid("operation handler scope must belong to the scope catalog")
	}
	if _, exists := s.ops.handlers[kind]; exists {
		return ErrConflict
	}
	s.ops.handlers[kind] = handler
	return nil
}

// operationRuntime is the operation configuration every service bound to a
// transaction shares.
type operationRuntime struct {
	handlers map[string]OperationHandler
	// holder names this process's claims.
	holder   string
	quotas   OperationQuotas
	finished func([]string)
	// wakes are called when operations were accepted.
	wakeMu sync.Mutex
	wakes  []func()
	// running takes an operation out of this process's execution of it,
	// by operation ID.
	running sync.Map
}

// ended takes operations that ended, such as cancelled ones, out of this
// process's executions at once instead of at their next renewal, and then
// tells the consumers waiting on them.
func (r *operationRuntime) ended(ids []string) {
	for _, id := range ids {
		if leave, ok := r.running.Load(id); ok {
			leave.(func())()
		}
	}
	if r.finished != nil {
		r.finished(ids)
	}
}

// OperationQuotas bound the pending operations people may queue: per
// creator, per workspace and for the installation, and the payload bytes
// of a workspace. Zero leaves a bound off.
type OperationQuotas struct {
	PerCreator, PerWorkspace, Installation, WorkspacePayloadBytes int64
}

// ErrOperationQuota means accepting the operation would exceed a quota.
var ErrOperationQuota = errors.New("too many pending operations; try again later")

// SetOperationQuotas bounds operations accepted for people. Call it before
// the service is used.
func (s *Service) SetOperationQuotas(q OperationQuotas) { s.ops.quotas = q }

// OnOperationsFinished calls f after each committed write that ended
// operations, with their IDs, once the writer is free, so consumers waiting
// on them wake at once. It is a prompt, not a guarantee: a process can stop
// between the commit and the call, so consumers also reconcile from
// FinishedOperations. Call it before the service is used.
func (s *Service) OnOperationsFinished(f func(ids []string)) { s.ops.finished = f }

// Retry marks err as transient: the operation is deferred for after, or a
// minute when after is not positive, instead of failing.
func Retry(err error, after time.Duration) error {
	if after <= 0 {
		after = time.Minute
	}
	return &retryError{err: err, after: after}
}

type retryError struct {
	err   error
	after time.Duration
}

func (e *retryError) Error() string { return e.err.Error() }
func (e *retryError) Unwrap() error { return e.err }

type idempotencyKey struct{}

type parentKey struct{}

// WithParentOperation makes the operations accepted on ctx belong to the
// operation id, such as the workflow run whose request starts them: the
// parent owns them, and cancelling it cancels them. An operation a request
// only finds, such as one an idempotency key replays, keeps its owner.
func WithParentOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, parentKey{}, id)
}

var idempotencyPattern = regexp.MustCompile(`^[\x21-\x7e]{1,128}$`)

// WithIdempotencyKey makes the operation the next acceptance on ctx records
// idempotent under key: a retry with the same key and payload returns the
// operation it accepted.
func WithIdempotencyKey(ctx context.Context, key string) (context.Context, error) {
	if !idempotencyPattern.MatchString(key) {
		return ctx, invalid("Idempotency-Key must be 1 to 128 printable ASCII characters without spaces")
	}
	return context.WithValue(ctx, idempotencyKey{}, key), nil
}

// IdempotencyKey returns the idempotency key WithIdempotencyKey put on ctx,
// for work that a request records without accepting an operation, such as
// a manual workflow start.
func IdempotencyKey(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKey{}).(string)
	return key
}

// ErrIdempotencyConflict means the caller's idempotency key already names an
// operation with another payload.
var ErrIdempotencyConflict = operations.ErrIdempotencyConflict

// EnqueueOperation is used by registered feature endpoints. Its validation and
// permissions commit with the durable payload; payloads are never returned.
func (s *Service) EnqueueOperation(ctx context.Context, subject, containerID, kind string, payload json.RawMessage) (out *OperationView, err error) {
	handler, exists := s.ops.handlers[kind]
	if !exists || handler.Validate == nil {
		return nil, invalid("operation kind is not startable")
	}
	if len(payload) > 1<<20 || !json.Valid(payload) {
		return nil, invalid("operation payload must be valid JSON within 1 MiB")
	}
	err = s.write(ctx, func(t *Service) error {
		if err := t.operationPermission(ctx, subject, containerID, handler.Scope); err != nil {
			return err
		}
		var e error
		if out, e = t.replay(ctx, operations.Operation{ContainerID: containerID, Kind: kind, CreatedBy: subject, Payload: payload}); out != nil || e != nil {
			return e
		}
		if err := handler.Validate(ctx, t, subject, containerID, payload); err != nil {
			return err
		}
		op, replayed, e := t.enqueue(ctx, operations.Operation{ContainerID: containerID, Kind: kind, CreatedBy: subject, Payload: payload})
		if e != nil {
			return e
		}
		out = viewOperation(op, time.Now())
		out.Replayed = replayed
		if replayed {
			return nil
		}
		return t.auditCatalog(ctx, subject, "operation.create", map[string]any{"operation_id": out.ID})
	})
	if replay, ok := s.replayAfterConflict(ctx, operations.Operation{ContainerID: containerID, Kind: kind, CreatedBy: subject, Payload: payload}, err); ok {
		return replay, nil
	}
	return
}

// replayAfterConflict answers a keyed request whose acceptance lost a race
// with the same request from another process: the unique key refused the
// second insert, and the operation the first one accepted is the answer.
func (s *Service) replayAfterConflict(ctx context.Context, op operations.Operation, err error) (*OperationView, bool) {
	if _, keyed := ctx.Value(idempotencyKey{}).(string); !keyed || !errors.Is(err, ErrConflict) {
		return nil, false
	}
	out, e := read(ctx, s, func(t *Service) (*OperationView, error) { return t.replay(ctx, op) })
	return out, e == nil && out != nil
}

// enqueue accepts an operation for its creator in this trace, limited to
// the caller's access token when one authenticated the request, within the
// creator's quotas and idempotent under the request's key. It reports
// whether the key returned an operation accepted earlier.
func (s *Service) enqueue(ctx context.Context, op operations.Operation) (operations.Operation, bool, error) {
	if token, limited := identity.AccessTokenContext(ctx); limited {
		op.CredentialID, op.CredentialScopes = token.ID, token.Scopes
	}
	if key, ok := ctx.Value(idempotencyKey{}).(string); ok {
		op.IdempotencyKey, op.PayloadDigest = key, payloadDigest(op)
	}
	stored, replayed, err := s.accept(ctx, op, true)
	if errors.Is(err, operations.ErrIdempotencyConflict) {
		return stored, false, ErrIdempotencyConflict
	}
	return stored, replayed, err
}

// replay returns the operation the request's idempotency key already names
// for the same request, before the request is validated again against
// state that may have changed since; nil when there is none.
func (s *Service) replay(ctx context.Context, op operations.Operation) (*OperationView, error) {
	key, ok := ctx.Value(idempotencyKey{}).(string)
	if !ok {
		return nil, nil
	}
	existing, err := s.DAL.Operations().FindIdempotent(ctx, op.CreatedBy, op.Kind, op.ContainerID, key)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing.PayloadDigest != payloadDigest(op) {
		return nil, ErrIdempotencyConflict
	}
	out := viewOperation(existing, time.Now())
	out.Replayed = true
	return out, nil
}

// accept records a pending operation, attributed to its container's
// workspace and given its kind's deadline, and wakes the runner after the
// write commits. System work is not bound by quotas.
func (s *Service) accept(ctx context.Context, op operations.Operation, quota bool) (operations.Operation, bool, error) {
	now := time.Now()
	op.TraceID = s.traceID
	if parent, ok := ctx.Value(parentKey{}).(string); ok && op.ParentID == "" && parent != op.ID {
		op.ParentID = parent
	}
	if op.ContainerID != "" && op.WorkspaceID == "" {
		r, err := s.DAL.Resources().Resource(ctx, op.ContainerID)
		if err != nil {
			return operations.Operation{}, false, err
		}
		op.WorkspaceID = r.WorkspaceID
		if r.Kind == refs.KindWorkspace {
			op.WorkspaceID = r.ID
		}
	}
	handler := s.ops.handlers[op.Kind]
	if handler.External {
		op.ExecutionID = externalExecution
	}
	if handler.Deadline > 0 && op.Deadline == nil {
		deadline := now.Add(handler.Deadline)
		op.Deadline = &deadline
	}
	if handler.Admission != nil {
		group, input, err := handler.Admission(op.Payload)
		if err != nil {
			return operations.Operation{}, false, err
		}
		op.GroupKey, op.InputKey = group, input
	}
	if quota && op.IdempotencyKey == "" {
		if err := s.checkOperationQuota(ctx, op); err != nil {
			return operations.Operation{}, false, err
		}
	}
	stored, replayed, err := s.DAL.Operations().Accept(ctx, op, now)
	if err != nil || replayed {
		return stored, replayed, err
	}
	if quota && op.IdempotencyKey != "" {
		// A replay never counts against the quota; a new key does.
		if err = s.checkOperationQuota(ctx, op); err != nil {
			return operations.Operation{}, false, err
		}
	}
	s.wakeOperations()
	return stored, false, nil
}

// externalExecution parks external operations until a domain action ends
// them.
const externalExecution = "external"

// AcceptOperation accepts an operation PaperGo starts itself for
// op.CreatedBy, such as a workflow run or the operation a task's answer
// ends: the caller authorized it, so it is neither validated nor counted
// toward quotas. A given ID names it. Call it on a service bound to a
// write.
func (s *Service) AcceptOperation(ctx context.Context, op operations.Operation) (operations.Operation, error) {
	if _, ok := s.ops.handlers[op.Kind]; !ok {
		return operations.Operation{}, invalid("no handler runs operation kind " + op.Kind)
	}
	stored, _, err := s.accept(ctx, op, false)
	return stored, err
}

// EndExternalOperation ends a pending external operation with status, a
// result or a message. An operation that already ended is left alone.
// Call it on a service bound to a write.
func (s *Service) EndExternalOperation(ctx context.Context, id string, status operations.Status, result json.RawMessage, message string) error {
	op, err := s.DAL.Operations().Find(ctx, id)
	if err != nil {
		return err
	}
	if op.Status != operations.StatusPending {
		return nil
	}
	op.Status, op.Result, op.Error = status, result, message
	if err = s.FinishParkedOperation(ctx, &op, externalExecution); errors.Is(err, operations.ErrClaimLost) {
		return nil
	}
	return err
}

// checkOperationQuota counts the pending operations, including any this
// transaction already accepted.
func (s *Service) checkOperationQuota(ctx context.Context, op operations.Operation) error {
	q := s.ops.quotas
	if q == (OperationQuotas{}) {
		return nil
	}
	// Index maintenance follows configuration changes; it never uses up a
	// person's quota. Neither do the workflow runs PaperGo starts for a
	// person, nor the work an operation starts: a backlog of runs must not
	// refuse the requests those runs make. Workspace and installation
	// quotas still bound them.
	totals, err := s.DAL.Operations().PendingTotals(ctx, op.CreatedBy, op.WorkspaceID, []string{OperationKindFieldIndex}, []string{OperationKindRun})
	if err != nil {
		return err
	}
	if op.IdempotencyKey == "" {
		if op.ParentID == "" && op.Kind != OperationKindRun {
			totals.Creator++
		}
		totals.Installation++
		if op.WorkspaceID != "" {
			totals.Workspace++
			totals.WorkspacePayloadBytes += int64(len(op.Payload))
		}
	}
	over := func(n, limit int64) bool { return limit > 0 && n > limit }
	if over(totals.Creator, q.PerCreator) || over(totals.Installation, q.Installation) ||
		op.WorkspaceID != "" && (over(totals.Workspace, q.PerWorkspace) || over(totals.WorkspacePayloadBytes, q.WorkspacePayloadBytes)) {
		return ErrOperationQuota
	}
	return nil
}

// payloadDigest identifies what an operation does, so an idempotency key
// replays only the same request.
func payloadDigest(op operations.Operation) string {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(op.Payload))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		payload = string(op.Payload)
	}
	raw, _ := json.Marshal([]any{op.Kind, op.ContainerID, payload})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// wakeOperations wakes the workers once this write commits.
func (s *Service) wakeOperations() {
	if s.accepted != nil {
		*s.accepted = true
	}
}

// OnOperationsAccepted adds f to the functions called after each committed
// write that accepted operations, once the writer is free, so workers claim
// them at once.
func (s *Service) OnOperationsAccepted(f func()) {
	s.ops.wakeMu.Lock()
	s.ops.wakes = append(s.ops.wakes, f)
	s.ops.wakeMu.Unlock()
}

// wake tells everything waiting for work that operations were accepted.
func (r *operationRuntime) wake() {
	r.wakeMu.Lock()
	wakes := slices.Clone(r.wakes)
	r.wakeMu.Unlock()
	for _, f := range wakes {
		f()
	}
}

func (s *Service) operationPermission(ctx context.Context, subject, containerID, scope string) error {
	if err := identity.RequireActive(ctx, dal.NewStore(s.db).WithTx(s.tx), subject); err != nil {
		return ErrForbidden
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return err
	}
	if containerID == "" {
		return s.RequireScope(ctx, subject, "organization.manage")
	}
	_, err := s.authorize(ctx, subject, containerID, "write")
	return err
}
func (s *Service) operationContext(ctx context.Context, op *operations.Operation) (context.Context, error) {
	ctx = WithParentOperation(requestmeta.WithTrace(ctx, op.TraceID), op.ID)
	if op.CredentialID != "" {
		restored, err := identity.RestoreCredential(ctx, dal.NewStore(s.db).WithTx(s.tx), op.CreatedBy, op.CredentialID, op.CredentialScopes)
		if err != nil {
			return restored, ErrForbidden
		}
		ctx = restored
	}
	return ctx, nil
}

// SaveOperation writes a step's progress under the operation's claim, or
// ends the operation when the step set a terminal status. Writes of an
// execution that lost its claim fail with operations.ErrClaimLost.
func (s *Service) SaveOperation(ctx context.Context, op *operations.Operation) error {
	now := time.Now()
	if op.Status == operations.StatusPending {
		return s.DAL.Operations().Checkpoint(ctx, op, now)
	}
	if err := s.DAL.Operations().Finish(ctx, op, now); err != nil {
		return err
	}
	s.operationsFinished(op.ID)
	return nil
}

// ParkOperations makes claimed operations members of a durable module
// execution, such as a provider batch or a waiting script run,
// releasing their claims: they hold no worker until FinishParkedOperation
// ends them or UnparkOperations returns them to the queue. Call it on a
// service bound to a write.
func (s *Service) ParkOperations(ctx context.Context, ops []*operations.Operation, in operations.Parking) error {
	now := time.Now()
	for _, op := range ops {
		if err := s.DAL.Operations().Park(ctx, []string{op.ID}, op.Claim, in, now); err != nil {
			return err
		}
		op.ExecutionID, op.Claim = in.ExecutionID, operations.Claim{}
	}
	return nil
}

// SleepOperation gives a claimed operation back until at, without counting
// the attempt: an execution that made progress and continues then, such
// as a workflow run that sleeps. Call it on a service bound to a write.
func (s *Service) SleepOperation(ctx context.Context, op *operations.Operation, at time.Time) error {
	if err := s.DAL.Operations().Sleep(ctx, op.ID, op.Claim, at, time.Now()); err != nil {
		return err
	}
	op.Claim = operations.Claim{}
	return nil
}

// ReleaseOperation gives a claimed operation back, ready at once, without
// counting the attempt. Call it on a service bound to a write.
func (s *Service) ReleaseOperation(ctx context.Context, op *operations.Operation) error {
	if err := s.DAL.Operations().Release(ctx, op.ID, op.Claim, time.Now()); err != nil {
		return err
	}
	op.Claim = operations.Claim{}
	s.wakeOperations()
	return nil
}

// FinishParkedOperation ends op, a pending member of executionID, with its
// terminal status. operations.ErrClaimLost means it ended or left the
// execution meanwhile.
func (s *Service) FinishParkedOperation(ctx context.Context, op *operations.Operation, executionID string) error {
	if err := s.DAL.Operations().FinishMember(ctx, op, executionID, time.Now()); err != nil {
		return err
	}
	s.operationsFinished(op.ID)
	return nil
}

// UnparkOperations returns the pending members of executionID among ids
// to the queue, ready at at with cause as their error when there is one;
// members that ended stay ended.
func (s *Service) UnparkOperations(ctx context.Context, executionID string, ids []string, at time.Time, cause string) error {
	if err := s.DAL.Operations().Unpark(ctx, executionID, ids, at, cause, time.Now()); err != nil {
		return err
	}
	s.wakeOperations()
	return nil
}

// operationsFinished records operations this write ended, for the hook that
// runs after it commits.
func (s *Service) operationsFinished(ids ...string) {
	if s.finished != nil {
		*s.finished = append(*s.finished, ids...)
	}
}

// CancelOperation ends a pending operation for its creator, or for an
// organization manager. Index maintenance and PaperGo's own operations
// cannot be cancelled.
func (s *Service) CancelOperation(ctx context.Context, subject, id string) (out *OperationView, err error) {
	err = s.write(ctx, func(t *Service) error {
		op, e := t.DAL.Operations().Find(ctx, id)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		// Index maintenance and PaperGo's own work complete what scheduled
		// them.
		// External operations end with what they stand for, such as a task.
		handler, startable := t.ops.handlers[op.Kind]
		if !startable || handler.External || handler.Validate == nil && op.Kind != OperationKindBulkUpdate || op.CreatedBy == SystemSubject {
			return ErrForbidden
		}
		if op.CreatedBy != subject {
			if e = t.RequireScope(ctx, subject, "organization.manage"); e != nil {
				return ErrNotFound
			}
		} else if op.ContainerID != "" {
			if _, e = t.authorize(ctx, subject, op.ContainerID, "read"); e != nil {
				return e
			}
		}
		ended, cancelled, e := t.cancelOperation(ctx, id)
		if e != nil {
			return e
		}
		if !cancelled {
			return ErrConflict
		}
		out = viewOperation(ended, time.Now())
		return t.auditCatalog(ctx, subject, "operation.cancel", map[string]any{"operation_id": id})
	})
	return
}

// cancelOperation ends a pending operation as cancelled, with its handler's
// cleanup, then the pending operations it owns, and reports whether it was
// pending.
func (s *Service) cancelOperation(ctx context.Context, id string) (operations.Operation, bool, error) {
	cancelled, err := s.DAL.Operations().Cancel(ctx, id, time.Now())
	if err != nil || !cancelled {
		return operations.Operation{}, false, err
	}
	s.operationsFinished(id)
	ended, err := s.DAL.Operations().Find(ctx, id)
	if err != nil {
		return operations.Operation{}, false, err
	}
	if handler := s.ops.handlers[ended.Kind]; handler.Cancelled != nil {
		if err = handler.Cancelled(ctx, s, &ended); err != nil {
			return operations.Operation{}, false, err
		}
	}
	owned, err := s.DAL.Operations().List(ctx, operations.Filter{ParentIDs: []string{id}, Statuses: []operations.Status{operations.StatusPending}})
	if err != nil {
		return operations.Operation{}, false, err
	}
	for _, child := range owned {
		if _, _, err = s.cancelOperation(ctx, child.ID); err != nil {
			return operations.Operation{}, false, err
		}
	}
	return ended, true, nil
}

// OwnedOperations returns the operations id owns, directly or through
// the operations it owns, whatever their status.
func (s *Service) OwnedOperations(ctx context.Context, id string) ([]string, error) {
	var out []string
	for parents := []string{id}; len(parents) > 0; {
		children, err := s.DAL.Operations().List(ctx, operations.Filter{ParentIDs: parents})
		if err != nil {
			return nil, err
		}
		parents = parents[:0]
		for _, c := range children {
			out, parents = append(out, c.ID), append(parents, c.ID)
		}
	}
	return out, nil
}

// CancelOperations cancels the pending operations among ids that PaperGo
// ends itself, such as a replaced workflow run, with their handlers'
// cleanup and the pending operations they own; those that ended are left.
// It checks no permissions. Call it on a service bound to a write.
func (s *Service) CancelOperations(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, _, err := s.cancelOperation(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Terminal payloads/results expire together. Pending work is retained for resume.
// Each pass deletes operationPurgePage rows and continues until the expired set is drained.
// Kinds whose handlers keep finished operations are left to their owners.
func (s *Service) CollectExpiredOperations(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-OperationRetention)
	var keep []string
	for kind, handler := range s.ops.handlers {
		if handler.KeepFinished {
			keep = append(keep, kind)
		}
	}
	for {
		var n int
		err := s.write(ctx, func(t *Service) error {
			var err error
			n, err = t.DAL.Operations().CollectExpired(ctx, cutoff, keep, operationPurgePage)
			return err
		})
		if err != nil || n < operationPurgePage {
			return err
		}
	}
}

const operationPurgePage = 200

// ExpireOperations fails pending operations whose deadline passed, with
// their handlers' failure cleanup.
func (s *Service) ExpireOperations(ctx context.Context, now time.Time) error {
	kinds := s.OperationKinds()
	at := now.UTC()
	// Checking first keeps an idle loop off the writer.
	if due, err := s.DAL.Operations().Exists(ctx, operations.Filter{Kinds: kinds, Statuses: []operations.Status{operations.StatusPending}, DeadlineBefore: &at}); err != nil || !due {
		return err
	}
	for {
		var n int
		err := s.write(ctx, func(t *Service) error {
			ended, err := t.DAL.Operations().Expire(ctx, kinds, now, operationPurgePage)
			if err != nil {
				return err
			}
			n = len(ended)
			t.operationsFinished(ended...)
			for _, id := range ended {
				if err = t.operationFailed(ctx, id, errDeadline); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || n < operationPurgePage {
			return err
		}
	}
}

var errDeadline = errors.New("the operation's deadline passed")

// operationFailed runs the handler's failure cleanup for an operation that
// ended without its execution.
func (s *Service) operationFailed(ctx context.Context, id string, cause error) error {
	op, err := s.DAL.Operations().Find(ctx, id)
	if err != nil {
		return err
	}
	if handler, exists := s.ops.handlers[op.Kind]; exists && handler.Failed != nil {
		return handler.Failed(ctx, s, &op, cause)
	}
	return nil
}

func executeOperation(ctx context.Context, s *Service, handler OperationHandler, op *operations.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return handler.Step(ctx, s, op)
}

func executeExternalOperation(ctx context.Context, s *Service, execute func(context.Context, *Service, *operations.Operation) error, op *operations.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return execute(ctx, s, op)
}

func operationCheckpoint(op *operations.Operation) string {
	raw, _ := json.Marshal([]any{op.Surface, op.AfterItemID, op.Processed, op.Prepared, op.Matched, op.Updated, op.Failed, op.Progress, op.Failures, op.Error, op.Result})
	return string(raw)
}
