package postgres

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/workflows"
	"papergo/internal/model"
)

func (q Queries) FindWorkflow(ctx context.Context, id string) (workflows.Workflow, error) {
	row, err := models.FindWorkflow(ctx, q.exec, id)
	if err != nil {
		return workflows.Workflow{}, dberr.Translate(err)
	}
	return workflowRecord(row), nil
}

func (q Queries) Workflows(ctx context.Context, f workflows.Filter) ([]workflows.Workflow, error) {
	query := models.Workflows.Query()
	w := models.SelectWhere.Workflows
	c := models.Workflows.Columns
	if len(f.IDs) > 0 {
		query.Apply(w.ID.In(f.IDs...))
	}
	if len(f.BuiltinKeys) > 0 {
		query.Apply(w.BuiltinKey.In(f.BuiltinKeys...))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.Name != "" {
		query.Apply(w.Name.EQ(f.Name))
	}
	if f.BuiltinKey != "" {
		query.Apply(w.BuiltinKey.EQ(f.BuiltinKey))
	}
	if f.Role != "" {
		query.Apply(w.Role.EQ(f.Role))
	}
	if f.CollectionID != "" {
		query.Apply(w.CollectionID.EQ(f.CollectionID))
	}
	if f.CollectionAbsent {
		query.Apply(w.CollectionID.IsNull())
	}
	if f.ExcludeID != "" {
		query.Apply(w.ID.NE(f.ExcludeID))
	}
	if f.State != "" {
		query.Apply(w.State.EQ(string(f.State)))
	}
	if f.LiveOnly {
		query.Apply(w.DeletedAt.IsNull())
	}
	if f.OnlyBuiltins {
		query.Apply(w.BuiltinKey.IsNotNull())
	}
	if f.AutomaticEnabled != nil {
		query.Apply(w.AutomaticEnabled.EQ(*f.AutomaticEnabled))
	}
	if f.AutomaticProvider != nil {
		query.Apply(w.AutomaticProvider.EQ(*f.AutomaticProvider))
	}
	switch f.Order {
	case "name":
		query.Apply(sm.OrderBy(c.Name))
	case "provider":
		query.Apply(sm.OrderBy(c.AutomaticEnabled).Desc(), sm.OrderBy(c.ID))
	default:
		query.Apply(sm.OrderBy(c.ID))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]workflows.Workflow, len(rows))
	for i, row := range rows {
		out[i] = workflowRecord(row)
	}
	return out, nil
}

