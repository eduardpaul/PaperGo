package postgres

import (
	"context"
	"encoding/json"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/apps"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/model"
)

func (q Queries) FindApp(ctx context.Context, id string) (apps.App, error) {
	row, err := models.FindApp(ctx, q.exec, id)
	if err != nil {
		return apps.App{}, dberr.Translate(err)
	}
	return appRecord(row), nil
}

func (q Queries) Apps(ctx context.Context, f apps.Filter) ([]apps.App, error) {
	query := models.Apps.Query(sm.OrderBy(models.Apps.Columns.Name))
	w := models.SelectWhere.Apps
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.State != "" {
		query.Apply(w.State.EQ(string(f.State)))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]apps.App, len(rows))
	for i, row := range rows {
		out[i] = appRecord(row)
	}
	return out, nil
}

func (q Queries) InsertApp(ctx context.Context, a apps.App) error {
	_, err := models.Apps.Insert(&models.AppSetter{
		ID: omit.From(a.ID), CreatedAt: omit.From(a.CreatedAt), UpdatedAt: omit.From(a.UpdatedAt), WorkspaceID: omit.From(a.WorkspaceID),
		Key: omit.From(a.Key), Name: omit.From(a.Name), Description: omit.From(a.Description), State: omit.From(string(a.State)),
		Parameters: omit.From(types.NewJSON(model.JSONMap(a.Parameters))), CurrentVersion: omit.From(int64(a.CurrentVersion)),
		CreatedBy: omit.From(a.CreatedBy), UpdatedBy: omit.From(a.UpdatedBy), Version: omit.From(int64(a.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateApp(ctx context.Context, a apps.App, from int64) error {
	set := &models.AppSetter{
		UpdatedAt: omit.From(a.UpdatedAt), UpdatedBy: omit.From(a.UpdatedBy), Name: omit.From(a.Name), Description: omit.From(a.Description),
		State: omit.From(string(a.State)), Parameters: omit.From(types.NewJSON(model.JSONMap(a.Parameters))),
		CurrentVersion: omit.From(int64(a.CurrentVersion)), Version: omit.From(int64(a.Version)),
	}
	w := models.UpdateWhere.Apps
	n, err := models.Apps.Update(set.UpdateMod(), w.ID.EQ(a.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) DeleteApp(ctx context.Context, id string, from int64) error {
	w := models.DeleteWhere.Apps
	n, err := models.Apps.Delete(w.ID.EQ(id), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) AppVersions(ctx context.Context, appID string) ([]apps.Version, error) {
	rows, err := models.AppVersions.Query(models.SelectWhere.AppVersions.AppID.EQ(appID), sm.OrderBy(models.AppVersions.Columns.Number).Desc()).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]apps.Version, len(rows))
	for i, row := range rows {
		out[i] = appVersionRecord(row)
	}
	return out, nil
}

func (q Queries) AppVersion(ctx context.Context, appID string, number int64) (apps.Version, error) {
	w := models.SelectWhere.AppVersions
	row, err := models.AppVersions.Query(w.AppID.EQ(appID), w.Number.EQ(number)).One(ctx, q.exec)
	if err != nil {
		return apps.Version{}, dberr.Translate(err)
	}
	return appVersionRecord(row), nil
}

func (q Queries) AppVersionSchema(ctx context.Context, appID string, number int64) (json.RawMessage, error) {
	w := models.SelectWhere.AppVersions
	row, err := models.AppVersions.Query(sm.Columns(models.AppVersions.Columns.ParametersSchema), w.AppID.EQ(appID), w.Number.EQ(number)).One(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	return row.ParametersSchema.Val, nil
}

func (q Queries) InsertAppVersion(ctx context.Context, v apps.Version) error {
	_, err := models.AppVersions.Insert(&models.AppVersionSetter{
		ID: omit.From(v.ID), CreatedAt: omit.From(v.CreatedAt), AppID: omit.From(v.AppID), Number: omit.From(int64(v.Number)),
		Source: omit.From(v.Source), Bundle: omit.From(v.Bundle), BundleHash: omit.From(v.BundleHash),
		ParametersSchema: omit.From(types.NewJSON(v.ParametersSchema)), CreatedBy: omit.From(v.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func appRecord(r *models.App) apps.App {
	return apps.App{
		ID: r.ID, WorkspaceID: r.WorkspaceID, Key: r.Key, Name: r.Name, Description: r.Description, CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy,
		State: apps.State(r.State), Parameters: r.Parameters.Val, CurrentVersion: int(r.CurrentVersion), Version: int(r.Version),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func appVersionRecord(r *models.AppVersion) apps.Version {
	return apps.Version{ID: r.ID, AppID: r.AppID, CreatedBy: r.CreatedBy, Number: int(r.Number), CreatedAt: r.CreatedAt.UTC(),
		Content: apps.Content{Source: r.Source, Bundle: r.Bundle, BundleHash: r.BundleHash, ParametersSchema: r.ParametersSchema.Val}}
}
