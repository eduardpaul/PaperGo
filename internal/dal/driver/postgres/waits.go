package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aarondl/opt/null"
	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/im"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/dialect/psql/um"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/waits"
)

func (q Queries) Wait(ctx context.Context, kind, key string) (waits.Wait, error) {
	w := models.SelectWhere.WorkflowWaits
	row, err := models.WorkflowWaits.Query(w.Kind.EQ(kind), w.Key.EQ(key)).One(ctx, q.exec)
	if err != nil {
		return waits.Wait{}, dberr.Translate(err)
	}
	return waitRecord(row), nil
}

// LockWait reads the wait under a row lock so replays observe the latest
// committed parking and status.
func (q Queries) LockWait(ctx context.Context, kind, key string) (waits.Wait, error) {
	w := models.SelectWhere.WorkflowWaits
	row, err := models.WorkflowWaits.Query(w.Kind.EQ(kind), w.Key.EQ(key), sm.ForUpdate()).One(ctx, q.exec)
	if err != nil {
		return waits.Wait{}, dberr.Translate(err)
	}
	return waitRecord(row), nil
}

func (q Queries) Waits(ctx context.Context, f waits.Filter) ([]waits.Wait, error) {
	query := models.WorkflowWaits.Query(sm.OrderBy(models.WorkflowWaits.Columns.ID))
	w := models.SelectWhere.WorkflowWaits
	if f.Kind != "" {
		query.Apply(w.Kind.EQ(f.Kind))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.RunID != "" {
		query.Apply(w.RunID.EQ(f.RunID))
	}
	if len(f.RunIDs) > 0 {
		query.Apply(w.RunID.In(f.RunIDs...))
	}
	if len(f.Statuses) > 0 {
		statuses := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		query.Apply(w.Status.In(statuses...))
	}
	if f.Queue != "" {
		query.Apply(w.Queue.EQ(f.Queue))
	}
	if f.After != "" {
		query.Apply(w.ID.GT(f.After))
	}
	if f.Parking {
		query.Apply(w.Parking.EQ(true))
	}
	if f.Unnotified {
		query.Apply(w.NotifiedAt.IsNull())
	}
	if f.DeadlineBefore != nil {
		query.Apply(w.Deadline.LT(f.DeadlineBefore.UTC()))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]waits.Wait, len(rows))
	for i, row := range rows {
		out[i] = waitRecord(row)
	}
	return out, nil
}