func (q Queries) WorkflowProviderExists(ctx context.Context, workspaceID, collectionID, builtinKey, role string) (bool, error) {
	w := models.SelectWhere.Workflows
	c := models.Workflows.Columns
	provider := psql.Or(c.BuiltinKey.EQ(psql.Arg(builtinKey)), psql.And(c.Role.EQ(psql.Arg(role)), c.AutomaticProvider.EQ(psql.Arg(true))))
	ok, err := models.Workflows.Query(w.WorkspaceID.EQ(workspaceID), w.CollectionID.EQ(collectionID), sm.Where(provider)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) InsertWorkflow(ctx context.Context, wf workflows.Workflow) error {
	_, err := models.Workflows.Insert(&models.WorkflowSetter{
		ID: omit.From(wf.ID), CreatedAt: omit.From(wf.CreatedAt), UpdatedAt: omit.From(wf.UpdatedAt), WorkspaceID: omit.From(wf.WorkspaceID),
		Key: omit.From(wf.Key), Name: omit.From(wf.Name), Description: omit.From(wf.Description), AutomaticEnabled: omit.From(wf.AutomaticEnabled),
		ManualEnabled: omit.From(wf.ManualEnabled), AutomaticProvider: omit.From(wf.AutomaticProvider), State: omit.From(string(wf.State)),
		BuiltinKey: nullable.String(wf.BuiltinKey), CollectionID: nullable.String(wf.CollectionID), Parameters: omit.From(types.NewJSON(model.JSONMap(wf.Parameters))),
		CurrentVersion: omit.From(int64(wf.CurrentVersion)), CreatedBy: omit.From(wf.CreatedBy), UpdatedBy: omit.From(wf.UpdatedBy),
		DeletedAt: nullable.Time(wf.DeletedAt), Version: omit.From(int64(wf.Version)), Role: nullable.String(wf.Role),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateWorkflow(ctx context.Context, wf workflows.Workflow, from int64) error {
	set := &models.WorkflowSetter{
		UpdatedAt: omit.From(wf.UpdatedAt), UpdatedBy: omit.From(wf.UpdatedBy), Name: omit.From(wf.Name), Description: omit.From(wf.Description),
		AutomaticEnabled: omit.From(wf.AutomaticEnabled), ManualEnabled: omit.From(wf.ManualEnabled), AutomaticProvider: omit.From(wf.AutomaticProvider),
		State: omit.From(string(wf.State)), Parameters: omit.From(types.NewJSON(model.JSONMap(wf.Parameters))),
		CurrentVersion: omit.From(int64(wf.CurrentVersion)), DeletedAt: nullable.Time(wf.DeletedAt), Version: omit.From(int64(wf.Version)),
	}
	w := models.UpdateWhere.Workflows
	n, err := models.Workflows.Update(set.UpdateMod(), w.ID.EQ(wf.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) WorkflowVersions(ctx context.Context, workflowID string) ([]workflows.Version, error) {
	rows, err := models.WorkflowVersions.Query(models.SelectWhere.WorkflowVersions.WorkflowID.EQ(workflowID), sm.OrderBy(models.WorkflowVersions.Columns.Number).Desc()).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]workflows.Version, len(rows))
	for i, row := range rows {
		out[i] = versionRecord(row)
	}
	return out, nil
}

func (q Queries) WorkflowVersion(ctx context.Context, workflowID string, number int64) (workflows.Version, error) {
	w := models.SelectWhere.WorkflowVersions
	row, err := models.WorkflowVersions.Query(w.WorkflowID.EQ(workflowID), w.Number.EQ(number)).One(ctx, q.exec)
	if err != nil {
		return workflows.Version{}, dberr.Translate(err)
	}
	return versionRecord(row), nil
}

func (q Queries) InsertWorkflowVersion(ctx context.Context, v workflows.Version) error {
	_, err := models.WorkflowVersions.Insert(&models.WorkflowVersionSetter{
		ID: omit.From(v.ID), CreatedAt: omit.From(v.CreatedAt), WorkflowID: omit.From(v.WorkflowID), Number: omit.From(int64(v.Number)),
		Definition: omit.From(types.NewJSON(v.Definition)), CreatedBy: omit.From(v.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) WorkflowTriggers(ctx context.Context, f workflows.TriggerFilter) ([]workflows.Trigger, error) {
	query := models.WorkflowTriggers.Query()
	w := models.SelectWhere.WorkflowTriggers
	c := models.WorkflowTriggers.Columns
	if f.Type != "" {
		query.Apply(w.Type.EQ(f.Type))
	}
	if f.WorkflowID != "" {
		query.Apply(w.WorkflowID.EQ(f.WorkflowID))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.MatchCollection {
		query.Apply(sm.Where(psql.Or(c.CollectionID.IsNull(), c.CollectionID.EQ(psql.Arg(f.CollectionID)))))
	}
	if f.DueAt != nil {
		query.Apply(w.NextAt.LTE(f.DueAt.UTC()), sm.OrderBy(c.NextAt))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]workflows.Trigger, len(rows))
	for i, r := range rows {
		out[i] = workflows.Trigger{
			ID: r.ID, WorkflowID: r.WorkflowID, WorkspaceID: r.WorkspaceID, CollectionID: r.CollectionID.GetOrZero(), Type: r.Type,
			Position: int(r.Position), NextAt: nullable.UTC(r.NextAt), CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return out, nil
}

func (q Queries) InsertWorkflowTrigger(ctx context.Context, t workflows.Trigger) error {
	_, err := models.WorkflowTriggers.Insert(&models.WorkflowTriggerSetter{
		ID: omit.From(t.ID), CreatedAt: omit.From(t.CreatedAt), WorkflowID: omit.From(t.WorkflowID), WorkspaceID: omit.From(t.WorkspaceID),
		CollectionID: nullable.String(t.CollectionID), Type: omit.From(t.Type), Position: omit.From(int64(t.Position)), NextAt: nullable.Time(t.NextAt),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteWorkflowTriggers(ctx context.Context, workflowID string) error {
	_, err := models.WorkflowTriggers.Delete(models.DeleteWhere.WorkflowTriggers.WorkflowID.EQ(workflowID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetTriggerNext(ctx context.Context, id string, next time.Time) error {
	set := &models.WorkflowTriggerSetter{NextAt: omitnull.From(next)}
	_, err := models.WorkflowTriggers.Update(set.UpdateMod(), models.UpdateWhere.WorkflowTriggers.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func workflowRecord(r *models.Workflow) workflows.Workflow {
	return workflows.Workflow{
		ID: r.ID, WorkspaceID: r.WorkspaceID, Key: r.Key, Name: r.Name, Description: r.Description, CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy,
		BuiltinKey: r.BuiltinKey.GetOrZero(), CollectionID: r.CollectionID.GetOrZero(), Role: r.Role.GetOrZero(), State: workflows.State(r.State),
		AutomaticEnabled: r.AutomaticEnabled, ManualEnabled: r.ManualEnabled, AutomaticProvider: r.AutomaticProvider, Parameters: r.Parameters.Val,
		CurrentVersion: int(r.CurrentVersion), Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), DeletedAt: nullable.UTC(r.DeletedAt),
	}
}

func versionRecord(r *models.WorkflowVersion) workflows.Version {
	return workflows.Version{ID: r.ID, WorkflowID: r.WorkflowID, CreatedBy: r.CreatedBy, Number: int(r.Number), Definition: r.Definition.Val, CreatedAt: r.CreatedAt.UTC()}
}

func (q Queries) TriggeredWorkflows(ctx context.Context, collectionID string) ([]string, error) {
	return directoryRows(ctx, q.exec, `SELECT DISTINCT v.workflow_id FROM workflow_versions v JOIN workflows w ON w.id = v.workflow_id AND w.current_version = v.number CROSS JOIN jsonb_array_elements(v.definition->'triggers') tr WHERE w.deleted_at IS NULL AND tr->>'collection_id' = $1`,
		func(r scan.Rows) (string, error) { var id string; err := r.Scan(&id); return id, err }, collectionID)
}
