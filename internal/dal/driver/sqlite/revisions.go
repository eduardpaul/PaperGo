package sqlite

import (
	"context"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/revisions"
)

func (q Queries) Revision(ctx context.Context, id string) (revisions.Revision, error) {
	row, err := models.FindItemRevision(ctx, q.exec, id)
	if err != nil {
		return revisions.Revision{}, dberr.Translate(err)
	}
	return revisionRecord(row), nil
}

func (q Queries) Revisions(ctx context.Context, f revisions.Filter) ([]revisions.Revision, error) {
	c := models.ItemRevisions.Columns
	query := models.ItemRevisions.Query(sm.OrderBy(c.ItemID), sm.OrderBy(c.RevisionNumber))
	if f.IDs != nil {
		query.Apply(sm.Where(c.ID.In(sqlite.Arg(anys(f.IDs)...))))
	}
	if f.BlobIDs != nil {
		query.Apply(sm.Where(c.BlobID.In(sqlite.Arg(anys(f.BlobIDs)...))))
	}
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(sqlite.Arg(f.ItemID))))
	}
	if f.Number > 0 {
		query.Apply(sm.Where(c.RevisionNumber.EQ(sqlite.Arg(f.Number))))
	}
	if f.HistoryNumber > 0 {
		h := models.ItemHistories.Columns
		recorded := models.ItemHistories.Query(sm.Columns(h.RevisionID), sm.Where(h.Number.EQ(sqlite.Arg(f.HistoryNumber))))
		if f.ItemID != "" {
			recorded.Apply(sm.Where(h.ItemID.EQ(sqlite.Arg(f.ItemID))))
		}
		query.Apply(sm.Where(sqlite.Raw(`"item_revisions"."id" IN ?`, recorded)))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]revisions.Revision, len(rows))
	for i, row := range rows {
		out[i] = revisionRecord(row)
	}
	return out, nil
}

