package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"regexp"
	"time"
)

const OperationKindFieldIndex = "field_index"
const OperationKindBulkUpdate = "bulk_update"
const OperationRetention = 30 * 24 * time.Hour

// A step performs one bounded, transactional batch and persists its resume
// point or terminal status. Register handlers before serving requests/workers.
type OperationHandler struct {
	Scope    string
	Validate func(context.Context, *Service, string, string, json.RawMessage) error
	Step     func(context.Context, *Service, *operations.Operation) error
	// ExternalStep runs outside the database writer, and owns its checkpoint
	// transactions. Network integrations must never fetch in a write transaction.
	ExternalStep func(context.Context, *Service, *operations.Operation) error
	Failed       func(context.Context, *Service, *operations.Operation, error) error
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
		}},
	}
}

var operationKind = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func (s *Service) RegisterOperationHandler(kind string, handler OperationHandler) error {
	if !operationKind.MatchString(kind) || (handler.Step == nil && handler.ExternalStep == nil) || handler.Validate == nil {
		return invalid("operation handlers require a kind, validator and step")
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
	if _, exists := s.operationHandlers[kind]; exists {
		return ErrConflict
	}
	s.operationHandlers[kind] = handler
	return nil
}

// EnqueueOperation is used by registered feature endpoints. Its validation and
// permissions commit with the durable payload; payloads are never returned.
func (s *Service) EnqueueOperation(ctx context.Context, subject, containerID, kind string, payload json.RawMessage) (out *OperationView, err error) {
	handler, exists := s.operationHandlers[kind]
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
		if err := handler.Validate(ctx, t, subject, containerID, payload); err != nil {
			return err
		}
		op, e := t.enqueue(ctx, operations.Operation{ContainerID: containerID, Kind: kind, CreatedBy: subject, Payload: payload})
		if e != nil {
			return e
		}
		out = viewOperation(op)
		return t.auditCatalog(ctx, subject, "operation.create", map[string]any{"operation_id": out.ID})
	})
	if err == nil {
		select {
		case s.operations <- struct{}{}:
		default:
		}
	}
	return
}

// enqueue records a pending operation in this trace, limited to the caller's
// access token when one authenticated the request.
func (s *Service) enqueue(ctx context.Context, op operations.Operation) (operations.Operation, error) {
	op.TraceID = s.traceID
	if token, limited := identity.AccessTokenContext(ctx); limited {
		op.CredentialID, op.CredentialScopes = token.ID, token.Scopes
	}
	return s.DAL.Operations().Enqueue(ctx, op)
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

func (s *Service) failOperation(ctx context.Context, op *operations.Operation, cause error) error {
	return s.write(ctx, func(t *Service) error {
		current, err := t.DAL.Operations().Find(ctx, op.ID)
		if err != nil {
			return err
		}
		if current.Status != operations.StatusPending {
			return nil
		}
		completed := time.Now().UTC()
		current.Status, current.CompletedAt, current.Error = operations.StatusFailed, &completed, cause.Error()
		if op.StartedAt != nil {
			current.StartedAt = op.StartedAt
		}
		if err := t.DAL.Operations().Save(ctx, &current); err != nil {
			return err
		}
		if handler, exists := t.operationHandlers[op.Kind]; exists && handler.Failed != nil {
			return handler.Failed(ctx, t, &current, cause)
		}
		return nil
	})
}

var errUnknownOperation = errors.New("no handler registered for operation kind")

func executeOperation(ctx context.Context, s *Service, handler OperationHandler, op *operations.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return handler.Step(ctx, s, op)
}

func executeExternalOperation(ctx context.Context, s *Service, step func(context.Context, *Service, *operations.Operation) error, op *operations.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return step(ctx, s, op)
}

func operationCheckpoint(op *operations.Operation) string {
	raw, _ := json.Marshal([]any{op.Surface, op.AfterItemID, op.Processed, op.Prepared, op.Matched, op.Updated, op.Failed, op.Progress, op.Failures, op.Error, op.Result})
	return string(raw)
}
