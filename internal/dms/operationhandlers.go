package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/operation"
	"papergo/ent/operationitem"
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
	Step     func(context.Context, *Service, *ent.Operation) error
	// ExternalStep runs outside the database writer, and owns its checkpoint
	// transactions. Network integrations must never fetch in a write transaction.
	ExternalStep func(context.Context, *Service, *ent.Operation) error
	Failed       func(context.Context, *Service, *ent.Operation, error) error
}

func builtInOperationHandlers() map[string]OperationHandler {
	return map[string]OperationHandler{
		OperationKindIntegrationRefresh: integrationOperationHandler(),
		OperationKindFieldIndex: {Step: func(ctx context.Context, s *Service, op *ent.Operation) error { return s.fieldIndexBatch(ctx, op) }, Failed: func(ctx context.Context, s *Service, op *ent.Operation, cause error) error {
			err := s.Client.FieldDefinition.UpdateOneID(op.FieldID).SetIndexStatus(fielddefinition.IndexStatusFailed).Exec(ctx)
			if ent.IsNotFound(err) {
				return nil
			}
			return err
		}},
		OperationKindSearchReindex: searchReindexHandler(),
		OperationKindBulkUpdate: {Scope: "list.write", Step: func(ctx context.Context, s *Service, op *ent.Operation) error { return s.bulkUpdateStep(ctx, op) }, Failed: func(ctx context.Context, s *Service, op *ent.Operation, cause error) error {
			_, err := s.Client.OperationItem.Delete().Where(operationitem.OperationIDEQ(op.ID)).Exec(ctx)
			return err
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
func (s *Service) EnqueueOperation(ctx context.Context, subject, containerID, kind string, payload json.RawMessage) (out *ent.Operation, err error) {
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
		b := t.Client.Operation.Create().SetKind(kind).SetCreatedBy(subject).SetPayload(payload).SetTraceID(t.traceID)
		if containerID != "" {
			b.SetContainerID(containerID)
		}
		b = operationCredentials(ctx, b)
		var e error
		out, e = b.Save(ctx)
		if e != nil {
			return e
		}
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

func operationCredentials(ctx context.Context, b *ent.OperationCreate) *ent.OperationCreate {
	if token, limited := identity.RESTTokenContext(ctx); limited {
		b.SetCredentialID(token.ID).SetCredentialScopes(token.Scopes)
	}
	return b
}
func (s *Service) operationPermission(ctx context.Context, subject, containerID, scope string) error {
	if err := identity.RequireActive(ctx, s.tx, subject); err != nil {
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
func (s *Service) operationContext(ctx context.Context, op *ent.Operation) (context.Context, error) {
	ctx = requestmeta.WithTrace(ctx, op.TraceID)
	if op.CredentialID != "" {
		ctx = identity.WithRESTToken(ctx, identity.RESTTokenIdentity{Subject: op.CreatedBy, ID: op.CredentialID, Scopes: op.CredentialScopes})
		if err := identity.RequireCredential(ctx, s.tx, op.CreatedBy); err != nil {
			return ctx, ErrForbidden
		}
	}
	return ctx, nil
}

// Terminal payloads/results expire together. Pending work is retained for resume.
func (s *Service) CollectExpiredOperations(ctx context.Context, now time.Time) error {
	return s.write(ctx, func(t *Service) error {
		rows, err := t.Client.Operation.Query().Where(operation.StatusNEQ(operation.StatusPending), operation.UpdatedAtLTE(now.UTC().Add(-OperationRetention))).Limit(1000).All(ctx)
		if err != nil {
			return err
		}
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		if len(ids) == 0 {
			return nil
		}
		if _, err = t.Client.OperationItem.Delete().Where(operationitem.OperationIDIn(ids...)).Exec(ctx); err != nil {
			return err
		}
		_, err = t.Client.Operation.Delete().Where(operation.IDIn(ids...)).Exec(ctx)
		return err
	})
}

func (s *Service) failOperation(ctx context.Context, op *ent.Operation, cause error) error {
	return s.write(ctx, func(t *Service) error {
		current, err := t.Client.Operation.Get(ctx, op.ID)
		if err != nil {
			return err
		}
		if current.Status != operation.StatusPending {
			return nil
		}
		b := t.Client.Operation.UpdateOne(current).SetStatus(operation.StatusFailed).SetCompletedAt(time.Now().UTC()).SetError(cause.Error())
		if op.StartedAt != nil {
			b.SetStartedAt(*op.StartedAt)
		}
		if err := b.Exec(ctx); err != nil {
			return err
		}
		if handler, exists := t.operationHandlers[op.Kind]; exists && handler.Failed != nil {
			current, err = t.Client.Operation.Get(ctx, op.ID)
			if err != nil {
				return err
			}
			return handler.Failed(ctx, t, current, cause)
		}
		return nil
	})
}

var errUnknownOperation = errors.New("no handler registered for operation kind")

func executeOperation(ctx context.Context, s *Service, handler OperationHandler, op *ent.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return handler.Step(ctx, s, op)
}

func executeExternalOperation(ctx context.Context, s *Service, step func(context.Context, *Service, *ent.Operation) error, op *ent.Operation) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("operation handler panicked")
		}
	}()
	return step(ctx, s, op)
}

func operationCheckpoint(op *ent.Operation) string {
	raw, _ := json.Marshal([]any{op.Surface, op.AfterItemID, op.Processed, op.Prepared, op.Matched, op.Updated, op.Failed, op.Progress, op.Failures, op.Error, op.Result})
	return string(raw)
}
