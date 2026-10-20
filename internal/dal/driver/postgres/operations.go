package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/dialect/psql/um"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/runs"
)

// operationsMatching returns the conditions f selects.
func operationsMatching(f operations.Filter) []dialect.Expression {
	c := models.Operations.Columns
	var where []dialect.Expression
	if f.ContainerID != "" {
		where = append(where, c.ContainerID.EQ(psql.Arg(f.ContainerID)))
	}
	if f.FieldID != "" {
		where = append(where, c.FieldID.EQ(psql.Arg(f.FieldID)))
	}
	if f.CreatedBy != "" {
		where = append(where, c.CreatedBy.EQ(psql.Arg(f.CreatedBy)))
	}
	if f.AfterID != "" {
		where = append(where, c.ID.GT(psql.Arg(f.AfterID)))
	}
	if len(f.Kinds) > 0 {
		where = append(where, c.Kind.In(psql.Arg(anys(f.Kinds)...)))
	}
	if len(f.Statuses) > 0 {
		statuses := make([]any, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		where = append(where, c.Status.In(psql.Arg(statuses...)))
	}
	if f.Finished {
		where = append(where, c.Status.NE(psql.Arg(string(operations.StatusPending))))
	}
	if f.UpdatedThrough != nil {
		where = append(where, c.UpdatedAt.LTE(psql.Arg(f.UpdatedThrough.UTC())))
	}
	if f.OwnedBy != "" {
		where = append(where, psql.Or(c.CreatedBy.EQ(psql.Arg(f.OwnedBy)), c.Kind.In(psql.Arg(anys(f.SharedKinds)...))))
	}
	if f.PayloadField != "" {
		where = append(where, psql.Raw(`"operations"."payload" ->> ? = ?`, f.PayloadField, f.PayloadValue))
	}
	return where
}

func anys(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func (q Queries) FindOperation(ctx context.Context, id string) (operations.Operation, error) {
	row, err := models.FindOperation(ctx, q.exec, id)
	if err != nil {
		return operations.Operation{}, dberr.Translate(err)
	}
	return operationRecord(row), nil
}

func (q Queries) NextOperation(ctx context.Context) (operations.Operation, error) {
	c := models.Operations.Columns
	row, err := models.Operations.Query(models.SelectWhere.Operations.Status.EQ(string(operations.StatusPending)), sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID), sm.Limit(1)).One(ctx, q.exec)
	if err != nil {
		return operations.Operation{}, dberr.Translate(err)
	}
	return operationRecord(row), nil
}

func (q Queries) Operations(ctx context.Context, f operations.Filter) ([]operations.Operation, error) {
	query := models.Operations.Query(sm.OrderBy(models.Operations.Columns.ID))
	for _, where := range operationsMatching(f) {
		query.Apply(sm.Where(where))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]operations.Operation, len(rows))
	for i, row := range rows {
		out[i] = operationRecord(row)
	}
	return out, nil
}

