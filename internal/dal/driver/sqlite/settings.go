package sqlite

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/settings"
)

func (q Queries) Settings(ctx context.Context, key settings.Key, resourceIDs []string, organization bool) ([]settings.Setting, error) {
	c := models.Settings.Columns
	var match []bob.Expression
	if len(resourceIDs) > 0 {
		match = append(match, c.ResourceID.In(sqlite.Arg(anys(resourceIDs)...)))
	}
	if organization {
		match = append(match, c.ResourceID.IsNull())
	}
	if len(match) == 0 {
		return nil, nil
	}
	rows, err := models.Settings.Query(models.SelectWhere.Settings.Key.EQ(string(key)), sm.Where(sqlite.Or(match...))).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]settings.Setting, len(rows))
	for i, r := range rows {
		out[i] = settings.Setting{ID: r.ID, ResourceID: r.ResourceID.GetOrZero(), Key: settings.Key(r.Key), Value: r.Value.Val, Version: r.Version, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) InsertSetting(ctx context.Context, s settings.Setting) error {
	_, err := models.Settings.Insert(&models.SettingSetter{
		ID: omit.From(s.ID), CreatedAt: omit.From(s.UpdatedAt), UpdatedAt: omit.From(s.UpdatedAt), ResourceID: nullable.String(s.ResourceID),
		Key: omit.From(string(s.Key)), Value: omit.From(types.NewJSON(s.Value)), Version: omit.From(s.Version), UpdatedBy: omit.From(s.UpdatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateSetting(ctx context.Context, s settings.Setting, from int64) error {
	set := &models.SettingSetter{Value: omit.From(types.NewJSON(s.Value)), Version: omit.From(s.Version), UpdatedBy: omit.From(s.UpdatedBy), UpdatedAt: omit.From(s.UpdatedAt)}
	w := models.UpdateWhere.Settings
	n, err := models.Settings.Update(set.UpdateMod(), w.ID.EQ(s.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}
