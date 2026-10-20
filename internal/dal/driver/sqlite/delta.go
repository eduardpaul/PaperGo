package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/delta"
	"papergo/internal/dal/driver/sqlite/models"
)

func (q Queries) SyncView(ctx context.Context, subject, containerID string) (delta.View, error) {
	w := models.SelectWhere.SyncViews
	row, err := models.SyncViews.Query(w.Subject.EQ(subject), w.ContainerID.EQ(containerID)).One(ctx, q.exec)
	if err != nil {
		return delta.View{}, dberr.Translate(err)
	}
	return viewRecord(row), nil
}

func (q Queries) SyncViewByID(ctx context.Context, id string) (delta.View, error) {
	row, err := models.FindSyncView(ctx, q.exec, id)
	if err != nil {
		return delta.View{}, dberr.Translate(err)
	}
	return viewRecord(row), nil
}

func (q Queries) InsertSyncView(ctx context.Context, v delta.View) error {
	_, err := models.SyncViews.Insert(&models.SyncViewSetter{
		ID: omit.From(v.ID), CreatedAt: omit.From(v.CreatedAt), Subject: omit.From(v.Subject), ContainerID: omit.From(v.ContainerID), Sequence: omit.From(v.Sequence),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetSyncViewSequence(ctx context.Context, id string, sequence int64) error {
	set := &models.SyncViewSetter{Sequence: omit.From(sequence)}
	_, err := models.SyncViews.Update(set.UpdateMod(), models.UpdateWhere.SyncViews.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SyncStates(ctx context.Context, viewID string, sequence int64) (map[string]string, error) {
	w := models.SelectWhere.SyncStates
	c := models.SyncStates.Columns
	open := sqlite.Or(c.ToSequence.IsNull(), c.ToSequence.GT(sqlite.Arg(sequence)))
	rows, err := models.SyncStates.Query(sm.Columns(c.ItemID, c.Fingerprint), w.ViewID.EQ(viewID), w.FromSequence.LTE(sequence), sm.Where(open)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ItemID] = row.Fingerprint
	}
	return out, nil
}

func (q Queries) EndSyncState(ctx context.Context, viewID, itemID string, sequence int64) error {
	set := &models.SyncStateSetter{ToSequence: omitnull.From(sequence)}
	w := models.UpdateWhere.SyncStates
	_, err := models.SyncStates.Update(set.UpdateMod(), w.ViewID.EQ(viewID), w.ItemID.EQ(itemID), w.ToSequence.IsNull()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) InsertSyncState(ctx context.Context, s delta.State) error {
	_, err := models.SyncStates.Insert(&models.SyncStateSetter{
		ID: omit.From(s.ID), CreatedAt: omit.From(s.CreatedAt), ViewID: omit.From(s.ViewID), ItemID: omit.From(s.ItemID),
		FromSequence: omit.From(s.FromSequence), Fingerprint: omit.From(s.Fingerprint),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) CountSyncStates(ctx context.Context) (int64, error) {
	n, err := models.SyncStates.Query().Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) SyncToken(ctx context.Context, id string) (delta.Token, error) {
	row, err := models.FindSyncToken(ctx, q.exec, id)
	if err != nil {
		return delta.Token{}, dberr.Translate(err)
	}
	return delta.Token{
		ID: row.ID, ViewID: row.ViewID, Sequence: row.Sequence, Changes: row.Changes.Val,
		PageExpiresAt: row.PageExpiresAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(), CreatedAt: row.CreatedAt.UTC(),
	}, nil
}

func (q Queries) InsertSyncToken(ctx context.Context, t delta.Token) error {
	_, err := models.SyncTokens.Insert(&models.SyncTokenSetter{
		ID: omit.From(t.ID), CreatedAt: omit.From(t.CreatedAt), ViewID: omit.From(t.ViewID), Sequence: omit.From(t.Sequence),
		Changes: omit.From(types.NewJSON(t.Changes)), PageExpiresAt: omit.From(t.PageExpiresAt), ExpiresAt: omit.From(t.ExpiresAt),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetSyncTokenExpiry(ctx context.Context, id string, at time.Time) error {
	set := &models.SyncTokenSetter{ExpiresAt: omit.From(at)}
	_, err := models.SyncTokens.Update(set.UpdateMod(), models.UpdateWhere.SyncTokens.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteExpiredSyncTokens(ctx context.Context, now time.Time) error {
	if _, err := models.SyncTokens.Delete(models.DeleteWhere.SyncTokens.ExpiresAt.LTE(now)).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	set := &models.SyncTokenSetter{Changes: omit.From(types.NewJSON(emptyChanges))}
	_, err := models.SyncTokens.Update(set.UpdateMod(), models.UpdateWhere.SyncTokens.PageExpiresAt.LTE(now)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteUnreachableSyncStates(ctx context.Context) error {
	tok, st, view := models.SyncTokens.Columns, models.SyncStates.Columns, models.SyncViews.Columns
	// An ended state stays while a token names a snapshot inside its range.
	named := models.SyncTokens.Query(sm.Columns(sqlite.Raw("1")), sm.Where(tok.ViewID.EQ(st.ViewID)), sm.Where(tok.Sequence.GTE(st.FromSequence)), sm.Where(tok.Sequence.LT(st.ToSequence)))
	if _, err := models.SyncStates.Delete(dm.Where(st.ToSequence.IsNotNull()), dm.Where(sqlite.Raw("NOT EXISTS ?", named))).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	tokenless := models.SyncViews.Query(sm.Columns(view.ID), sm.Where(sqlite.Raw("NOT EXISTS ?", models.SyncTokens.Query(sm.Columns(sqlite.Raw("1")), sm.Where(tok.ViewID.EQ(view.ID))))))
	if _, err := models.SyncStates.Delete(dm.Where(sqlite.Raw(`"view_id" IN ?`, tokenless))).Exec(ctx, q.exec); err != nil {
		return dberr.Translate(err)
	}
	_, err := models.SyncViews.Delete(dm.Where(sqlite.Raw("NOT EXISTS ?", models.SyncTokens.Query(sm.Columns(sqlite.Raw("1")), sm.Where(tok.ViewID.EQ(view.ID)))))).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func viewRecord(r *models.SyncView) delta.View {
	return delta.View{ID: r.ID, Subject: r.Subject, ContainerID: r.ContainerID, Sequence: r.Sequence, CreatedAt: r.CreatedAt.UTC()}
}
