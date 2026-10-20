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
	"time"
)

const OperationKindFieldIndex = "field_index"
const OperationKindBulkUpdate = "bulk_update"
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
	Step     func(context.Context, *Service, *operations.Operation) error
	// Execute runs outside the database writer under the operation's claim,
	// and owns its checkpoint transactions. Network integrations must never
	// fetch in a write transaction. A Retry error defers the operation.
	Execute func(context.Context, *Service, *operations.Operation) error
	// Failed and Cancelled run in the transaction that ends the operation.
	Failed    func(context.Context, *Service, *operations.Operation, error) error
	Cancelled func(context.Context, *Service, *operations.Operation) error
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
		OperationKindSearchReindex: searchReindexHandler(),
		OperationKindBulkUpdate: {Scope: "list.write", Step: func(ctx context.Context, s *Service, op *operations.Operation) error {
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
	if !operationKind.MatchString(kind) || (handler.Step == nil) == (handler.Execute == nil) || handler.Validate == nil || handler.Deadline < 0 {
		return invalid("operation handlers require a kind, a validator and either a step or an execution")
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
// operations, with their IDs, so consumers waiting on them wake at once.
// Call it before the service is used.
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
	return
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
	if handler := s.ops.handlers[op.Kind]; handler.Deadline > 0 && op.Deadline == nil {
		deadline := now.Add(handler.Deadline)
		op.Deadline = &deadline
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

// checkOperationQuota counts the pending operations, including any this
// transaction already accepted.
func (s *Service) checkOperationQuota(ctx context.Context, op operations.Operation) error {
	q := s.ops.quotas
	if q == (OperationQuotas{}) {
		return nil
	}
	// Index maintenance follows configuration changes; it never uses up a
	// person's quota.
	totals, err := s.DAL.Operations().PendingTotals(ctx, op.CreatedBy, op.WorkspaceID, []string{OperationKindFieldIndex})
	if err != nil {
		return err
	}
	if op.IdempotencyKey == "" {
		totals.Creator, totals.Installation = totals.Creator+1, totals.Installation+1
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

// wakeOperations wakes the runner once this write commits; the runner's
// claim waits for the writer slot, so it sees the operation.
func (s *Service) wakeOperations() {
	select {
	case s.operations <- struct{}{}:
	default:
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
	ctx = requestmeta.WithTrace(ctx, op.TraceID)
	if op.CredentialID != "" {
		ctx = identity.WithAccessToken(ctx, identity.AccessTokenIdentity{Subject: op.CreatedBy, ID: op.CredentialID, Scopes: op.CredentialScopes})
		if err := identity.RequireCredential(ctx, dal.NewStore(s.db).WithTx(s.tx), op.CreatedBy); err != nil {
			return ctx, ErrForbidden
		}
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
		handler, startable := t.ops.handlers[op.Kind]
		if !startable || handler.Validate == nil && op.Kind != OperationKindBulkUpdate || op.CreatedBy == systemSubject {
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
		cancelled, e := t.DAL.Operations().Cancel(ctx, id, time.Now())
		if e != nil {
			return e
		}
		if !cancelled {
			return ErrConflict
		}
		t.operationsFinished(id)
		ended, e := t.DAL.Operations().Find(ctx, id)
		if e != nil {
			return e
		}
		if handler.Cancelled != nil {
			if e = handler.Cancelled(ctx, t, &ended); e != nil {
				return e
			}
		}
		out = viewOperation(ended, time.Now())
		return t.auditCatalog(ctx, subject, "operation.cancel", map[string]any{"operation_id": id})
	})
	return
}

// Terminal payloads/results expire together. Pending work is retained for resume.
// Each pass deletes operationPurgePage rows and continues until the expired set is drained.
func (s *Service) CollectExpiredOperations(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-OperationRetention)
	for {
		var n int
		err := s.write(ctx, func(t *Service) error {
			var err error
			n, err = t.DAL.Operations().CollectExpired(ctx, cutoff, operationPurgePage)
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
	kinds := s.operationKinds()
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
