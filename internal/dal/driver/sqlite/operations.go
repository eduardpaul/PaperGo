package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/dialect/sqlite/um"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
)

// operationsMatching returns the conditions f selects.
func operationsMatching(f operations.Filter) []dialect.Expression {
	c := models.Operations.Columns
	var where []dialect.Expression
	if f.ContainerID != "" {
		where = append(where, c.ContainerID.EQ(sqlite.Arg(f.ContainerID)))
	}
	if f.FieldID != "" {
		where = append(where, c.FieldID.EQ(sqlite.Arg(f.FieldID)))
	}
	if f.CreatedBy != "" {
		where = append(where, c.CreatedBy.EQ(sqlite.Arg(f.CreatedBy)))
	}
	if f.AfterID != "" {
		where = append(where, c.ID.GT(sqlite.Arg(f.AfterID)))
	}
	if len(f.IDs) > 0 {
		where = append(where, c.ID.In(sqlite.Arg(anys(f.IDs)...)))
	}
	if len(f.Kinds) > 0 {
		where = append(where, c.Kind.In(sqlite.Arg(anys(f.Kinds)...)))
	}
	if len(f.ExcludeKinds) > 0 {
		where = append(where, c.Kind.NotIn(sqlite.Arg(anys(f.ExcludeKinds)...)))
	}
	if len(f.Statuses) > 0 {
		statuses := make([]any, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		where = append(where, c.Status.In(sqlite.Arg(statuses...)))
	}
	if f.Finished {
		where = append(where, c.Status.NE(sqlite.Arg(string(operations.StatusPending))))
	}
	if f.UpdatedThrough != nil {
		where = append(where, c.UpdatedAt.LTE(sqlite.Arg(f.UpdatedThrough.UTC())))
	}
	if f.DeadlineBefore != nil {
		where = append(where, c.Deadline.LT(sqlite.Arg(f.DeadlineBefore.UTC())))
	}
	if f.ExecutionID != "" {
		where = append(where, c.ExecutionID.EQ(sqlite.Arg(f.ExecutionID)))
	}
	if len(f.ParentIDs) > 0 {
		where = append(where, c.ParentID.In(sqlite.Arg(anys(f.ParentIDs)...)))
	}
	if f.OwnedBy != "" {
		where = append(where, sqlite.Or(c.CreatedBy.EQ(sqlite.Arg(f.OwnedBy)), c.Kind.In(sqlite.Arg(anys(f.SharedKinds)...))))
	}
	if f.PayloadField != "" {
		where = append(where, sqlite.Raw(`json_extract("operations"."payload", ?) = ?`, "$."+f.PayloadField, f.PayloadValue))
	}
	return where
}

// operationsReady returns the conditions of pending operations of kinds that
// a worker may claim at now.
func operationsReady(kinds []string, now time.Time) []dialect.Expression {
	c := models.Operations.Columns
	return append([]dialect.Expression{c.Status.EQ(sqlite.Arg(string(operations.StatusPending))), c.Kind.In(sqlite.Arg(anys(kinds)...))}, operationsClaimable(now)...)
}

// operationsClaimable returns the conditions of operations no execution
// owns at now that are due and still before their deadline.
func operationsClaimable(now time.Time) []dialect.Expression {
	c := models.Operations.Columns
	return []dialect.Expression{
		c.ExecutionID.EQ(sqlite.Arg("")),
		c.AttemptAt.LTE(sqlite.Arg(now)),
		sqlite.Or(c.ClaimUntil.IsNull(), c.ClaimUntil.LTE(sqlite.Arg(now))),
		sqlite.Or(c.Deadline.IsNull(), c.Deadline.GT(sqlite.Arg(now))),
	}
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

func (q Queries) FindIdempotent(ctx context.Context, createdBy, kind, containerID, key string) (operations.Operation, error) {
	c := models.Operations.Columns
	container := c.ContainerID.IsNull()
	if containerID != "" {
		container = c.ContainerID.EQ(sqlite.Arg(containerID))
	}
	row, err := models.Operations.Query(sm.Where(c.CreatedBy.EQ(sqlite.Arg(createdBy))), sm.Where(c.Kind.EQ(sqlite.Arg(kind))),
		sm.Where(container), sm.Where(c.IdempotencyKey.EQ(sqlite.Arg(key)))).One(ctx, q.exec)
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
	return operationRows(query.All(ctx, q.exec))
}

func operationRows(rows models.OperationSlice, err error) ([]operations.Operation, error) {
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
		WorkspaceID: nullable.String(op.WorkspaceID), FieldID: omit.From(op.FieldID), Kind: omit.From(op.Kind), TraceID: omit.From(op.TraceID),
		CredentialID: nullable.String(op.CredentialID), CredentialScopes: omit.From(types.NewJSON(op.CredentialScopes)), Result: omit.From(types.NewJSON(op.Result)),
		Status: omit.From(string(op.Status)), ErrorCode: omit.From(string(op.ErrorCode)), Surface: omit.From(string(op.Surface)), AfterItemID: omit.From(op.AfterItemID),
		Processed: omit.From(int64(op.Processed)), Error: omit.From(op.Error), CreatedBy: omit.From(op.CreatedBy), Payload: omit.From(types.NewJSON(op.Payload)),
		Prepared: omit.From(op.Prepared), Matched: omit.From(int64(op.Matched)), Updated: omit.From(int64(op.Updated)), Failed: omit.From(int64(op.Failed)),
		Progress: omit.From(int64(op.Progress)), Failures: omit.From(types.NewJSON(op.Failures)),
		StartedAt: nullable.Time(op.StartedAt), CompletedAt: nullable.Time(op.CompletedAt), Deadline: nullable.Time(op.Deadline),
		AttemptAt: omit.From(op.AttemptAt), Attempts: omit.From(int64(op.Attempts)), ExecutionID: omit.From(op.ExecutionID),
		GroupKey: omit.From(op.GroupKey), InputKey: omit.From(op.InputKey), IdempotencyKey: omit.From(op.IdempotencyKey), PayloadDigest: omit.From(op.PayloadDigest),
		ParentID: nullable.String(op.ParentID),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// The writer slot already serializes SQLite claimers, so ready operations
// need no row locks.
func (q Queries) ReadyOperations(ctx context.Context, r operations.Ready) ([]operations.Operation, error) {
	c := models.Operations.Columns
	query := models.Operations.Query()
	for _, where := range operationsReady(r.Kinds, r.Now) {
		query.Apply(sm.Where(where))
	}
	switch {
	case r.WorkspaceID == nil:
	case *r.WorkspaceID == "":
		query.Apply(sm.Where(c.WorkspaceID.IsNull()))
	default:
		query.Apply(sm.Where(c.WorkspaceID.EQ(sqlite.Arg(*r.WorkspaceID))))
	}
	if len(r.IDs) > 0 {
		query.Apply(sm.Where(c.ID.In(sqlite.Arg(anys(r.IDs)...))))
	}
	if r.GroupKey != nil {
		query.Apply(sm.Where(c.GroupKey.EQ(sqlite.Arg(*r.GroupKey))), sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	} else {
		query.Apply(sm.OrderBy(c.AttemptAt), sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	}
	if r.Through != nil {
		query.Apply(sm.Where(c.CreatedAt.LTE(sqlite.Arg(r.Through.UTC()))))
	}
	if r.Limit > 0 {
		query.Apply(sm.Limit(r.Limit))
	}
	return operationRows(query.All(ctx, q.exec))
}

func (q Queries) ClaimOperations(ctx context.Context, ids []string, claim operations.Claim, now time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	c := models.Operations.Columns
	update := models.Operations.Update(
		um.SetCol("claim_holder").ToArg(claim.Holder), um.SetCol("claim_token").ToArg(claim.Token), um.SetCol("claim_until").ToArg(claim.Until),
		um.SetCol("attempts").To(sqlite.Raw("attempts + 1")), um.SetCol("updated_at").ToArg(now),
		um.Where(c.ID.In(sqlite.Arg(anys(ids)...))),
	)
	for _, where := range operationsClaimable(now) {
		update.Apply(um.Where(where))
	}
	update.Apply(um.Where(c.Status.EQ(sqlite.Arg(string(operations.StatusPending)))))
	n, err := update.Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) UpdateFenced(ctx context.Context, f operations.Fence, ch operations.Change) (int64, error) {
	if len(f.IDs) == 0 {
		return 0, nil
	}
	c := models.Operations.Columns
	set := &models.OperationSetter{UpdatedAt: omit.From(ch.UpdatedAt)}
	if op := ch.Progress; op != nil {
		set.Result, set.Status, set.ErrorCode = omit.From(types.NewJSON(op.Result)), omit.From(string(op.Status)), omit.From(string(op.ErrorCode))
		set.Surface, set.AfterItemID, set.Processed = omit.From(string(op.Surface)), omit.From(op.AfterItemID), omit.From(int64(op.Processed))
		set.Error, set.Prepared, set.Matched, set.Updated = omit.From(op.Error), omit.From(op.Prepared), omit.From(int64(op.Matched)), omit.From(int64(op.Updated))
		set.Failed, set.Progress, set.Failures = omit.From(int64(op.Failed)), omit.From(int64(op.Progress)), omit.From(types.NewJSON(op.Failures))
		set.StartedAt, set.CompletedAt = nullable.Time(op.StartedAt), nullable.Time(op.CompletedAt)
	}
	switch {
	case ch.ClearClaim:
		set.ClaimHolder, set.ClaimToken, set.ClaimUntil = omitnull.FromPtr[string](nil), omitnull.FromPtr[string](nil), omitnull.FromPtr[time.Time](nil)
	case ch.Claim != nil:
		set.ClaimHolder, set.ClaimToken, set.ClaimUntil = omitnull.From(ch.Claim.Holder), omitnull.From(ch.Claim.Token), omitnull.From(ch.Claim.Until)
	}
	if ch.AttemptAt != nil {
		set.AttemptAt = omit.From(*ch.AttemptAt)
	}
	if ch.ExecutionID != nil {
		set.ExecutionID = omit.From(*ch.ExecutionID)
	}
	if ch.Error != nil {
		set.Error = omit.From(*ch.Error)
	}
	update := models.Operations.Update(set.UpdateMod(), um.Where(c.ID.In(sqlite.Arg(anys(f.IDs)...))), um.Where(c.Status.EQ(sqlite.Arg(string(operations.StatusPending)))))
	if ch.Progress != nil && ch.Progress.Status == operations.StatusSucceeded {
		// Past its deadline an operation can no longer succeed.
		update.Apply(um.Where(sqlite.Or(c.Deadline.IsNull(), c.Deadline.GT(sqlite.Arg(f.Now)))))
	}
	if ch.Attempts != 0 {
		update.Apply(um.SetCol("attempts").To(sqlite.Raw("max(attempts + ?, 0)", ch.Attempts)))
	}
	if f.Token != "" {
		update.Apply(um.Where(c.ClaimToken.EQ(sqlite.Arg(f.Token))), um.Where(c.ClaimUntil.GT(sqlite.Arg(f.Now))))
	} else {
		update.Apply(um.Where(c.ExecutionID.EQ(sqlite.Arg(f.ExecutionID))), um.Where(c.ExecutionID.NE(sqlite.Arg(""))))
	}
	n, err := update.Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) TerminateOperations(ctx context.Context, ids []string, t operations.Termination) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	c := models.Operations.Columns
	set := &models.OperationSetter{
		Status: omit.From(string(t.Status)), ErrorCode: omit.From(string(t.Code)), CompletedAt: omitnull.From(t.At), UpdatedAt: omit.From(t.At),
		ClaimHolder: omitnull.FromPtr[string](nil), ClaimToken: omitnull.FromPtr[string](nil), ClaimUntil: omitnull.FromPtr[time.Time](nil), ExecutionID: omit.From(""),
	}
	if t.Error != "" {
		set.Error = omit.From(t.Error)
	}
	n, err := models.Operations.Update(set.UpdateMod(), um.Where(c.ID.In(sqlite.Arg(anys(ids)...))), um.Where(c.Status.EQ(sqlite.Arg(string(operations.StatusPending))))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) ReleaseOperationRetention(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := models.BlobLeases.Delete(models.DeleteWhere.BlobLeases.OperationID.In(ids...)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	args := append([]any{string(activitystore.OwnerOperation)}, anys(ids)...)
	_, err := bob.Exec(ctx, q.exec, sqlite.RawQuery(`DELETE FROM activity_references WHERE owner_kind = ? AND owner_id IN (`+marks(len(ids))+`)`, args...))
	return dberr.Translate(err)
}

func (q Queries) ReadyGroups(ctx context.Context, kinds []string, now time.Time) ([]operations.Group, error) {
	type count struct {
		Kind, GroupKey string
		Inputs         int
	}
	query := sqlite.Select(
		sm.Columns(models.Operations.Columns.Kind, models.Operations.Columns.GroupKey,
			sqlite.Raw("COUNT(DISTINCT NULLIF(input_key, '')) + SUM(CASE WHEN input_key = '' THEN 1 ELSE 0 END)").As("inputs")),
		sm.From(models.Operations.Name()), sm.GroupBy(models.Operations.Columns.Kind), sm.GroupBy(models.Operations.Columns.GroupKey),
	)
	for _, where := range operationsReady(kinds, now) {
		query.Apply(sm.Where(where))
	}
	counts, err := bob.All(ctx, q.exec, query, scan.StructMapper[count]())
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]operations.Group, len(counts))
	for i, g := range counts {
		oldest := models.Operations.Query(sm.Where(models.Operations.Columns.GroupKey.EQ(sqlite.Arg(g.GroupKey))), sm.OrderBy(models.Operations.Columns.CreatedAt), sm.Limit(1))
		for _, where := range operationsReady([]string{g.Kind}, now) {
			oldest.Apply(sm.Where(where))
		}
		row, err := oldest.One(ctx, q.exec)
		if err != nil {
			return nil, dberr.Translate(err)
		}
		out[i] = operations.Group{Kind: g.Kind, Key: g.GroupKey, Inputs: g.Inputs, Oldest: row.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) ReadyWorkspaces(ctx context.Context, kinds []string, now time.Time, limit int) ([]string, error) {
	query := sqlite.Select(sm.Distinct(), sm.Columns(sqlite.Raw("COALESCE(workspace_id, '')").As("workspace")), sm.From(models.Operations.Name()), sm.Limit(limit))
	for _, where := range operationsReady(kinds, now) {
		query.Apply(sm.Where(where))
	}
	out, err := bob.All(ctx, q.exec, query, scan.SingleColumnMapper[string])
	return out, dberr.Translate(err)
}

func (q Queries) NextAttempt(ctx context.Context, kinds []string, now time.Time) (*time.Time, error) {
	c := models.Operations.Columns
	pending := []dialect.Expression{c.Status.EQ(sqlite.Arg(string(operations.StatusPending))), c.Kind.In(sqlite.Arg(anys(kinds)...)), c.ExecutionID.EQ(sqlite.Arg(""))}
	var next *time.Time
	earliest := func(where []dialect.Expression, order any, at func(*models.Operation) time.Time) error {
		query := models.Operations.Query(sm.OrderBy(order), sm.Limit(1))
		for _, w := range where {
			query.Apply(sm.Where(w))
		}
		row, err := query.One(ctx, q.exec)
		if dberr.IsNotFound(dberr.Translate(err)) {
			return nil
		}
		if err != nil {
			return dberr.Translate(err)
		}
		if t := at(row).UTC(); next == nil || t.Before(*next) {
			next = &t
		}
		return nil
	}
	unclaimed := append(append([]dialect.Expression{}, pending...), sqlite.Or(c.ClaimUntil.IsNull(), c.ClaimUntil.LTE(sqlite.Arg(now))))
	if err := earliest(unclaimed, c.AttemptAt, func(r *models.Operation) time.Time { return r.AttemptAt }); err != nil {
		return nil, err
	}
	claimed := append(append([]dialect.Expression{}, pending...), c.ClaimUntil.GT(sqlite.Arg(now)))
	if err := earliest(claimed, c.ClaimUntil, func(r *models.Operation) time.Time { return r.ClaimUntil.GetOrZero() }); err != nil {
		return nil, err
	}
	return next, nil
}

func (q Queries) PendingTotals(ctx context.Context, createdBy, workspaceID string, exempt, unasked []string) (operations.Totals, error) {
	kinds, asked := "", ""
	args := []any{createdBy}
	if len(unasked) > 0 {
		asked = ` AND kind NOT IN (` + marks(len(unasked)) + `)`
		args = append(args, anys(unasked)...)
	}
	args = append(args, workspaceID, workspaceID)
	if len(exempt) > 0 {
		kinds = ` AND kind NOT IN (` + marks(len(exempt)) + `)`
		args = append(args, anys(exempt)...)
	}
	t, err := bob.One(ctx, q.exec, sqlite.RawQuery(`SELECT
		COALESCE(SUM(CASE WHEN created_by = ? AND parent_id IS NULL`+asked+` THEN 1 ELSE 0 END), 0) AS creator,
		COALESCE(SUM(CASE WHEN workspace_id = ? THEN 1 ELSE 0 END), 0) AS workspace,
		COUNT(*) AS installation,
		COALESCE(SUM(CASE WHEN workspace_id = ? THEN length(CAST(payload AS BLOB)) ELSE 0 END), 0) AS workspace_payload_bytes
		FROM operations WHERE status = 'pending'`+kinds, args...), scan.StructMapper[operations.Totals]())
	return t, dberr.Translate(err)
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
	op := operations.Operation{
		ID: r.ID, ContainerID: r.ContainerID.GetOrZero(), WorkspaceID: r.WorkspaceID.GetOrZero(), FieldID: r.FieldID, Kind: r.Kind, TraceID: r.TraceID, CreatedBy: r.CreatedBy,
		CredentialID: r.CredentialID.GetOrZero(), CredentialScopes: r.CredentialScopes.Val, Payload: r.Payload.Val, Result: r.Result.Val,
		Status: operations.Status(r.Status), ErrorCode: operations.ErrorCode(r.ErrorCode), Surface: refs.Surface(r.Surface), AfterItemID: r.AfterItemID, Prepared: r.Prepared,
		Processed: int(r.Processed), Matched: int(r.Matched), Updated: int(r.Updated), Failed: int(r.Failed), Progress: int(r.Progress),
		Failures: r.Failures.Val, Error: r.Error, StartedAt: nullable.UTC(r.StartedAt), CompletedAt: nullable.UTC(r.CompletedAt), Deadline: nullable.UTC(r.Deadline),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), AttemptAt: r.AttemptAt.UTC(), Attempts: int(r.Attempts),
		ExecutionID: r.ExecutionID, GroupKey: r.GroupKey, InputKey: r.InputKey, IdempotencyKey: r.IdempotencyKey, PayloadDigest: r.PayloadDigest,
		ParentID: r.ParentID.GetOrZero(),
	}
	if token, ok := r.ClaimToken.Get(); ok {
		op.Claim = operations.Claim{Holder: r.ClaimHolder.GetOrZero(), Token: token, Until: r.ClaimUntil.GetOrZero().UTC()}
	}
	return op
}
