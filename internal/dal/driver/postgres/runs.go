package postgres

import (
	"context"
	"encoding/json"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/runs"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
)

func (q Queries) VersionExists(ctx context.Context, workflowID string, number int64) (bool, error) {
	w := models.SelectWhere.WorkflowVersions
	ok, err := models.WorkflowVersions.Query(w.WorkflowID.EQ(workflowID), w.Number.EQ(number)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) WorkflowRun(ctx context.Context, id string) (runs.Run, error) {
	row, err := models.FindWorkflowRun(ctx, q.exec, id)
	if err != nil {
		return runs.Run{}, dberr.Translate(err)
	}
	return runRecord(row), nil
}

func (q Queries) WorkflowRunExists(ctx context.Context, workflowID, eventID string) (bool, error) {
	w := models.SelectWhere.WorkflowRuns
	ok, err := models.WorkflowRuns.Query(w.WorkflowID.EQ(workflowID), w.EventID.EQ(eventID)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) WorkflowRuns(ctx context.Context, f runs.Filter, statuses []operations.Status) ([]runs.Run, error) {
	query := models.WorkflowRuns.Query()
	w := models.SelectWhere.WorkflowRuns
	c := models.WorkflowRuns.Columns
	if len(f.IDs) > 0 {
		query.Apply(w.ID.In(f.IDs...))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.WorkflowID != "" {
		query.Apply(w.WorkflowID.EQ(f.WorkflowID))
	}
	if f.AfterID != "" {
		query.Apply(w.ID.GT(f.AfterID))
	}
	if len(statuses) > 0 {
		values := make([]any, len(statuses))
		for i, status := range statuses {
			values[i] = string(status)
		}
		oc := models.Operations.Columns
		matching := models.Operations.Query(sm.Columns(oc.ID), sm.Where(oc.Status.In(psql.Arg(values...))))
		query.Apply(sm.Where(psql.Raw(`"workflow_runs"."id" IN ?`, matching)))
	}
	if len(f.ExcludeEventTypes) > 0 {
		query.Apply(w.EventType.NotIn(f.ExcludeEventTypes...))
	}
	switch f.Origin {
	case "manual", "schedule", "test", "call":
		query.Apply(w.EventType.EQ(f.Origin))
	case "automatic":
		query.Apply(w.EventType.NotIn("manual", "schedule", "test", "call"))
	}
	if f.CreatedBefore != nil {
		query.Apply(w.CreatedAt.LT(f.CreatedBefore.UTC()))
	}
	if f.BeforeCreatedAt != nil {
		query.Apply(sm.Where(psql.Group(c.CreatedAt, c.ID).LT(psql.ArgGroup(f.BeforeCreatedAt.UTC(), f.BeforeID))))
	}
	if len(f.ItemIDs) > 0 {
		items := make([]any, len(f.ItemIDs))
		for i, id := range f.ItemIDs {
			items[i] = id
		}
		ic := models.WorkflowRunItems.Columns
		members := models.WorkflowRunItems.Query(sm.Columns(ic.RunID), sm.Where(ic.ItemID.In(psql.Arg(items...))))
		query.Apply(sm.Where(psql.Or(c.ItemID.In(psql.Arg(items...)), psql.Raw(`"workflow_runs"."id" IN ?`, members))))
	}
	if f.HideSystem || f.SystemOnly {
		if len(f.SystemKeys) > 0 {
			wc := models.Workflows.Columns
			keys := make([]any, len(f.SystemKeys))
			for i, k := range f.SystemKeys {
				keys[i] = k
			}
			system := models.Workflows.Query(sm.Columns(wc.ID), sm.Where(wc.BuiltinKey.In(psql.Arg(keys...))))
			if f.WorkspaceID != "" {
				system.Apply(models.SelectWhere.Workflows.WorkspaceID.EQ(f.WorkspaceID))
			}
			if f.SystemOnly {
				query.Apply(sm.Where(psql.Raw(`"workflow_runs"."workflow_id" IN ?`, system)), w.EventType.NE("manual"))
			} else {
				query.Apply(sm.Where(psql.Or(psql.Raw(`"workflow_runs"."workflow_id" NOT IN ?`, system), c.EventType.EQ(psql.Arg("manual")))))
			}
		}
	}
	if f.Order == "id" {
		query.Apply(sm.OrderBy(c.ID))
	} else {
		query.Apply(sm.OrderBy(c.CreatedAt).Desc(), sm.OrderBy(c.ID).Desc())
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]runs.Run, len(rows))
	for i, row := range rows {
		out[i] = runRecord(row)
	}
	return out, nil
}

func (q Queries) InsertWorkflowRun(ctx context.Context, r runs.Run) error {
	_, err := models.WorkflowRuns.Insert(&models.WorkflowRunSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), WorkflowID: omit.From(r.WorkflowID), WorkflowVersion: omit.From(int64(r.WorkflowVersion)),
		WorkspaceID: omit.From(r.WorkspaceID), ItemID: nullable.String(r.ItemID), EventID: omit.From(r.EventID), EventType: omit.From(r.EventType),
		Depth: omit.From(int64(r.Depth)), Actor: omit.From(r.Actor), RetryOf: nullable.String(r.RetryOf),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) RunItems(ctx context.Context, runIDs []string, itemID string) ([]runs.Item, error) {
	c := models.WorkflowRunItems.Columns
	w := models.SelectWhere.WorkflowRunItems
	query := models.WorkflowRunItems.Query(sm.OrderBy(c.RunID), sm.OrderBy(c.Position))
	if len(runIDs) > 0 {
		query.Apply(w.RunID.In(runIDs...))
	}
	if itemID != "" {
		query.Apply(w.ItemID.EQ(itemID))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]runs.Item, len(rows))
	for i, row := range rows {
		out[i] = runs.Item{ID: row.ID, RunID: row.RunID, ItemID: row.ItemID, Position: int(row.Position)}
	}
	return out, nil
}

func (q Queries) InsertRunItem(ctx context.Context, i runs.Item) error {
	_, err := models.WorkflowRunItems.Insert(&models.WorkflowRunItemSetter{
		ID: omit.From(i.ID), RunID: omit.From(i.RunID), ItemID: omit.From(i.ItemID), Position: omit.From(int64(i.Position)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func runRecord(r *models.WorkflowRun) runs.Run {
	return runs.Run{
		ID: r.ID, WorkflowID: r.WorkflowID, WorkspaceID: r.WorkspaceID, ItemID: r.ItemID.GetOrZero(), EventID: r.EventID, EventType: r.EventType,
		Actor: r.Actor, RetryOf: r.RetryOf.GetOrZero(), WorkflowVersion: int(r.WorkflowVersion), Depth: int(r.Depth),
		CreatedAt: r.CreatedAt.UTC(), Logs: runLogs(r.Logs.Val),
	}
}

func runLogs(raw json.RawMessage) []runs.Log {
	var out []runs.Log
	_ = json.Unmarshal(raw, &out)
	return out
}

func (q Queries) SetRunLogs(ctx context.Context, id string, logs []runs.Log) error {
	raw, err := json.Marshal(logs)
	if err != nil {
		return err
	}
	set := &models.WorkflowRunSetter{Logs: omit.From(types.NewJSON(json.RawMessage(raw)))}
	_, err = models.WorkflowRuns.Update(set.UpdateMod(), models.UpdateWhere.WorkflowRuns.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}
