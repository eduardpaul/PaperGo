package postgres

import (
	"context"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/postgres/models"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/im"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/scan"
)

func (q Queries) BytesHeld(ctx context.Context, sha256 string) (bool, error) {
	w := models.SelectWhere.Blobs
	ok, err := models.Blobs.Query(w.Sha256.EQ(sha256), w.Availability.NE(string(blobs.AvailabilityReclaimed))).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) Preview(ctx context.Context, sha256 string) (blobs.Preview, error) {
	row, err := models.BlobPreviews.Query(models.SelectWhere.BlobPreviews.Sha256.EQ(sha256)).One(ctx, q.exec)
	if err != nil {
		return blobs.Preview{}, dberr.Translate(err)
	}
	return previewRecord(row), nil
}

func (q Queries) SavePreview(ctx context.Context, p blobs.Preview) error {
	_, err := models.BlobPreviews.Insert(&models.BlobPreviewSetter{
		ID: omit.From(p.ID), CreatedAt: omit.From(p.CreatedAt), Sha256: omit.From(p.SHA256), Status: omit.From(string(p.Status)),
		ObjectKey: omit.From(p.ObjectKey), ObjectSha256: omit.From(p.ObjectSHA256), ObjectSize: omit.From(p.ObjectSize),
		Width: omit.From(int64(p.Width)), Height: omit.From(int64(p.Height)),
	}, im.OnConflict("sha256").DoUpdate(
		im.SetExcluded("status", "object_key", "object_sha256", "object_size", "width", "height"),
		im.Where(models.BlobPreviews.Columns.Status.NE(psql.Arg(string(blobs.PreviewReady)))),
	)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) OrphanPreviews(ctx context.Context, limit int) ([]blobs.Preview, error) {
	b, p := models.Blobs.Columns, models.BlobPreviews.Columns
	held := models.Blobs.Query(sm.Columns(psql.Raw("1")), sm.Where(b.Sha256.EQ(p.Sha256)), sm.Where(b.Availability.NE(psql.Arg(string(blobs.AvailabilityReclaimed)))))
	rows, err := models.BlobPreviews.Query(sm.Where(psql.Raw("NOT EXISTS ?", held)), sm.Limit(limit)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]blobs.Preview, len(rows))
	for i, row := range rows {
		out[i] = previewRecord(row)
	}
	return out, nil
}

func (q Queries) DeletePreview(ctx context.Context, id string) error {
	_, err := models.BlobPreviews.Delete(models.DeleteWhere.BlobPreviews.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) QueueDeletion(ctx context.Context, d blobs.Deletion) error {
	_, err := models.BlobDeletions.Insert(&models.BlobDeletionSetter{
		ID: omit.From(d.ID), CreatedAt: omit.From(d.CreatedAt), ObjectKey: omit.From(d.ObjectKey),
	}, im.OnConflict("object_key").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) QueueItemObjects(ctx context.Context, itemID string, at time.Time) error {
	b := models.Blobs.Columns
	_, err := psql.Insert(
		im.Into(models.BlobDeletions.Name(), "id", "created_at", "object_key"),
		im.Query(models.Blobs.Query(sm.Columns(b.ID, psql.Arg(at), b.ObjectKey), sm.Where(b.ItemID.EQ(psql.Arg(itemID))))),
		im.OnConflict("object_key").DoNothing(),
	).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Deletions(ctx context.Context, limit int) ([]blobs.Deletion, error) {
	rows, err := models.BlobDeletions.Query(sm.OrderBy(models.BlobDeletions.Columns.CreatedAt), sm.OrderBy(models.BlobDeletions.Columns.ID), sm.Limit(limit)).All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]blobs.Deletion, len(rows))
	for i, row := range rows {
		out[i] = blobs.Deletion{ID: row.ID, ObjectKey: row.ObjectKey, CreatedAt: row.CreatedAt.UTC()}
	}
	return out, nil
}

func (q Queries) DeleteDeletion(ctx context.Context, id string) error {
	_, err := models.BlobDeletions.Delete(models.DeleteWhere.BlobDeletions.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) InsertBlobLease(ctx context.Context, l blobs.Lease) error {
	_, err := models.BlobLeases.Insert(&models.BlobLeaseSetter{
		ID: omit.From(l.ID), CreatedAt: omit.From(l.CreatedAt), OperationID: omit.From(l.OperationID), BlobID: omit.From(l.BlobID),
	}, im.OnConflict("operation_id", "blob_id").DoNothing()).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func previewRecord(r *models.BlobPreview) blobs.Preview {
	return blobs.Preview{
		ID: r.ID, SHA256: r.Sha256, Status: blobs.PreviewStatus(r.Status), ObjectKey: r.ObjectKey, ObjectSHA256: r.ObjectSha256,
		ObjectSize: r.ObjectSize, Width: int(r.Width), Height: int(r.Height), CreatedAt: r.CreatedAt.UTC(),
	}
}

// ObjectReferenced checks every table that can hold a stored object in one
// statement, so the answer comes from one snapshot.
func (q Queries) ObjectReferenced(ctx context.Context, key string, p blobs.Packages) (bool, error) {
	type answer struct{ Referenced bool }
	a, err := bob.One(ctx, q.exec, psql.RawQuery(`SELECT EXISTS(SELECT 1 FROM blobs WHERE object_key=? AND availability<>'reclaimed')
	OR EXISTS(SELECT 1 FROM activity_records WHERE namespace='blob_text' AND payload->>'object_key'=?)
	OR EXISTS(SELECT 1 FROM blob_previews WHERE object_key=?)
	OR EXISTS(SELECT 1 FROM artifacts WHERE object_key=?)
	OR EXISTS(SELECT 1 FROM operations WHERE (kind=? AND status='pending' AND payload->>'object_key'=?)
		OR (kind=? AND status='succeeded' AND result->>'object_key'=? AND (result->>'expires_at')::timestamptz>now()))
	OR EXISTS(SELECT 1 FROM blob_leases l JOIN blobs b ON b.id=l.blob_id WHERE b.object_key=?) AS referenced`, key, key, key, key, p.ImportKind, key, p.ExportKind, key, key), scan.StructMapper[answer]())
	return a.Referenced, dberr.Translate(err)
}

func (q Queries) StoredBytes(ctx context.Context) (int64, error) {
	rows, err := directoryRows(ctx, q.exec, `SELECT COALESCE(SUM(size), 0) FROM (SELECT sha256, MAX(size) AS size FROM blobs WHERE availability = 'available' GROUP BY sha256) AS stored`,
		func(r scan.Rows) (int64, error) { var n int64; err := r.Scan(&n); return n, err })
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return rows[0], nil
}
