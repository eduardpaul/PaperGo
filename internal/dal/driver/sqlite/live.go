package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/live"
)

func (q Queries) InsertLiveChange(ctx context.Context, c live.Change) error {
	_, err := models.LiveChanges.Insert(&models.LiveChangeSetter{
		CreatedAt: omit.From(c.CreatedAt), Kind: omit.From(string(c.Kind)), EntityID: omit.From(c.EntityID), Action: omit.From(string(c.Action)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) LiveChanges(ctx context.Context, after int64, limit int) ([]live.Change, error) {
	rows, err := models.LiveChanges.Query(models.SelectWhere.LiveChanges.ID.GT(after), sm.OrderBy(models.LiveChanges.Columns.ID), sm.Limit(limit)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]live.Change, len(rows))
	for i, r := range rows {
		out[i] = live.Change{ID: r.ID, Kind: live.Kind(r.Kind), EntityID: r.EntityID, Action: live.Action(r.Action), CreatedAt: r.CreatedAt.UTC()}
	}
	return out, nil
}

// LiveBounds reads the bounds in one statement, so both come from one
// snapshot. sqlite_sequence keeps the highest ID AUTOINCREMENT assigned.
func (q Queries) LiveBounds(ctx context.Context) (int64, int64, error) {
	type bounds struct{ Earliest, Latest int64 }
	b, err := bob.One(ctx, q.exec, sqlite.RawQuery(`SELECT coalesce((SELECT min(id) FROM live_changes), 0) AS earliest, coalesce((SELECT seq FROM sqlite_sequence WHERE name = 'live_changes'), 0) AS latest`), scan.StructMapper[bounds]())
	return b.Earliest, b.Latest, dberr.Translate(err)
}

func (q Queries) CountLiveChanges(ctx context.Context, through time.Time) (int64, error) {
	n, err := models.LiveChanges.Query(models.SelectWhere.LiveChanges.CreatedAt.LTE(through)).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) DeleteLiveChanges(ctx context.Context, through time.Time, limit int) (int64, error) {
	page := models.LiveChanges.Query(sm.Columns(models.LiveChanges.Columns.ID), models.SelectWhere.LiveChanges.CreatedAt.LTE(through), sm.OrderBy(models.LiveChanges.Columns.ID), sm.Limit(limit))
	n, err := models.LiveChanges.Delete(dm.Where(sqlite.Raw(`"id" IN ?`, page))).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}
