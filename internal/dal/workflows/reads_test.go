package workflows_test

import (
	"context"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"

	"papergo/internal/dal"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/waits"
	"papergo/internal/dal/workflows"
	"papergo/internal/jsonutil"
)

func TestWorkflowExecutionReads_SQLite(t *testing.T) {
	testWorkflowExecutionReads(t, daltest.SQLiteStore(t))
}
func TestWorkflowExecutionReads_Postgres(t *testing.T) {
	testWorkflowExecutionReads(t, daltest.PostgresStore(t))
}

func testWorkflowExecutionReads(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	definition := []byte(`{"flow":{"start":"done","nodes":{"done":{"activity":"end"}}}}`)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "source", Name: "Source", CreatedBy: "alice", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.Workflows().Find(ctx, wf.ID)
	if err != nil || found.Key != "source" {
		t.Fatalf("workflow metadata: %+v %v", found, err)
	}
	version, err := s.Workflows().Version(ctx, wf.ID, 1)
	if err != nil || version.CreatedBy != "alice" || !jsonutil.Equal(version.Definition, definition) {
		t.Fatalf("immutable version: %+v %v", version, err)
	}
	run, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "source-run", WorkflowID: wf.ID, WorkspaceID: f.Workspace, ItemID: f.Item, EventID: "event", EventType: "manual", Actor: "coordinator", WorkflowVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	wait, err := s.Waits().Put(ctx, waits.PutInput{Kind: "ai.request", Key: "one", WorkspaceID: f.Workspace, RunID: run.ID, Node: "request", ExecutionID: "execution"})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := s.Blobs().AddBlob(ctx, blobs.Blob{ID: "source-blob", ItemID: f.Item, ObjectKey: "source-object", Filename: "source.pdf", ContentType: blobs.MediaPDF, Languages: "", Sha256: strings.Repeat("a", 64), Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Blobs().Lease(ctx, wait.ID, run.ID, []string{blob.ID}); err != nil {
		t.Fatal(err)
	}
	authority, err := s.Waits().Authority(ctx, wait.ID)
	if err != nil || authority.Author != "alice" || authority.WorkspaceID != f.Workspace || authority.ItemID != f.Item || len(authority.Sources) != 1 || authority.Sources[0].BlobID != blob.ID || authority.Sources[0].ItemID != f.Item {
		t.Fatalf("origin authority not preserved: %+v %v", authority, err)
	}
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.WithTx(tx).Blobs().Release(ctx, wait.ID); err != nil {
		t.Fatal(err)
	}
	released, err := s.WithTx(tx).Waits().Authority(ctx, wait.ID)
	if err != nil || len(released.Sources) != 0 {
		t.Fatalf("transaction did not see lease release: %+v %v", released, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Waits().Authority(ctx, wait.ID)
	if err != nil || len(restored.Sources) != 1 {
		t.Fatalf("lease release escaped rollback: %+v %v", restored, err)
	}
}
