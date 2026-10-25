package sqlite

import (
	"context"
	"time"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/runs"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/scan"
)

func (q Queries) RunSteps(ctx context.Context, runID string) ([]runs.Step, error) {
	rows, err := models.WorkflowSteps.Query(models.SelectWhere.WorkflowSteps.RunID.EQ(runID), sm.OrderBy(models.WorkflowSteps.Columns.Seq)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]runs.Step, len(rows))
	for i, r := range rows {
		out[i] = runs.Step{
			RunID: r.RunID, Seq: int(r.Seq), Kind: runs.StepKind(r.Kind), Name: r.Name, InputHash: r.InputHash,
			Status: runs.StepStatus(r.Status), Input: r.Input.GetOrZero().Val, Output: r.Output.GetOrZero(), OutputSize: int(r.OutputSize),
			Error: r.Error.GetOrZero(), StartedAt: r.StartedAt.UTC(), CompletedAt: nullable.UTC(r.CompletedAt),
		}
	}
	return out, nil
}

func (q Queries) InsertRunStep(ctx context.Context, s runs.Step) error {
	_, err := models.WorkflowSteps.Insert(&models.WorkflowStepSetter{
		RunID: omit.From(s.RunID), Seq: omit.From(int64(s.Seq)), Kind: omit.From(string(s.Kind)), Name: omit.From(s.Name),
		InputHash: omit.From(s.InputHash), Status: omit.From(string(s.Status)), Input: nullable.JSON(s.Input),
		Output: stepOutput(s.Output), OutputSize: omit.From(int64(s.OutputSize)), Error: nullable.String(s.Error), StartedAt: omit.From(s.StartedAt), CompletedAt: nullable.Time(s.CompletedAt),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) InsertStepOperations(ctx context.Context, runID string, seq int, operationIDs []string) error {
	rows := make([]*models.WorkflowStepOperationSetter, len(operationIDs))
	for i, id := range operationIDs {
		rows[i] = &models.WorkflowStepOperationSetter{RunID: omit.From(runID), Seq: omit.From(int64(seq)), OperationID: omit.From(id)}
	}
	_, err := models.WorkflowStepOperations.Insert(bob.ToMods(rows...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) FinishRunStep(ctx context.Context, runID string, seq int, status runs.StepStatus, output []byte, size int, message string, at time.Time) (bool, error) {
	set := &models.WorkflowStepSetter{Status: omit.From(string(status)), Output: stepOutput(output), OutputSize: omit.From(int64(size)), Error: nullable.String(message), CompletedAt: omitnull.From(at)}
	w := models.UpdateWhere.WorkflowSteps
	n, err := models.WorkflowSteps.Update(set.UpdateMod(), w.RunID.EQ(runID), w.Seq.EQ(int64(seq)), w.Status.EQ(string(runs.StepPending))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) ReleasableWaits(ctx context.Context, operationIDs []string) ([]runs.Wait, error) {
	args := []any{string(runs.StepPending), string(runs.StepWait)}
	waiting := ""
	if len(operationIDs) > 0 {
		waiting = `
  AND EXISTS (SELECT 1 FROM workflow_step_operations w WHERE w.run_id = s.run_id AND w.seq = s.seq AND w.operation_id IN (` + marks(len(operationIDs)) + `))`
		args = append(args, anys(operationIDs)...)
	}
	args = append(args, string(operations.StatusPending))
	type row struct {
		RunID       string `db:"run_id"`
		Seq         int64  `db:"seq"`
		OperationID string `db:"operation_id"`
	}
	rows, err := bob.All(ctx, q.exec, sqlite.RawQuery(`SELECT s.run_id, s.seq, l.operation_id FROM workflow_steps s
JOIN workflow_step_operations l ON l.run_id = s.run_id AND l.seq = s.seq
WHERE s.status = ? AND s.kind = ?`+waiting+`
  AND NOT EXISTS (SELECT 1 FROM workflow_step_operations p JOIN operations o ON o.id = p.operation_id
    WHERE p.run_id = s.run_id AND p.seq = s.seq AND o.status = ?)
ORDER BY s.run_id, s.seq, l.operation_id`, args...), scan.StructMapper[row]())
	if err != nil {
		return nil, dberr.Translate(err)
	}
	var out []runs.Wait
	for _, r := range rows {
		if n := len(out); n == 0 || out[n-1].RunID != r.RunID || out[n-1].Seq != int(r.Seq) {
			out = append(out, runs.Wait{RunID: r.RunID, Seq: int(r.Seq)})
		}
		out[len(out)-1].Operations = append(out[len(out)-1].Operations, r.OperationID)
	}
	return out, nil
}

func (q Queries) RunWaiting(ctx context.Context, runID string) (bool, error) {
	w := models.SelectWhere.WorkflowSteps
	waiting, err := models.WorkflowSteps.Query(w.RunID.EQ(runID), w.Status.EQ(string(runs.StepPending))).Exists(ctx, q.exec)
	return waiting, dberr.Translate(err)
}

func (q Queries) DropRunOutputs(ctx context.Context, runID string, limit int) error {
	_, err := bob.Exec(ctx, q.exec, sqlite.RawQuery(`UPDATE workflow_steps SET "output" = NULL WHERE run_id = ? AND length("output") > ?`, runID, limit))
	return dberr.Translate(err)
}

func (q Queries) CopyRunSteps(ctx context.Context, target, source string, before int) error {
	if _, err := bob.Exec(ctx, q.exec, sqlite.RawQuery(`INSERT INTO workflow_steps (run_id, seq, kind, name, input_hash, status, "input", "output", output_size, error, started_at, completed_at)
SELECT ?, seq, kind, name, input_hash, status, "input", "output", output_size, error, started_at, completed_at FROM workflow_steps WHERE run_id = ? AND seq < ?`, target, source, before)); err != nil {
		return dberr.Translate(err)
	}
	_, err := bob.Exec(ctx, q.exec, sqlite.RawQuery(`INSERT INTO workflow_step_operations (run_id, seq, operation_id)
SELECT ?, seq, operation_id FROM workflow_step_operations WHERE run_id = ? AND seq < ?`, target, source, before))
	return dberr.Translate(err)
}

func (q Queries) DeleteRunSteps(ctx context.Context, runID string) error {
	_, err := models.WorkflowSteps.Delete(models.DeleteWhere.WorkflowSteps.RunID.EQ(runID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func stepOutput(output []byte) omitnull.Val[[]byte] {
	if len(output) == 0 {
		return omitnull.FromPtr[[]byte](nil)
	}
	return omitnull.From([]byte(output))
}