func (q Queries) InsertRevision(ctx context.Context, r revisions.Revision) error {
	_, err := models.ItemRevisions.Insert(&models.ItemRevisionSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), ItemID: omit.From(r.ItemID), ContainerID: omit.From(r.ContainerID),
		SchemaRevisionID: omit.From(r.SchemaRevisionID), ContentTypeID: omit.From(r.ContentTypeID), RevisionNumber: omit.From(int64(r.RevisionNumber)),
		Name: omit.From(r.Name), Keywords: omit.From(types.NewJSON(r.Keywords)), Payload: omit.From(types.NewJSON(r.Payload)), CreatedBy: omit.From(r.CreatedBy),
		BlobID: nullable.String(deref(r.BlobID)), Filename: omit.From(r.Filename),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Histories(ctx context.Context, f revisions.HistoryFilter) ([]revisions.History, error) {
	c := models.ItemHistories.Columns
	query := models.ItemHistories.Query(sm.OrderBy(c.Number).Desc())
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(sqlite.Arg(f.ItemID))))
	}
	if f.Number > 0 {
		query.Apply(sm.Where(c.Number.EQ(sqlite.Arg(f.Number))))
	}
	if f.BeforeNumber > 0 {
		query.Apply(sm.Where(c.Number.LT(sqlite.Arg(f.BeforeNumber))))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]revisions.History, len(rows))
	for i, row := range rows {
		out[i] = revisions.History{ID: row.ID, RevisionID: row.RevisionID, ItemID: row.ItemID, Number: int(row.Number), ChangedFields: row.ChangedFields.Val, CreatedAt: row.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) LastHistoryNumber(ctx context.Context, itemID string) (int, error) {
	c := models.ItemHistories.Columns
	query := models.ItemHistories.Query(sm.Columns(sqlite.Raw("coalesce(max(number), 0)")), sm.Where(c.ItemID.EQ(sqlite.Arg(itemID))))
	n, err := bob.One(ctx, q.exec, query, scan.SingleColumnMapper[int])
	return n, dberr.Translate(err)
}

func (q Queries) InsertHistory(ctx context.Context, h revisions.History) error {
	_, err := models.ItemHistories.Insert(&models.ItemHistorySetter{
		ID: omit.From(h.ID), CreatedAt: omit.From(h.CreatedAt), RevisionID: omit.From(h.RevisionID), ItemID: omit.From(h.ItemID),
		Number: omit.From(int64(h.Number)), ChangedFields: omit.From(types.NewJSON(h.ChangedFields)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DeleteHistoryBelow(ctx context.Context, itemID string, n int) error {
	w := models.DeleteWhere.ItemHistories
	_, err := models.ItemHistories.Delete(w.ItemID.EQ(itemID), w.Number.LT(int64(n))).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Publications(ctx context.Context, f revisions.PublicationFilter) ([]revisions.Publication, error) {
	c := models.Publications.Columns
	query := models.Publications.Query(sm.OrderBy(c.Version), sm.OrderBy(c.ID))
	if f.ID != "" {
		query.Apply(sm.Where(c.ID.EQ(sqlite.Arg(f.ID))))
	}
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(sqlite.Arg(f.ItemID))))
	}
	if f.AfterVersion > 0 {
		query.Apply(sm.Where(c.Version.GT(sqlite.Arg(f.AfterVersion))))
	}
	if f.RevisionID != "" {
		query.Apply(sm.Where(c.RevisionID.EQ(sqlite.Arg(f.RevisionID))))
	}
	if f.Action != "" {
		query.Apply(sm.Where(c.Action.EQ(sqlite.Arg(string(f.Action)))))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]revisions.Publication, len(rows))
	for i, row := range rows {
		out[i] = revisions.Publication{ID: row.ID, CreatedAt: row.CreatedAt.UTC(), ItemID: row.ItemID, Version: int(row.Version), Action: revisions.PublicationAction(row.Action),
			RevisionID: row.RevisionID.Ptr(), PublishedBy: row.PublishedBy, Snapshot: row.Snapshot.Val}
	}
	return out, nil
}

func (q Queries) CountPublications(ctx context.Context) (int, error) {
	n, err := models.Publications.Query().Count(ctx, q.exec)
	return int(n), dberr.Translate(err)
}

func (q Queries) InsertPublication(ctx context.Context, p revisions.Publication) error {
	_, err := models.Publications.Insert(&models.PublicationSetter{
		ID: omit.From(p.ID), CreatedAt: omit.From(p.CreatedAt), Version: omit.From(int64(p.Version)), PublishedBy: omit.From(p.PublishedBy),
		Snapshot: omit.From(types.NewJSON(p.Snapshot)), ItemID: omit.From(p.ItemID), Action: omit.From(string(p.Action)), RevisionID: nullable.String(deref(p.RevisionID)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func revisionRecord(r *models.ItemRevision) revisions.Revision {
	keywords := r.Keywords.Val
	if keywords == nil {
		keywords = []string{}
	}
	return revisions.Revision{
		ID: r.ID, CreatedAt: r.CreatedAt.UTC(), ItemID: r.ItemID, ContainerID: r.ContainerID, ContentTypeID: r.ContentTypeID,
		SchemaRevisionID: r.SchemaRevisionID, BlobID: r.BlobID.Ptr(), Filename: r.Filename, RevisionNumber: int(r.RevisionNumber),
		Name: r.Name, Keywords: keywords, Payload: r.Payload.Val, CreatedBy: r.CreatedBy,
	}
}

// deref is the string p points to, or empty.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// CollectRevisions filters by item in its own statement, so the engine seeks
// the item's revisions instead of scanning them all on every write.
func (q Queries) CollectRevisions(ctx context.Context, itemID string) error {
	scope, args := "TRUE", []any{}
	if itemID != "" {
		scope, args = "v.item_id = ?", []any{itemID}
	}
	_, err := q.exec.ExecContext(ctx, `DELETE FROM item_revisions AS v
WHERE `+scope+`
AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.head_revision_id = v.id OR r.published_revision_id = v.id)
AND NOT EXISTS (SELECT 1 FROM item_surfaces p WHERE p.revision_id = v.id)
AND NOT EXISTS (SELECT 1 FROM item_histories h WHERE h.revision_id = v.id)
AND NOT EXISTS (SELECT 1 FROM domain_events e JOIN item_revisions event_revision
 ON event_revision.id = json_extract(e.data, '$.revision_id')
 WHERE e.dispatched_at IS NULL AND event_revision.item_id = v.item_id
 AND (event_revision.id = v.id OR event_revision.revision_number = v.revision_number + 1))`, args...)
	return dberr.Translate(err)
}
