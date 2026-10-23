package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/notifications"
)

func (q Queries) Notification(ctx context.Context, id string) (notifications.Notification, error) {
	row, err := models.FindNotification(ctx, q.exec, id)
	if err != nil {
		return notifications.Notification{}, dberr.Translate(err)
	}
	return notificationRecord(row), nil
}

func (q Queries) Notifications(ctx context.Context, f notifications.Filter) ([]notifications.Notification, error) {
	w := models.SelectWhere.Notifications
	c := models.Notifications.Columns
	query := models.Notifications.Query(w.Recipient.EQ(f.Recipient))
	if f.Unread {
		query.Apply(w.ReadAt.IsNull())
	}
	if f.Type != "" {
		query.Apply(w.Type.EQ(string(f.Type)))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	query.Apply(sm.OrderBy(c.CreatedAt).Desc(), sm.OrderBy(c.ID).Desc())
	// SQLite accepts OFFSET only after LIMIT.
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit), sm.Offset(max(f.Offset, 0)))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]notifications.Notification, len(rows))
	for i, row := range rows {
		out[i] = notificationRecord(row)
	}
	return out, nil
}

// InsertNotification skips a notification whose dedup key the recipient
// already has: the unique index makes the insert do nothing.
func (q Queries) InsertNotification(ctx context.Context, n notifications.Notification) (bool, error) {
	inserted, err := models.Notifications.Insert(&models.NotificationSetter{
		ID: omit.From(n.ID), CreatedAt: omit.From(n.CreatedAt), Recipient: omit.From(n.Recipient), WorkspaceID: nullable.String(n.WorkspaceID),
		Type: omit.From(string(n.Type)), Title: omit.From(n.Title), Body: omit.From(n.Body),
		ItemID: nullable.String(n.ItemID), TaskID: nullable.String(n.TaskID), RunID: nullable.String(n.RunID),
		Source: omit.From(string(n.Source)), Sender: omit.From(n.Sender), WorkflowID: nullable.String(n.WorkflowID),
		DedupKey: nullable.String(n.DedupKey),
	}, im.OnConflict().DoNothing()).Exec(ctx, q.exec)
	if err != nil {
		return false, dberr.Translate(err)
	}
	return inserted == 1, nil
}

func (q Queries) CountUnread(ctx context.Context, recipient string) (int64, error) {
	w := models.SelectWhere.Notifications
	n, err := models.Notifications.Query(w.Recipient.EQ(recipient), w.ReadAt.IsNull()).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) MarkRead(ctx context.Context, recipient string, ids []string, before, at time.Time) ([]string, error) {
	w := models.SelectWhere.Notifications
	query := models.Notifications.Query(w.Recipient.EQ(recipient), w.ReadAt.IsNull())
	if len(ids) > 0 {
		query.Apply(w.ID.In(ids...))
	} else {
		query.Apply(w.CreatedAt.LTE(before))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil || len(rows) == 0 {
		return nil, dberr.Translate(err)
	}
	marked := make([]string, len(rows))
	for i, row := range rows {
		marked[i] = row.ID
	}
	u := models.UpdateWhere.Notifications
	set := &models.NotificationSetter{ReadAt: omitnull.From(at)}
	_, err = models.Notifications.Update(set.UpdateMod(), u.ID.In(marked...)).Exec(ctx, q.exec)
	return marked, dberr.Translate(err)
}

func (q Queries) DeleteNotification(ctx context.Context, recipient, id string) (bool, error) {
	w := models.DeleteWhere.Notifications
	n, err := models.Notifications.Delete(w.ID.EQ(id), w.Recipient.EQ(recipient)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func notificationRecord(r *models.Notification) notifications.Notification {
	return notifications.Notification{
		ID: r.ID, Recipient: r.Recipient, WorkspaceID: r.WorkspaceID.GetOrZero(), Type: notifications.Type(r.Type), Title: r.Title, Body: r.Body,
		ItemID: r.ItemID.GetOrZero(), TaskID: r.TaskID.GetOrZero(), RunID: r.RunID.GetOrZero(),
		Source: notifications.Source(r.Source), Sender: r.Sender, WorkflowID: r.WorkflowID.GetOrZero(), DedupKey: r.DedupKey.GetOrZero(),
		CreatedAt: r.CreatedAt.UTC(), ReadAt: nullable.UTC(r.ReadAt),
	}
}
