package postgres

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/im"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
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
	if f.After != nil {
		query.Apply(sm.Where(psql.Or(c.CreatedAt.LT(psql.Arg(f.After.CreatedAt)), psql.And(c.CreatedAt.EQ(psql.Arg(f.After.CreatedAt)), c.ID.LT(psql.Arg(f.After.ID))))))
	}
	if !f.Until.IsZero() {
		query.Apply(w.CreatedAt.LTE(f.Until))
	}
	query.Apply(sm.OrderBy(c.CreatedAt).Desc(), sm.OrderBy(c.ID).Desc())
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
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
		ItemID: nullable.String(n.ItemID), TaskID: nullable.String(n.TaskID), RunID: nullable.String(n.RunID), CommentID: nullable.String(n.CommentID),
		Source: omit.From(string(n.Source)), Sender: omit.From(n.Sender), WorkflowID: nullable.String(n.WorkflowID),
		DedupKey: nullable.String(n.DedupKey), Count: omit.From(n.Count),
	}, im.OnConflict().DoNothing()).Exec(ctx, q.exec)
	if err != nil {
		return false, dberr.Translate(err)
	}
	return inserted == 1, nil
}

// CoalesceNotification folds n into the recipient's unread notification
// with its coalesce key, which the partial unique index finds, or inserts
// it.
func (q Queries) CoalesceNotification(ctx context.Context, n notifications.Notification) (string, int64, error) {
	type stored struct {
		ID    string `db:"id"`
		Count int64  `db:"count"`
	}
	out, err := bob.One(ctx, q.exec, psql.RawQuery(`INSERT INTO notifications (id, created_at, recipient, workspace_id, type, title, body, item_id, task_id, run_id, comment_id, source, sender, workflow_id, coalesce_key, "count")
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
ON CONFLICT (recipient, coalesce_key) WHERE coalesce_key IS NOT NULL AND read_at IS NULL
DO UPDATE SET created_at = excluded.created_at, title = excluded.title, body = excluded.body, comment_id = excluded.comment_id, sender = excluded.sender,
	source = excluded.source, workflow_id = excluded.workflow_id, "count" = notifications."count" + 1
RETURNING id, "count"`, n.ID, n.CreatedAt, n.Recipient, orNull(n.WorkspaceID), string(n.Type), n.Title, n.Body, orNull(n.ItemID), orNull(n.TaskID), orNull(n.RunID), orNull(n.CommentID),
		string(n.Source), n.Sender, orNull(n.WorkflowID), n.CoalesceKey), scan.StructMapper[stored]())
	if err != nil {
		return "", 0, dberr.Translate(err)
	}
	return out.ID, out.Count, nil
}

// orNull binds an empty optional identifier as NULL.
func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (q Queries) CountUnread(ctx context.Context, recipient string) (int64, error) {
	w := models.SelectWhere.Notifications
	n, err := models.Notifications.Query(w.Recipient.EQ(recipient), w.ReadAt.IsNull()).Count(ctx, q.exec)
	return n, dberr.Translate(err)
}

func (q Queries) MarkRead(ctx context.Context, recipient string, ids []string, at time.Time) ([]string, error) {
	w := models.SelectWhere.Notifications
	rows, err := models.Notifications.Query(w.Recipient.EQ(recipient), w.ReadAt.IsNull(), w.ID.In(ids...)).All(ctx, q.exec)
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

func (q Queries) MarkAllRead(ctx context.Context, recipient string, before, at time.Time) (int64, error) {
	u := models.UpdateWhere.Notifications
	set := &models.NotificationSetter{ReadAt: omitnull.From(at)}
	n, err := models.Notifications.Update(set.UpdateMod(), u.Recipient.EQ(recipient), u.ReadAt.IsNull(), u.CreatedAt.LTE(before)).Exec(ctx, q.exec)
	return n, dberr.Translate(err)
}

// LinkNotificationComment records that a notification tells of a comment.
func (q Queries) LinkNotificationComment(ctx context.Context, notificationID, commentID string) error {
	_, err := models.NotificationComments.Insert(&models.NotificationCommentSetter{
		NotificationID: omit.From(notificationID), CommentID: omit.From(commentID),
	}, im.OnConflict().DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UnlinkNotificationComment(ctx context.Context, notificationID, commentID string) error {
	w := models.DeleteWhere.NotificationComments
	_, err := models.NotificationComments.Delete(w.NotificationID.EQ(notificationID), w.CommentID.EQ(commentID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// NotificationComments returns the comments a notification tells of.
func (q Queries) NotificationComments(ctx context.Context, notificationID string) ([]string, error) {
	rows, err := models.NotificationComments.Query(models.SelectWhere.NotificationComments.NotificationID.EQ(notificationID)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.CommentID
	}
	return out, nil
}

// CommentNotifications returns the notifications that tell of a comment.
func (q Queries) CommentNotifications(ctx context.Context, commentID string) ([]notifications.Notification, error) {
	links, err := models.NotificationComments.Query(models.SelectWhere.NotificationComments.CommentID.EQ(commentID)).All(ctx, q.exec)
	if err != nil || len(links) == 0 {
		return nil, dberr.Translate(err)
	}
	ids := make([]string, len(links))
	for i, link := range links {
		ids[i] = link.NotificationID
	}
	rows, err := models.Notifications.Query(models.SelectWhere.Notifications.ID.In(ids...)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]notifications.Notification, len(rows))
	for i, row := range rows {
		out[i] = notificationRecord(row)
	}
	return out, nil
}

// ReviseNotification stores what n says and links: its title, text,
// comment, sender, source, workflow and count.
func (q Queries) ReviseNotification(ctx context.Context, n notifications.Notification) error {
	set := &models.NotificationSetter{
		Title: omit.From(n.Title), Body: omit.From(n.Body), CommentID: nullable.String(n.CommentID),
		Sender: omit.From(n.Sender), Source: omit.From(string(n.Source)), WorkflowID: nullable.String(n.WorkflowID), Count: omit.From(n.Count),
	}
	_, err := models.Notifications.Update(set.UpdateMod(), models.UpdateWhere.Notifications.ID.EQ(n.ID)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteNotification(ctx context.Context, recipient, id string) (bool, error) {
	w := models.DeleteWhere.Notifications
	n, err := models.Notifications.Delete(w.ID.EQ(id), w.Recipient.EQ(recipient)).Exec(ctx, q.exec)
	return n == 1, dberr.Translate(err)
}

func notificationRecord(r *models.Notification) notifications.Notification {
	return notifications.Notification{
		ID: r.ID, Recipient: r.Recipient, WorkspaceID: r.WorkspaceID.GetOrZero(), Type: notifications.Type(r.Type), Title: r.Title, Body: r.Body,
		ItemID: r.ItemID.GetOrZero(), TaskID: r.TaskID.GetOrZero(), RunID: r.RunID.GetOrZero(), CommentID: r.CommentID.GetOrZero(),
		CoalesceKey: r.CoalesceKey.GetOrZero(), Count: r.Count,
		Source: notifications.Source(r.Source), Sender: r.Sender, WorkflowID: r.WorkflowID.GetOrZero(), DedupKey: r.DedupKey.GetOrZero(),
		CreatedAt: r.CreatedAt.UTC(), ReadAt: nullable.UTC(r.ReadAt),
	}
}
