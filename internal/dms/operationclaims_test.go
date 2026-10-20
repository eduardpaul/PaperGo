package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/operations"
	"strings"
	"testing"
	"time"
)

func TestOperationIdempotencyKeys(t *testing.T) {
	s, _, list := fixture(t)
	if err := s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithIdempotencyKey(testContext, "retry-1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{"a":1,"b":2}`))
	if err != nil || first.Replayed {
		t.Fatal(first, err)
	}
	// The same request replays, whatever its key order; another payload
	// conflicts.
	again, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{"b":2,"a":1}`))
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatal(again, err)
	}
	if _, err = s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{"a":2}`)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err = WithIdempotencyKey(testContext, "has space"); err == nil {
		t.Fatal("key with a space accepted")
	}
}

func TestOperationQuotas(t *testing.T) {
	s, _, list := fixture(t)
	if err := s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	s.SetOperationQuotas(OperationQuotas{PerCreator: 2})
	for range 2 {
		if _, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); !errors.Is(err, ErrOperationQuota) {
		t.Fatal(err)
	}
	// Finished work no longer counts.
	if err := s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestOperationCancellation(t *testing.T) {
	s, _, list := fixture(t)
	if err := s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	var finished []string
	s.OnOperationsFinished(func(ids []string) { finished = append(finished, ids...) })
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelOperation(testContext, "bob", op.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("another person cancelled", err)
	}
	cancelled, err := s.CancelOperation(testContext, "alice", op.ID)
	if err != nil || cancelled.Status != operations.StatusCancelled || cancelled.CompletedAt == nil {
		t.Fatal(cancelled, err)
	}
	if len(finished) != 1 || finished[0] != op.ID {
		t.Fatal("finished hook", finished)
	}
	if _, err = s.CancelOperation(testContext, "alice", op.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled twice", err)
	}
	if worked, err := s.operationStep(testContext); worked || err != nil {
		t.Fatal("cancelled operation ran", worked, err)
	}
	// PaperGo's own work completes what scheduled it.
	rebuild, _, err := s.DAL.Operations().Accept(testContext, operations.Operation{Kind: OperationKindSearchReindex, CreatedBy: systemSubject}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelOperation(testContext, "alice", rebuild.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("system operation cancelled", err)
	}
}

func TestOperationExecutionsAreFenced(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	handler.Step = nil
	attempts := 0
	handler.Execute = func(ctx context.Context, t *Service, op *operations.Operation) error {
		attempts++
		if attempts == 1 {
			return Retry(errors.New("provider busy"), time.Hour)
		}
		// The caller cancels while the execution works; its result is
		// discarded.
		if _, err := t.CancelOperation(ctx, "alice", op.ID); err != nil {
			return err
		}
		return t.Write(ctx, func(t *Service) error {
			op.Status = operations.StatusSucceeded
			return t.SaveOperation(ctx, op)
		})
	}
	if err := s.RegisterOperationHandler("external", handler); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "external", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || err != nil {
		t.Fatal(worked, err)
	}
	deferred, err := s.Operation(testContext, "alice", op.ID)
	if err != nil || deferred.Status != operations.StatusPending || deferred.Waiting != WaitingRetry || deferred.NextAttemptAt == nil || deferred.Error != "provider busy" {
		t.Fatal(deferred, err)
	}
	if worked, err := s.operationStep(testContext); worked || err != nil {
		t.Fatal("deferred operation ran early", worked, err)
	}
	if _, err = s.db.ExecContext(testContext, `UPDATE operations SET attempt_at = $1 WHERE id = $2`, time.Now().UTC().Add(-time.Second), op.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || err != nil {
		t.Fatal(worked, err)
	}
	ended, err := s.Operation(testContext, "alice", op.ID)
	if err != nil || ended.Status != operations.StatusCancelled || ended.Attempts != 2 {
		t.Fatal(ended, err)
	}
}

func TestOperationDeadlines(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	handler.Deadline = time.Hour
	var cleaned []string
	handler.Failed = func(ctx context.Context, t *Service, op *operations.Operation, cause error) error {
		cleaned = append(cleaned, op.ID)
		return nil
	}
	if err := s.RegisterOperationHandler("deadline", handler); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "deadline", json.RawMessage(`{}`))
	if err != nil || op.Deadline == nil || op.Deadline.Before(time.Now().Add(59*time.Minute)) {
		t.Fatal(op, err)
	}
	if err = s.ExpireOperations(testContext, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireOperations(testContext, op.Deadline.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	expired, err := s.Operation(testContext, "alice", op.ID)
	if err != nil || expired.Status != operations.StatusFailed || expired.ErrorCode != operations.ErrorDeadlineExceeded {
		t.Fatal(expired, err)
	}
	if len(cleaned) != 1 || cleaned[0] != op.ID {
		t.Fatal("failure cleanup", cleaned)
	}
}

func TestOperationLeasesKeepAcceptedBytes(t *testing.T) {
	s, w, _ := fixture(t)
	if err := s.RegisterOperationHandler("checkpoint", checkpointHandler()); err != nil {
		t.Fatal(err)
	}
	list := create(t, s, w.ID, "library", "Files", nil)
	item := create(t, s, list.ID, "item", "Scan", nil)
	old, err := s.DAL.Blobs().AddBlob(testContext, blobs.Blob{ItemID: item.ID, ObjectKey: "old", Filename: "scan.pdf", ContentType: blobs.MediaPDF, Sha256: strings.Repeat("a", 64), Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	var op *OperationView
	err = s.Write(testContext, func(t *Service) error {
		var e error
		if op, e = t.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); e != nil {
			return e
		}
		return t.LeaseOperationBlobs(testContext, op.ID, []string{old.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	protected, reason, err := s.blobProtected(testContext, &old)
	if err != nil || !protected || reason != "in use" {
		t.Fatal("accepted bytes reclaimable", protected, reason, err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	if protected, _, err = s.blobProtected(testContext, &old); err != nil || protected {
		t.Fatal("lease outlived its operation", protected, err)
	}
}

func TestInlineStepsOwnTheirOperationForTheWholeStep(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	step := handler.Step
	var until time.Time
	handler.Step = func(ctx context.Context, t *Service, op *operations.Operation) error {
		stored, err := t.DAL.Operations().Find(ctx, op.ID)
		if err != nil {
			return err
		}
		until = stored.Claim.Until
		return step(ctx, t, op)
	}
	if err := s.RegisterOperationHandler("checkpoint", handler); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.operationStep(testContext); !worked || err != nil {
		t.Fatal(worked, err)
	}
	// A long batch does not lose its claim halfway through.
	if until.Before(time.Now().Add(time.Hour)) {
		t.Fatal("step claim expires during the step", until)
	}
}

func TestReplaysSkipRevalidation(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	valid := true
	handler.Validate = func(context.Context, *Service, string, string, json.RawMessage) error {
		if !valid {
			return invalid("no longer valid")
		}
		return nil
	}
	if err := s.RegisterOperationHandler("checkpoint", handler); err != nil {
		t.Fatal(err)
	}
	ctx, _ := WithIdempotencyKey(testContext, "once")
	first, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// A lost response is retried after the state it was validated against
	// changed; the retry still answers with what was accepted.
	valid = false
	again, err := s.EnqueueOperation(ctx, "alice", list.ID, "checkpoint", json.RawMessage(`{}`))
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatal(again, err)
	}
	if _, err = s.EnqueueOperation(testContext, "alice", list.ID, "checkpoint", json.RawMessage(`{}`)); err == nil {
		t.Fatal("new request skipped validation")
	}
}

func TestOperationRetriesRunOut(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	handler.Step, handler.MaxAttempts = nil, 2
	runs := 0
	handler.Execute = func(context.Context, *Service, *operations.Operation) error {
		runs++
		return Retry(errors.New("provider busy"), time.Millisecond)
	}
	failed := false
	handler.Failed = func(context.Context, *Service, *operations.Operation, error) error { failed = true; return nil }
	if err := s.RegisterOperationHandler("flaky", handler); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "flaky", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		time.Sleep(5 * time.Millisecond)
		if worked, err := s.operationStep(testContext); !worked || err != nil {
			t.Fatal(worked, err)
		}
	}
	ended, err := s.Operation(testContext, "alice", op.ID)
	if err != nil || ended.Status != operations.StatusFailed || ended.ErrorCode != operations.ErrorRetriesExhausted || runs != 2 || !failed {
		t.Fatal(ended, err, runs, failed)
	}
}

func TestCancellationStopsARunningExecution(t *testing.T) {
	s, _, list := fixture(t)
	handler := checkpointHandler()
	handler.Step = nil
	started, stopped := make(chan struct{}), make(chan error, 1)
	handler.Execute = func(ctx context.Context, _ *Service, _ *operations.Operation) error {
		close(started)
		select {
		case <-ctx.Done():
			stopped <- context.Cause(ctx)
		case <-time.After(10 * time.Second):
			stopped <- errors.New("still running")
		}
		return ctx.Err()
	}
	if err := s.RegisterOperationHandler("long", handler); err != nil {
		t.Fatal(err)
	}
	op, err := s.EnqueueOperation(testContext, "alice", list.ID, "long", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.operationStep(testContext); done <- err }()
	<-started
	if _, err = s.CancelOperation(testContext, "alice", op.ID); err != nil {
		t.Fatal(err)
	}
	if cause := <-stopped; !errors.Is(cause, operations.ErrClaimLost) {
		t.Fatal("execution kept running after its operation was cancelled", cause)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if ended, _ := s.Operation(testContext, "alice", op.ID); ended.Status != operations.StatusCancelled {
		t.Fatal(ended)
	}
}
