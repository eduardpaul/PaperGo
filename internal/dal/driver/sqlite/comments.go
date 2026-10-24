package sqlite

import (
	"context"
	"time"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/comments"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
)

func (q Queries) Comment(ctx context.Context, id string) (comments.Comment, error) {
	row, err := models.FindComment(ctx, q.exec, id)
	if err != nil {
		return comments.Comment{}, dberr.Translate(err)
	}
	return commentRecord(row), nil
}

func (q Queries) Roots(ctx context.Context, f comments.Threads) ([]comments.Comment, error) {
	c := models.Comments.Columns
	query := models.Comments.Query(models.SelectWhere.Comments.ItemID.EQ(f.ItemID), sm.Where(c.ThreadID.EQ(c.ID)))
	if f.Resolved != nil {
		if *f.Resolved {
			query.Apply(sm.Where(c.ResolvedAt.IsNotNull()))
		} else {
			query.Apply(sm.Where(c.ResolvedAt.IsNull()))
		}
	}
	if f.AnchorKind != "" {
		query.Apply(sm.Where(sqlite.Raw(`json_extract("comments"."anchor", '$.kind') = ?`, f.AnchorKind)))
	}
	// A deleted root stays while its thread has replies.
	query.Apply(sm.Where(sqlite.Or(c.DeletedAt.IsNull(), sqlite.Raw(`EXISTS (SELECT 1 FROM comments r WHERE r.thread_id = "comments"."id" AND r.id <> r.thread_id AND r.deleted_at IS NULL)`))))
	if f.After != nil {
		query.Apply(sm.Where(sqlite.Or(c.CreatedAt.GT(sqlite.Arg(f.After.CreatedAt)), sqlite.And(c.CreatedAt.EQ(sqlite.Arg(f.After.CreatedAt)), c.ID.GT(sqlite.Arg(f.After.ID))))))
	}
	query.Apply(sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	return commentRows(query.All(ctx, q.exec))
}

func (q Queries) Replies(ctx context.Context, threadIDs []string) ([]comments.Comment, error) {
	c := models.Comments.Columns
	return commentRows(models.Comments.Query(models.SelectWhere.Comments.ThreadID.In(threadIDs...), sm.Where(c.ThreadID.NE(c.ID)),
		sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID)).All(ctx, q.exec))
}

func (q Queries) CountReplies(ctx context.Context, threadID string) (int, error) {
	c := models.Comments.Columns
	n, err := models.Comments.Query(models.SelectWhere.Comments.ThreadID.EQ(threadID), sm.Where(c.ThreadID.NE(c.ID))).Count(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) InsertComment(ctx context.Context, c comments.Comment) error {
	_, err := models.Comments.Insert(&models.CommentSetter{
		ID: omit.From(c.ID), CreatedAt: omit.From(c.CreatedAt), UpdatedAt: omit.From(c.UpdatedAt), Version: omit.From(c.Version),
		ItemID: omit.From(c.ItemID), ThreadID: omit.From(c.ThreadID), Author: omit.From(c.Author), Source: omit.From(string(c.Source)),
		WorkflowID: nullable.String(c.WorkflowID), Body: omit.From(c.Body), Anchor: nullable.JSON(c.Anchor),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateComment(ctx context.Context, c comments.Comment, from int64) error {
	set := &models.CommentSetter{
		UpdatedAt: omit.From(c.UpdatedAt), Version: omit.From(c.Version), Body: omit.From(c.Body), EditedAt: nullable.Time(c.EditedAt),
		DeletedAt: nullable.Time(c.DeletedAt), DeletedBy: nullable.String(c.DeletedBy), ResolvedAt: nullable.Time(c.ResolvedAt), ResolvedBy: nullable.String(c.ResolvedBy),
	}
	w := models.UpdateWhere.Comments
	n, err := models.Comments.Update(set.UpdateMod(), w.ID.EQ(c.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) Mentions(ctx context.Context, commentIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(commentIDs) == 0 {
		return out, nil
	}
	c := models.CommentMentions.Columns
	rows, err := models.CommentMentions.Query(models.SelectWhere.CommentMentions.CommentID.In(commentIDs...), sm.OrderBy(c.Subject)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	for _, r := range rows {
		out[r.CommentID] = append(out[r.CommentID], r.Subject)
	}
	return out, nil
}

func (q Queries) AddMentions(ctx context.Context, commentID string, subjects []string, at time.Time) error {
	for _, s := range subjects {
		if _, err := models.CommentMentions.Insert(&models.CommentMentionSetter{CommentID: omit.From(commentID), Subject: omit.From(s), CreatedAt: omit.From(at)},
			im.OnConflict().DoNothing()).Exec(ctx, q.exec); err != nil {
			return dberr.Translate(err)
		}
	}
	return nil
}

func (q Queries) RemoveMentions(ctx context.Context, commentID string, subjects []string) error {
	if len(subjects) == 0 {
		return nil
	}
	w := models.DeleteWhere.CommentMentions
	_, err := models.CommentMentions.Delete(w.CommentID.EQ(commentID), w.Subject.In(subjects...)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

// CountThreads counts each item's open and resolved threads: roots that
// are live, or deleted with live replies.
func (q Queries) CountThreads(ctx context.Context, itemIDs []string) (map[string]comments.Count, error) {
	type row struct {
		ItemID   string `db:"item_id"`
		Open     int    `db:"open"`
		Resolved int    `db:"resolved"`
	}
	rows, err := bob.All(ctx, q.exec, sqlite.RawQuery(`SELECT item_id, SUM(CASE WHEN resolved_at IS NULL THEN 1 ELSE 0 END) AS open, SUM(CASE WHEN resolved_at IS NULL THEN 0 ELSE 1 END) AS resolved FROM comments c
WHERE item_id IN (`+marks(len(itemIDs))+`) AND thread_id = id
AND (deleted_at IS NULL OR EXISTS (SELECT 1 FROM comments r WHERE r.thread_id = c.id AND r.id <> r.thread_id AND r.deleted_at IS NULL))
GROUP BY item_id`, anys(itemIDs)...), scan.StructMapper[row]())
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := map[string]comments.Count{}
	for _, r := range rows {
		out[r.ItemID] = comments.Count{Open: r.Open, Resolved: r.Resolved}
	}
	return out, nil
}

func commentRows(rows models.CommentSlice, err error) ([]comments.Comment, error) {
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]comments.Comment, len(rows))
	for i, r := range rows {
		out[i] = commentRecord(r)
	}
	return out, nil
}

func commentRecord(r *models.Comment) comments.Comment {
	return comments.Comment{
		ID: r.ID, ItemID: r.ItemID, ThreadID: r.ThreadID, Author: r.Author, Source: comments.Source(r.Source), WorkflowID: r.WorkflowID.GetOrZero(), Body: r.Body,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), EditedAt: nullable.UTC(r.EditedAt), DeletedAt: nullable.UTC(r.DeletedAt), DeletedBy: r.DeletedBy.GetOrZero(),
		ResolvedAt: nullable.UTC(r.ResolvedAt), ResolvedBy: r.ResolvedBy.GetOrZero(), Version: r.Version, Anchor: r.Anchor.GetOrZero().Val,
	}
}
