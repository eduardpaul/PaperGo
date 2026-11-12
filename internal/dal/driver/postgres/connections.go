package postgres

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/connections"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"
)

func (q Queries) FindConnection(ctx context.Context, id string) (connections.Connection, error) {
	row, err := models.FindConnection(ctx, q.exec, id)
	if err != nil {
		return connections.Connection{}, dberr.Translate(err)
	}
	return connectionRecord(row), nil
}

func (q Queries) Connections(ctx context.Context, f connections.Filter) ([]connections.Connection, error) {
	query := models.Connections.Query(sm.OrderBy(models.Connections.Columns.Name))
	w := models.SelectWhere.Connections
	if f.WorkspaceIDs != nil {
		query.Apply(w.WorkspaceID.In(f.WorkspaceIDs...))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.Kind != "" {
		query.Apply(w.Kind.EQ(string(f.Kind)))
	}
	if f.Enabled {
		query.Apply(w.Enabled.EQ(true))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]connections.Connection, len(rows))
	for i, row := range rows {
		out[i] = connectionRecord(row)
	}
	return out, nil
}

func (q Queries) InsertConnection(ctx context.Context, c connections.Connection) error {
	_, err := models.Connections.Insert(&models.ConnectionSetter{
		ID: omit.From(c.ID), CreatedAt: omit.From(c.CreatedAt), UpdatedAt: omit.From(c.UpdatedAt), WorkspaceID: omit.From(c.WorkspaceID),
		Key: omit.From(c.Key), Name: omit.From(c.Name), Kind: omit.From(string(c.Kind)), Enabled: omit.From(c.Enabled),
		ConfigCiphertext: omit.From(c.ConfigCiphertext), ConfigHash: omit.From(c.ConfigHash),
		Settings: omit.From(types.NewJSON(c.Settings)), Channels: omit.From(types.NewJSON(c.Channels)),
		CreatedBy: omit.From(c.CreatedBy), UpdatedBy: omit.From(c.UpdatedBy), Version: omit.From(int64(c.Version)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateConnection(ctx context.Context, c connections.Connection, from int64) error {
	set := &models.ConnectionSetter{
		UpdatedAt: omit.From(c.UpdatedAt), UpdatedBy: omit.From(c.UpdatedBy), Name: omit.From(c.Name), Enabled: omit.From(c.Enabled),
		ConfigCiphertext: omit.From(c.ConfigCiphertext), ConfigHash: omit.From(c.ConfigHash),
		Settings: omit.From(types.NewJSON(c.Settings)), Channels: omit.From(types.NewJSON(c.Channels)), Version: omit.From(int64(c.Version)),
	}
	w := models.UpdateWhere.Connections
	n, err := models.Connections.Update(set.UpdateMod(), w.ID.EQ(c.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) DeleteConnection(ctx context.Context, id string, from int64) error {
	w := models.DeleteWhere.Connections
	n, err := models.Connections.Delete(w.ID.EQ(id), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func connectionRecord(r *models.Connection) connections.Connection {
	return connections.Connection{
		ID: r.ID, WorkspaceID: r.WorkspaceID, Key: r.Key, Name: r.Name, Kind: connections.Kind(r.Kind), Enabled: r.Enabled,
		ConfigCiphertext: r.ConfigCiphertext, ConfigHash: r.ConfigHash, Settings: r.Settings.Val, Channels: r.Channels.Val,
		CreatedBy: r.CreatedBy, UpdatedBy: r.UpdatedBy, Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}
