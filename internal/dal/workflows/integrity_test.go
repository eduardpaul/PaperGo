package workflows_test

import (
	"context"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"testing"
	"time"
)

func TestWorkflowsOperationsAndLiveChangesPruning(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		ws, err := s.Resources().Create(ctx, resources.New{
			ID:          "ws-wf",
			WorkspaceID: "ws-wf",
			Kind:        refs.KindWorkspace,
			Name:        "Automation",
		})
		if err != nil {
			t.Fatalf("CreateResource ws: %v", err)
		}

		wf, err := s.Workflows().Create(ctx, workflows.CreateInput{
			ID:          "wf-1",
			WorkspaceID: ws.ID,
			Key:         "ocr-pipeline",
			Name:        "OCR Pipeline",
			CreatedBy:   "u1",
		})
		if err != nil {
			t.Fatalf("CreateWorkflow: %v", err)
		}

		run, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "run-1", WorkflowID: wf.ID, WorkspaceID: ws.ID, ItemID: "", EventID: "evt-1", EventType: "item.created", Actor: "u1", WorkflowVersion: 1})
		if err != nil {
			t.Fatalf("CreateWorkflowRun: %v", err)
		}
		if err := s.Runs().Observe(ctx, run.ID, runstore.StatusRunning, time.Now(), false); err != nil {
			t.Fatalf("observe run: %v", err)
		}

		op, err := s.Operations().Enqueue(ctx, operations.Operation{Kind: "reindex", TraceID: "trace-1", CreatedBy: "u1"})
		if err != nil {
			t.Fatalf("enqueue operation: %v", err)
		}
		op.Status, op.Progress, op.Processed = operations.StatusSucceeded, 100, 10
		if err := s.Operations().Save(ctx, &op); err != nil {
			t.Fatalf("save operation: %v", err)
		}

		if _, err := s.Audit().Append(ctx, audit.Event{WorkspaceID: ws.ID, Subject: "u1", Action: "permissions.replace", EntityType: "resource", EntityID: ws.ID, TraceID: "trace-1"}); err != nil {
			t.Fatalf("Append audit: %v", err)
		}

		pruned, err := s.LiveChanges().DeleteExpired(ctx, time.Now().UTC().Add(time.Minute), 100)
		if err != nil {
			t.Fatalf("delete expired live changes: %v", err)
		}
		if pruned < 5 {
			t.Fatalf("expected at least 5 live_changes rows to be pruned, got %d", pruned)
		}
	})
}