func (q Queries) CountOperations(ctx context.Context, f operations.Filter) (int64, error) {
	query := models.Operations.Query()
	for _, where := range operationsMatching(f) {
		query.Apply(sm.Where(where))
	}
	n, err := query.Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) InsertOperation(ctx context.Context, op operations.Operation) error {
	_, err := models.Operations.Insert(&models.OperationSetter{
		ID: omit.From(op.ID), CreatedAt: omit.From(op.CreatedAt), UpdatedAt: omit.From(op.UpdatedAt), ContainerID: nullable.String(op.ContainerID),
		FieldID: omit.From(op.FieldID), Kind: omit.From(op.Kind), TraceID: omit.From(op.TraceID), CredentialID: nullable.String(op.CredentialID),
		CredentialScopes: omit.From(types.NewJSON(op.CredentialScopes)), Result: omit.From(types.NewJSON(op.Result)), Status: omit.From(string(op.Status)),
		Surface: omit.From(string(op.Surface)), AfterItemID: omit.From(op.AfterItemID), Processed: omit.From(int64(op.Processed)),
		Error: omit.From(op.Error), CreatedBy: omit.From(op.CreatedBy), Payload: omit.From(types.NewJSON(op.Payload)), Prepared: omit.From(op.Prepared),
		Matched: omit.From(int64(op.Matched)), Updated: omit.From(int64(op.Updated)), Failed: omit.From(int64(op.Failed)),
		Progress: omit.From(int64(op.Progress)), Failures: omit.From(types.NewJSON(op.Failures)),
		StartedAt: nullable.Time(op.StartedAt), CompletedAt: nullable.Time(op.CompletedAt),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateOperation(ctx context.Context, op operations.Operation) error {
	set := &models.OperationSetter{
		UpdatedAt: omit.From(op.UpdatedAt), Result: omit.From(types.NewJSON(op.Result)), Status: omit.From(string(op.Status)),
		Surface: omit.From(string(op.Surface)), AfterItemID: omit.From(op.AfterItemID), Processed: omit.From(int64(op.Processed)),
		Error: omit.From(op.Error), Prepared: omit.From(op.Prepared), Matched: omit.From(int64(op.Matched)), Updated: omit.From(int64(op.Updated)),
		Failed: omit.From(int64(op.Failed)), Progress: omit.From(int64(op.Progress)), Failures: omit.From(types.NewJSON(op.Failures)),
		StartedAt: nullable.Time(op.StartedAt), CompletedAt: nullable.Time(op.CompletedAt),
	}
	n, err := models.Operations.Update(set.UpdateMod(), models.UpdateWhere.Operations.ID.EQ(op.ID)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n == 0 {
		return dberr.ErrNotFound
	}
	return nil
}

func (q Queries) SupersedeOperations(ctx context.Context, f operations.Filter, at time.Time) error {
	f.Statuses = []operations.Status{operations.StatusPending}
	set := &models.OperationSetter{Status: omit.From(string(operations.StatusSuperseded)), CompletedAt: omitnull.From(at), UpdatedAt: omit.From(at)}
	update := models.Operations.Update(set.UpdateMod())
	for _, where := range operationsMatching(f) {
		update.Apply(um.Where(where))
	}
	_, err := update.Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteOperations(ctx context.Context, ids []string) error {
	_, err := models.Operations.Delete(models.DeleteWhere.Operations.ID.In(ids...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) OperationItems(ctx context.Context, operationID string, limit int) ([]operations.Item, error) {
	rows, err := models.OperationItems.Query(models.SelectWhere.OperationItems.OperationID.EQ(operationID), sm.OrderBy(models.OperationItems.Columns.ItemID), sm.Limit(limit)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]operations.Item, len(rows))
	for i, r := range rows {
		out[i] = operations.Item{ID: r.ID, OperationID: r.OperationID, ItemID: r.ItemID, CreatedAt: r.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) CountOperationItems(ctx context.Context, operationID string) (int64, error) {
	n, err := models.OperationItems.Query(models.SelectWhere.OperationItems.OperationID.EQ(operationID)).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) DeleteOperationItem(ctx context.Context, id string) error {
	_, err := models.OperationItems.Delete(models.DeleteWhere.OperationItems.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteOperationItems(ctx context.Context, operationIDs []string) error {
	_, err := models.OperationItems.Delete(models.DeleteWhere.OperationItems.OperationID.In(operationIDs...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func operationRecord(r *models.Operation) operations.Operation {
	return operations.Operation{
		ID: r.ID, ContainerID: r.ContainerID.GetOrZero(), FieldID: r.FieldID, Kind: r.Kind, TraceID: r.TraceID, CreatedBy: r.CreatedBy,
		CredentialID: r.CredentialID.GetOrZero(), CredentialScopes: r.CredentialScopes.Val, Payload: r.Payload.Val, Result: r.Result.Val,
		Status: operations.Status(r.Status), Surface: refs.Surface(r.Surface), AfterItemID: r.AfterItemID, Prepared: r.Prepared,
		Processed: int(r.Processed), Matched: int(r.Matched), Updated: int(r.Updated), Failed: int(r.Failed), Progress: int(r.Progress),
		Failures: r.Failures.Val, Error: r.Error, StartedAt: nullable.UTC(r.StartedAt), CompletedAt: nullable.UTC(r.CompletedAt),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func (q Queries) ReleaseWaits(ctx context.Context, operationIDs []string, at time.Time) error {
	if len(operationIDs) == 0 {
		return nil
	}
	args := []any{string(runs.StepCompleted), at, string(runs.StepPending), string(runs.StepWait)}
	marks := make([]string, len(operationIDs))
	for i, id := range operationIDs {
		args = append(args, id)
		marks[i] = "$" + strconv.Itoa(5+i)
	}
	args = append(args, string(operations.StatusPending))
	_, err := q.exec.ExecContext(ctx, `UPDATE workflow_steps SET status = $1, completed_at = $2
WHERE status = $3 AND kind = $4
  AND EXISTS (SELECT 1 FROM workflow_step_operations l WHERE l.run_id = workflow_steps.run_id AND l.seq = workflow_steps.seq AND l.operation_id IN (`+strings.Join(marks, ", ")+`))
  AND NOT EXISTS (SELECT 1 FROM workflow_step_operations l JOIN operations o ON o.id = l.operation_id
    WHERE l.run_id = workflow_steps.run_id AND l.seq = workflow_steps.seq AND o.status = `+"$"+strconv.Itoa(len(args))+`)`, args...)
	return dberr.Translate(err)
}
