package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/dialect/sqlite/um"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/views"
)

func (q Queries) ListView(ctx context.Context, id string) (views.ListView, error) {
	row, err := models.FindListView(ctx, q.exec, id)
	if err != nil {
		return views.ListView{}, dberr.Translate(err)
	}
	return listViewRecord(row), nil
}

func (q Queries) ListViews(ctx context.Context, f views.ViewFilter) ([]views.ListView, error) {
	c := models.ListViews.Columns
	query := models.ListViews.Query(sm.OrderBy(c.ID))
	if f.ContainerID != "" {
		query.Apply(sm.Where(c.ContainerID.EQ(sqlite.Arg(f.ContainerID))))
	}
	if f.AfterID != "" {
		query.Apply(sm.Where(c.ID.GT(sqlite.Arg(f.AfterID))))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]views.ListView, len(rows))
	for i, row := range rows {
		out[i] = listViewRecord(row)
	}
	return out, nil
}

func (q Queries) InsertListView(ctx context.Context, v views.ListView) error {
	_, err := models.ListViews.Insert(&models.ListViewSetter{
		SystemKey: omit.From(string(v.SystemKey)), ID: omit.From(v.ID), CreatedAt: omit.From(v.CreatedAt), UpdatedAt: omit.From(v.UpdatedAt), ContainerID: omit.From(v.ContainerID),
		Name: omit.From(v.Name), Columns: omit.From(types.NewJSON(v.Columns)), Query: omit.From(types.NewJSON(v.Query)),
		Layout: omit.From(string(v.Layout)), IsDefault: omit.From(v.IsDefault), Version: omit.From(int64(v.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateListView(ctx context.Context, v views.ListView) (bool, error) {
	set := &models.ListViewSetter{
		Name: omit.From(v.Name), Columns: omit.From(types.NewJSON(v.Columns)), Query: omit.From(types.NewJSON(v.Query)), Layout: omit.From(string(v.Layout)),
		IsDefault: omit.From(v.IsDefault), UpdatedAt: omit.From(v.UpdatedAt), Version: omit.From(int64(v.Version)),
	}
	w := models.UpdateWhere.ListViews
	n, err := models.ListViews.Update(set.UpdateMod(), w.ID.EQ(v.ID), w.Version.EQ(int64(v.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteListView(ctx context.Context, id string) error {
	_, err := models.ListViews.Delete(models.DeleteWhere.ListViews.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) ClearDefaultView(ctx context.Context, containerID, keep string, at time.Time) error {
	w := models.UpdateWhere.ListViews
	_, err := models.ListViews.Update(um.SetCol("is_default").ToArg(false), um.SetCol("updated_at").ToArg(at),
		um.SetCol("version").To(models.ListViews.Columns.Version.OP("+", sqlite.Arg(1))),
		w.ContainerID.EQ(containerID), w.IsDefault.EQ(true), w.ID.NE(keep)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SmartFolder(ctx context.Context, id string) (views.SmartFolder, error) {
	row, err := models.FindSmartFolder(ctx, q.exec, id)
	if err != nil {
		return views.SmartFolder{}, dberr.Translate(err)
	}
	return smartFolderRecord(row), nil
}

func smartFolderQuery(f views.FolderFilter) *sqlite.ViewQuery[*models.SmartFolder, models.SmartFolderSlice] {
	c := models.SmartFolders.Columns
	query := models.SmartFolders.Query()
	if f.WorkspaceID != "" {
		query.Apply(sm.Where(c.WorkspaceID.EQ(sqlite.Arg(f.WorkspaceID))))
	}
	if f.Shared {
		query.Apply(sm.Where(c.OwnerID.IsNull()))
	}
	if f.Reader != nil {
		readable, args := access.SQLite.Resource(`"smart_folders"."workspace_id"`, *f.Reader, access.Read)
		query.Apply(sm.Where(sqlite.Raw(`("smart_folders"."owner_id" = ? OR ("smart_folders"."owner_id" IS NULL AND `+readable+`))`, append([]any{f.Reader.Subject}, args...)...)))
	}
	if f.AfterID != "" {
		query.Apply(sm.Where(c.ID.GT(sqlite.Arg(f.AfterID))))
	}
	return query
}

func (q Queries) SmartFolders(ctx context.Context, f views.FolderFilter) ([]views.SmartFolder, error) {
	c := models.SmartFolders.Columns
	query := smartFolderQuery(f)
	if f.Order == views.FoldersByName {
		query.Apply(sm.OrderBy(c.Name))
	}
	query.Apply(sm.OrderBy(c.ID))
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]views.SmartFolder, len(rows))
	for i, row := range rows {
		out[i] = smartFolderRecord(row)
	}
	return out, nil
}

func (q Queries) CountSmartFolders(ctx context.Context, f views.FolderFilter) (int, error) {
	n, err := smartFolderQuery(f).Count(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) InsertSmartFolder(ctx context.Context, f views.SmartFolder) error {
	_, err := models.SmartFolders.Insert(&models.SmartFolderSetter{
		ID: omit.From(f.ID), CreatedAt: omit.From(f.CreatedAt), UpdatedAt: omit.From(f.UpdatedAt), WorkspaceID: nullable.String(deref(f.WorkspaceID)),
		OwnerID: nullable.String(deref(f.OwnerID)), Name: omit.From(f.Name), Description: omit.From(f.Description), Definition: omit.From(types.NewJSON(f.Definition)),
		CreatedBy: omit.From(f.CreatedBy), UpdatedBy: omit.From(f.UpdatedBy), Version: omit.From(int64(f.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateSmartFolder(ctx context.Context, f views.SmartFolder) (bool, error) {
	set := &models.SmartFolderSetter{
		Name: omit.From(f.Name), Description: omit.From(f.Description), Definition: omit.From(types.NewJSON(f.Definition)),
		UpdatedBy: omit.From(f.UpdatedBy), UpdatedAt: omit.From(f.UpdatedAt), Version: omit.From(int64(f.Version)),
	}
	w := models.UpdateWhere.SmartFolders
	n, err := models.SmartFolders.Update(set.UpdateMod(), w.ID.EQ(f.ID), w.Version.EQ(int64(f.Version-1))).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func (q Queries) DeleteSmartFolder(ctx context.Context, id string) error {
	_, err := models.SmartFolders.Delete(models.DeleteWhere.SmartFolders.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func listViewRecord(r *models.ListView) views.ListView {
	return views.ListView{
		SystemKey: views.SystemKey(r.SystemKey), ID: r.ID, CreatedAt: r.CreatedAt.UTC(), ContainerID: r.ContainerID, Name: r.Name, Columns: r.Columns.Val, Query: r.Query.Val,
		Layout: views.Layout(r.Layout), IsDefault: r.IsDefault, Version: int(r.Version), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

func smartFolderRecord(r *models.SmartFolder) views.SmartFolder {
	return views.SmartFolder{
		ID: r.ID, CreatedAt: r.CreatedAt.UTC(), WorkspaceID: r.WorkspaceID.Ptr(), OwnerID: r.OwnerID.Ptr(), Name: r.Name, Description: r.Description,
		Definition: r.Definition.Val, CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC(), Version: int(r.Version),
	}
}
