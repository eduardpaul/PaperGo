package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"testing"
	"time"
)

func checkpointHandler() OperationHandler {
	return OperationHandler{Scope: "list.write", Validate: func(ctx context.Context, s *Service, subject, container string, payload json.RawMessage) error {
		return nil
	}, Step: func(ctx context.Context, s *Service, op *operations.Operation) error {
		op.Processed++
		if op.Processed == 2 {
			op.Status, op.Result = operations.StatusSucceeded, json.RawMessage(`{"answer":42}`)
		} else {
			op.Progress = 50
		}
		return s.SaveOperation(ctx, op)
	}}
}

func TestRegisteredOperationsResumeAndExpire(t *testing.T) {
	s, _, list := fixture(t)
	if err := s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || err != nil {
		t.Fatal(worked, err)
	}
	s = NewService(s.db)
	if err = s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	op, err = s.Operation(testContext, "alice", op.ID)
	if err != nil || op.Status != operations.StatusSucceeded || op.Processed != 2 || op.Progress != 100 || op.StartedAt == nil || op.CompletedAt == nil || !sameDocument(t, op.Result, []byte(`{"answer":42}`)) {
		t.Fatal(op, err)
	}
	if _, err = s.Operation(testContext, "bob", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); worked || err != nil {
		t.Fatal("terminal operation reran", worked, err)
	}
	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	daltest.Update(t, s.DAL, "operations", daltest.Row{"updated_at": old}, daltest.Row{"id": op.ID})
	pending, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	daltest.Update(t, s.DAL, "operations", daltest.Row{"updated_at": old}, daltest.Row{"id": pending.ID})
	if err = s.CollectExpiredOperations(testContext, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Operation(testContext, "alice", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired record", err)
	}
	if _, err = s.Operation(testContext, "alice", pending.ID); err != nil {
		t.Fatal("pending work expired", err)
	}
}

func TestOperationUnknownHandlerAndRevokedCredential(t *testing.T) {
	s, _, list := fixture(t)
	// A process claims only the kinds it runs.
	queued, _, err := s.DAL.Operations().Accept(testContext, operations.Operation{Kind: "unknown", ContainerID: list.ID, CreatedBy: "alice"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); worked || err != nil {
		t.Fatal(worked, err)
	}
	unknown, err := s.Operation(testContext, "alice", queued.ID)
	if err != nil || unknown.Status != operations.StatusPending || unknown.Waiting != WaitingQueued {
		t.Fatal(unknown, err)
	}
	if err = s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateAccessToken(testContext, "alice", identity.AccessTokenInput{Name: "Job", Scopes: []string{"list.read", "list.write", "workspace.read"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithAccessToken(testContext, identity.AccessTokenIdentity{Subject: "alice", ID: token.Token.ID, Scopes: token.Token.Scopes})
	op, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().RevokeAccessToken(testContext, "alice", token.Token.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || !errors.Is(err, ErrForbidden) {
		t.Fatal(worked, err)
	}
	op, err = s.Operation(testContext, "alice", op.ID)
	if err != nil || op.Status != operations.StatusFailed || op.ErrorCode != operations.ErrorForbidden || op.Processed != 0 || op.Attempts != 1 {
		t.Fatal(op, err)
	}
	if _, err = s.EnqueueOperation(testContext, "alice", list.ID, "unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unregistered operation queued")
	}
}

func TestOperationPanicRollsBackBatch(t *testing.T) {
	s, _, list := fixture(t)
	item := create(t, s, list.ID, "item", "Original", nil)
	handler := checkpointHandler()
	handler.Step = func(ctx context.Context, t *Service, op *operations.Operation) error {
		if err := t.DAL.Resources().Change(ctx, item.ID, item.Version, "alice", resources.Changes{Name: ptr("Partial")}); err != nil {
			return err
		}
		panic("failed handler")
	}
	if err := s.RegisterOperationHandler("panic", handler); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "panic", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || err == nil {
		t.Fatal(worked, err)
	}
	op, err = s.Operation(testContext, "alice", op.ID)
	if err != nil || op.Status != operations.StatusFailed || op.StartedAt == nil {
		t.Fatal(op, err)
	}
	row, err := s.DAL.Resources().Resource(testContext, item.ID)
	if err != nil || row.Name != "Original" || row.Version != item.Version {
		t.Fatal(row, err)
	}
}
