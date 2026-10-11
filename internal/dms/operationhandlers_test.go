package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/operation"
	"papergo/internal/identity"
	"testing"
	"time"
)

func checkpointHandler() OperationHandler {
	return OperationHandler{Scope: "list.write", Validate: func(ctx context.Context, s *Service, subject, container string, payload json.RawMessage) error {
		return nil
	}, Step: func(ctx context.Context, s *Service, op *ent.Operation) error {
		b := s.Client.Operation.UpdateOne(op).AddProcessed(1)
		if op.Processed == 1 {
			b.SetStatus(operation.StatusSucceeded).SetResult(json.RawMessage(`{"answer":42}`))
		} else {
			b.SetProgress(50)
		}
		return b.Exec(ctx)
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
	if err != nil || op.Status != operation.StatusSucceeded || op.Processed != 2 || op.Progress != 100 || op.StartedAt == nil || op.CompletedAt == nil || string(op.Result) != `{"answer":42}` {
		t.Fatal(op, err)
	}
	if _, err = s.Operation(testContext, "bob", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); worked || err != nil {
		t.Fatal("terminal operation reran", worked, err)
	}
	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if err = s.Client.Operation.UpdateOne(op).SetUpdatedAt(old).Exec(testContext); err != nil {
		t.Fatal(err)
	}
	pending, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Client.Operation.UpdateOne(pending).SetUpdatedAt(old).Exec(testContext); err != nil {
		t.Fatal(err)
	}
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
	unknown, err := s.Client.Operation.Create().SetKind("unknown").SetContainerID(list.ID).SetCreatedBy("alice").Save(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || !errors.Is(err, errUnknownOperation) {
		t.Fatal(worked, err)
	}
	unknown, err = s.Operation(testContext, "alice", unknown.ID)
	if err != nil || unknown.Status != operation.StatusFailed || unknown.CompletedAt == nil {
		t.Fatal(unknown, err)
	}
	if err = s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().CreateRESTToken(testContext, "alice", identity.RESTTokenInput{Name: "Job", Scopes: []string{"list.read", "list.write", "workspace.read"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithRESTToken(testContext, identity.RESTTokenIdentity{Subject: "alice", ID: token.Token.ID, Scopes: token.Token.Scopes})
	op, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().RevokeRESTToken(testContext, "alice", token.Token.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || !errors.Is(err, ErrForbidden) {
		t.Fatal(worked, err)
	}
	op, err = s.Operation(testContext, "alice", op.ID)
	if err != nil || op.Status != operation.StatusFailed || op.Processed != 0 {
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
	handler.Step = func(ctx context.Context, t *Service, op *ent.Operation) error {
		if err := t.Client.Resource.UpdateOneID(item.ID).SetName("Partial").AddVersion(1).Exec(ctx); err != nil {
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
	if err != nil || op.Status != operation.StatusFailed || op.StartedAt == nil {
		t.Fatal(op, err)
	}
	row, err := s.Client.Resource.Get(testContext, item.ID)
	if err != nil || row.Name != "Original" || row.Version != item.Version {
		t.Fatal(row, err)
	}
}
