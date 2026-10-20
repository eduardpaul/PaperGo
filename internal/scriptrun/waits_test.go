package scriptrun

import (
	"context"
	"testing"
	"time"

	"papergo/internal/dal/operations"
	"papergo/internal/dal/runs"
	"papergo/internal/dms"
)

// A wait whose operation ended without its release, as when the process
// stopped between the commit and the hook, is completed by reconciling.
func TestReconcileReleasesMissedWaits(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	list, err := f.s.Create(ctx, "alice", f.workspaceID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	run := f.run(t, `export default {}`, `{}`)
	op, _, err := f.s.DAL.Operations().Accept(ctx, operations.Operation{Kind: dms.OperationKindBulkUpdate, ContainerID: list.ID, CreatedBy: "alice"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wait := runs.Step{RunID: run.ID, Seq: 0, Kind: runs.StepWait, Name: "wait", InputHash: "0000000000000000000000000000000000000000000000000000000000000000", Status: runs.StepPending, Operations: []string{op.ID}}
	if err = f.s.DAL.Runs().RecordSteps(ctx, []runs.Step{wait}); err != nil {
		t.Fatal(err)
	}
	if n, err := ReconcileWaits(ctx, f.s); err != nil || n != 0 {
		t.Fatalf("released a wait for pending work: %d %v", n, err)
	}
	// Ending the operation outside the service runs no hook.
	if ok, err := f.s.DAL.Operations().Cancel(ctx, op.ID, time.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n, err := ReconcileWaits(ctx, f.s); err != nil || n != 1 {
		t.Fatalf("reconciled %d waits: %v", n, err)
	}
	steps, err := f.s.DAL.Runs().Steps(ctx, run.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != runs.StepCompleted {
		t.Fatalf("steps: %+v %v", steps, err)
	}
}