func (q Queries) InsertWait(ctx context.Context, w waits.Wait) error {
	_, err := models.WorkflowWaits.Insert(&models.WorkflowWaitSetter{
		ID: omit.From(w.ID), CreatedAt: omit.From(w.CreatedAt), UpdatedAt: omit.From(w.UpdatedAt), Kind: omit.From(w.Kind), Key: omit.From(w.Key),
		WorkspaceID: omit.From(w.WorkspaceID), RunID: omit.From(w.RunID), Node: omit.From(w.Node), ExecutionID: omit.From(w.ExecutionID),
		Data: omit.From(types.NewJSON(w.Data)), Queue: omit.From(w.Queue), PayloadID: omit.From(w.PayloadID), Parking: omit.From(w.Parking),
		Status: omit.From(string(w.Status)), Deadline: nullable.Time(w.Deadline),
	}, im.OnConflict("kind", "key").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetWaitParking(ctx context.Context, id string, at time.Time) error {
	set := &models.WorkflowWaitSetter{Parking: omit.From(true), UpdatedAt: omit.From(at)}
	_, err := models.WorkflowWaits.Update(set.UpdateMod(), models.UpdateWhere.WorkflowWaits.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) CompleteWait(ctx context.Context, kind, key string, completion json.RawMessage, at time.Time) (int64, error) {
	set := unclaimed(at)
	set.Status, set.Completion = omit.From(string(waits.StatusCompleted)), omitnull.From(types.NewJSON(completion))
	w := models.UpdateWhere.WorkflowWaits
	n, err := models.WorkflowWaits.Update(set.UpdateMod(), w.Kind.EQ(kind), w.Key.EQ(key), w.Status.EQ(string(waits.StatusOpen))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

// claimable matches waits c may take.
func claimable(c waits.Claim) dialect.Expression {
	cols := models.WorkflowWaits.Columns
	return psql.Or(cols.ClaimRunID.IsNull(), cols.ClaimRunID.EQ(psql.Arg(c.Holder)), cols.ClaimUntil.LTE(psql.Arg(c.Now)))
}

func (q Queries) ClaimableWait(ctx context.Context, c waits.Claim, seen []string) (waits.Wait, error) {
	w := models.SelectWhere.WorkflowWaits
	cols := models.WorkflowWaits.Columns
	query := models.WorkflowWaits.Query(w.Kind.EQ(c.Kind), w.Status.EQ(string(waits.StatusOpen)), sm.Where(claimable(c)), sm.OrderBy(cols.CreatedAt), sm.OrderBy(cols.ID), sm.Limit(1))
	if len(seen) > 0 {
		query.Apply(w.ID.NotIn(seen...))
	}
	if c.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(c.WorkspaceID))
	}
	if c.Key != "" {
		query.Apply(w.Key.EQ(c.Key))
	}
	row, err := query.One(ctx, q.exec)
	if err != nil {
		return waits.Wait{}, dberr.Translate(err)
	}
	return waitRecord(row), nil
}

func (q Queries) ClaimWait(ctx context.Context, id string, c waits.Claim, token string, until time.Time) (waits.Wait, int64, error) {
	set := &models.WorkflowWaitSetter{ClaimRunID: omitnull.From(c.Holder), ClaimToken: omitnull.From(token), ClaimUntil: omitnull.From(until), UpdatedAt: omit.From(c.Now)}
	w := models.UpdateWhere.WorkflowWaits
	rows, err := models.WorkflowWaits.Update(set.UpdateMod(), w.ID.EQ(id), w.Status.EQ(string(waits.StatusOpen)), um.Where(claimable(c))).All(ctx, q.exec)
	if err != nil || len(rows) == 0 {
		return waits.Wait{}, 0, dberr.Translate(err)
	}
	return waitRecord(rows[0]), 1, nil
}

func (q Queries) ConsumeWait(ctx context.Context, kind, key, runID string, at time.Time) (int64, error) {
	set := &models.WorkflowWaitSetter{Parking: omit.From(false), UpdatedAt: omit.From(at)}
	w := models.UpdateWhere.WorkflowWaits
	n, err := models.WorkflowWaits.Update(set.UpdateMod(), w.Kind.EQ(kind), w.Key.EQ(key), w.RunID.EQ(runID), w.Status.NE(string(waits.StatusOpen))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) CloseWait(ctx context.Context, kind, key, runID string, at time.Time) (int64, error) {
	set := closed(at)
	w := models.UpdateWhere.WorkflowWaits
	n, err := models.WorkflowWaits.Update(set.UpdateMod(), w.Kind.EQ(kind), w.Key.EQ(key), w.RunID.EQ(runID), w.Status.EQ(string(waits.StatusOpen))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) CloseRunWaits(ctx context.Context, runID string, at time.Time) error {
	w := models.UpdateWhere.WorkflowWaits
	open := w.Status.EQ(string(waits.StatusOpen))
	if _, err := models.WorkflowWaits.Update(closed(at).UpdateMod(), w.RunID.EQ(runID), open).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	unpark := &models.WorkflowWaitSetter{Parking: omit.From(false), UpdatedAt: omit.From(at)}
	if _, err := models.WorkflowWaits.Update(unpark.UpdateMod(), w.RunID.EQ(runID), w.Parking.EQ(true)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	_, err := models.WorkflowWaits.Update(unclaimed(at).UpdateMod(), w.ClaimRunID.EQ(runID), open).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteRunWaits(ctx context.Context, runIDs []string) error {
	_, err := models.WorkflowWaits.Delete(models.DeleteWhere.WorkflowWaits.RunID.In(runIDs...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateWaitData(ctx context.Context, kind, key, holder, token string, data json.RawMessage, at time.Time) (int64, error) {
	set := &models.WorkflowWaitSetter{Data: omit.From(types.NewJSON(data)), UpdatedAt: omit.From(at)}
	w := models.UpdateWhere.WorkflowWaits
	cols := models.WorkflowWaits.Columns
	holds := psql.Or(cols.RunID.EQ(psql.Arg(holder)), psql.And(cols.ClaimRunID.EQ(psql.Arg(holder)), cols.ClaimToken.EQ(psql.Arg(token)), cols.ClaimUntil.GT(psql.Arg(at))))
	n, err := models.WorkflowWaits.Update(set.UpdateMod(), w.Kind.EQ(kind), w.Key.EQ(key), w.Status.EQ(string(waits.StatusOpen)), um.Where(holds)).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) ReleaseWaitClaim(ctx context.Context, kind, key, holder, token string, at time.Time) (int64, error) {
	w := models.UpdateWhere.WorkflowWaits
	n, err := models.WorkflowWaits.Update(unclaimed(at).UpdateMod(), w.Kind.EQ(kind), w.Key.EQ(key), w.Status.EQ(string(waits.StatusOpen)), w.ClaimRunID.EQ(holder), w.ClaimToken.EQ(token)).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) SetWaitNotified(ctx context.Context, id string, at time.Time) error {
	set := &models.WorkflowWaitSetter{NotifiedAt: omitnull.From(at)}
	_, err := models.WorkflowWaits.Update(set.UpdateMod(), models.UpdateWhere.WorkflowWaits.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) WaitByID(ctx context.Context, id string) (waits.Wait, error) {
	row, err := models.FindWorkflowWait(ctx, q.exec, id)
	if err != nil {
		return waits.Wait{}, dberr.Translate(err)
	}
	return waitRecord(row), nil
}

func (q Queries) RunExecution(ctx context.Context, runID string) (waits.Execution, error) {
	row, err := models.FindWorkflowRun(ctx, q.exec, runID, "workflow_id", "workflow_version", "workspace_id", "item_id")
	if err != nil {
		return waits.Execution{}, dberr.Translate(err)
	}
	return waits.Execution{WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, ItemID: row.ItemID.GetOrZero()}, nil
}

func (q Queries) VersionAuthor(ctx context.Context, workflowID string, number int64) (string, error) {
	w := models.SelectWhere.WorkflowVersions
	row, err := models.WorkflowVersions.Query(sm.Columns(models.WorkflowVersions.Columns.CreatedBy), w.WorkflowID.EQ(workflowID), w.Number.EQ(number)).One(ctx, q.exec)
	if err != nil {
		return "", dberr.Translate(err)
	}
	return row.CreatedBy, nil
}

func (q Queries) LeasedSources(ctx context.Context, ownerID string) ([]waits.Source, error) {
	blobs, leases := models.Blobs.Columns, models.BlobLeases.Columns
	rows, err := models.Blobs.Query(
		sm.Columns(blobs.ID, blobs.ItemID),
		sm.InnerJoin(models.BlobLeases.Name()).On(leases.BlobID.EQ(blobs.ID)),
		sm.Where(leases.OwnerID.EQ(psql.Arg(ownerID))),
		sm.OrderBy(leases.CreatedAt), sm.OrderBy(leases.ID),
	).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]waits.Source, len(rows))
	for i, row := range rows {
		out[i] = waits.Source{ItemID: row.ItemID, BlobID: row.ID}
	}
	return out, nil
}

// unclaimed clears a wait's claim.
func unclaimed(at time.Time) *models.WorkflowWaitSetter {
	return &models.WorkflowWaitSetter{
		ClaimRunID: omitnull.FromNull(null.Val[string]{}), ClaimToken: omitnull.FromNull(null.Val[string]{}),
		ClaimUntil: omitnull.FromNull(null.Val[time.Time]{}), UpdatedAt: omit.From(at),
	}
}

// closed closes a wait, ending its parking and claim.
func closed(at time.Time) *models.WorkflowWaitSetter {
	set := unclaimed(at)
	set.Status, set.Parking = omit.From(string(waits.StatusClosed)), omit.From(false)
	return set
}

func waitRecord(r *models.WorkflowWait) waits.Wait {
	return waits.Wait{
		ID: r.ID, Kind: r.Kind, Key: r.Key, WorkspaceID: r.WorkspaceID, RunID: r.RunID, Node: r.Node, ExecutionID: r.ExecutionID,
		Queue: r.Queue, PayloadID: r.PayloadID, Data: r.Data.Val, Completion: r.Completion.GetOrZero().Val,
		Deadline: nullable.UTC(r.Deadline), NotifiedAt: nullable.UTC(r.NotifiedAt), ClaimRunID: r.ClaimRunID.GetOrZero(),
		ClaimToken: r.ClaimToken.GetOrZero(), ClaimUntil: nullable.UTC(r.ClaimUntil), Status: waits.Status(r.Status), Parking: r.Parking,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}
