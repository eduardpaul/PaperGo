package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/runs"
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
			Status: runs.StepStatus(r.Status), Input: r.Input.GetOrZero().Val, Output: r.Output.GetOrZero(),
			Error: r.Error.GetOrZero(), StartedAt: r.StartedAt.UTC(), CompletedAt: nullable.UTC(r.CompletedAt),
		}
	}
	return out, nil
}

func (q Queries) InsertRunStep(ctx context.Context, s runs.Step) error {
	_, err := models.WorkflowSteps.Insert(&models.WorkflowStepSetter{
		RunID: omit.From(s.RunID), Seq: omit.From(int64(s.Seq)), Kind: omit.From(string(s.Kind)), Name: omit.From(s.Name),
		InputHash: omit.From(s.InputHash), Status: omit.From(string(s.Status)), Input: nullable.JSON(s.Input),
		Output: stepOutput(s.Output), Error: nullable.String(s.Error), StartedAt: omit.From(s.StartedAt), CompletedAt: nullable.Time(s.CompletedAt),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) FinishRunStep(ctx context.Context, runID string, seq int, status runs.StepStatus, output json.RawMessage, message string, at time.Time) (bool, error) {
	set := &models.WorkflowStepSetter{Status: omit.From(string(status)), Output: stepOutput(output), Error: nullable.String(message), CompletedAt: omitnull.From(at)}
	w := models.UpdateWhere.WorkflowSteps
	n, err := models.WorkflowSteps.Update(set.UpdateMod(), w.RunID.EQ(runID), w.Seq.EQ(int64(seq)), w.Status.EQ(string(runs.StepPending))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func stepOutput(output json.RawMessage) omitnull.Val[[]byte] {
	if len(output) == 0 {
		return omitnull.FromPtr[[]byte](nil)
	}
	return omitnull.From([]byte(output))
}
