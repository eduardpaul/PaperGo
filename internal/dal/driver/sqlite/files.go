package sqlite

import (
	"context"
	"strings"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/sqlite/models"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/types"
	"github.com/stephenafamo/scan"
)

func (q Queries) Blob(ctx context.Context, id string) (blobs.Blob, error) {
	row, err := models.FindBlob(ctx, q.exec, id)
	if err != nil {
		return blobs.Blob{}, dberr.Translate(err)
	}
	return blobRecord(row), nil
}

func (q Queries) Blobs(ctx context.Context, f blobs.BlobFilter) ([]blobs.Blob, error) {
	c := models.Blobs.Columns
	query := models.Blobs.Query()
	if f.IDs != nil {
		query.Apply(sm.Where(c.ID.In(sqlite.Arg(anys(f.IDs)...))))
	}
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(sqlite.Arg(f.ItemID))))
	}
	if f.Version > 0 {
		query.Apply(sm.Where(c.Version.EQ(sqlite.Arg(f.Version))))
	}
	if f.BeforeVersion > 0 {
		query.Apply(sm.Where(c.Version.LT(sqlite.Arg(f.BeforeVersion))))
	}
	if f.DueBy != nil {
		query.Apply(sm.Where(c.Availability.EQ(sqlite.Arg(string(blobs.AvailabilityPending)))),
			sm.Where(sqlite.Or(c.ReclaimAfter.IsNull(), c.ReclaimAfter.LTE(sqlite.Arg(*f.DueBy)))))
	}
	if f.AfterID != "" {
		query.Apply(sm.Where(c.ID.GT(sqlite.Arg(f.AfterID))))
	}
	if f.Order == blobs.NewestFirst {
		query.Apply(sm.OrderBy(c.Version).Desc())
	} else {
		query.Apply(sm.OrderBy(c.ID))
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]blobs.Blob, len(rows))
	for i, row := range rows {
		out[i] = blobRecord(row)
	}
	return out, nil
}

func (q Queries) SharedBlob(ctx context.Context, sha256 string, size int64) (blobs.Blob, error) {
	w := models.SelectWhere.Blobs
	row, err := models.Blobs.Query(w.Sha256.EQ(sha256), w.Size.EQ(size), w.Availability.NE(string(blobs.AvailabilityReclaimed)), sm.OrderBy(models.Blobs.Columns.ID), sm.Limit(1)).One(ctx, q.exec)
	if err != nil {
		return blobs.Blob{}, dberr.Translate(err)
	}
	return blobRecord(row), nil
}

func (q Queries) LatestBlobVersion(ctx context.Context, itemID string) (int, error) {
	c := models.Blobs.Columns
	query := models.Blobs.Query(sm.Columns(sqlite.Raw("coalesce(max(version), 0)")), sm.Where(c.ItemID.EQ(sqlite.Arg(itemID))))
	n, err := bob.One(ctx, q.exec, query, scan.SingleColumnMapper[int])
	return n, dberr.Translate(err)
}

func (q Queries) InsertBlob(ctx context.Context, b blobs.Blob) error {
	_, err := models.Blobs.Insert(&models.BlobSetter{
		ID: omit.From(b.ID), CreatedAt: omit.From(b.CreatedAt), Version: omit.From(int64(b.Version)), ObjectKey: omit.From(b.ObjectKey),
		Filename: omit.From(b.Filename), ContentType: omit.From(string(b.ContentType)), Languages: omit.From(b.Languages), Size: omit.From(b.Size),
		Sha256: omit.From(b.Sha256), ItemID: omit.From(b.ItemID), Availability: omit.From(string(b.Availability)),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetBlobAvailability(ctx context.Context, id string, a blobs.Availability, reclaimAfter *time.Time) error {
	set := &models.BlobSetter{Availability: omit.From(string(a)), ReclaimAfter: nullable.Time(reclaimAfter)}
	_, err := models.Blobs.Update(set.UpdateMod(), models.UpdateWhere.Blobs.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) StagedFile(ctx context.Context, id string) (blobs.StagedFile, error) {
	row, err := models.FindStagedFile(ctx, q.exec, id)
	if err != nil {
		return blobs.StagedFile{}, dberr.Translate(err)
	}
	return blobs.StagedFile{
		ID: row.ID, WorkspaceID: row.WorkspaceID, ItemID: row.ItemID, RunID: row.RunID, Sources: row.Sources.Val, ObjectKey: row.ObjectKey,
		Filename: row.Filename, ContentType: row.ContentType, Size: row.Size, Sha256: row.Sha256, Metrics: row.Metrics.Val,
		State: blobs.StagedState(row.State), CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (q Queries) InsertStagedFile(ctx context.Context, f blobs.StagedFile) error {
	_, err := models.StagedFiles.Insert(&models.StagedFileSetter{
		ID: omit.From(f.ID), CreatedAt: omit.From(f.CreatedAt), UpdatedAt: omit.From(f.UpdatedAt), WorkspaceID: omit.From(f.WorkspaceID),
		ItemID: omit.From(f.ItemID), RunID: omit.From(f.RunID), Sources: omit.From(types.NewJSON(f.Sources)), ObjectKey: omit.From(f.ObjectKey),
		Filename: omit.From(f.Filename), ContentType: omit.From(f.ContentType), Size: omit.From(f.Size), Sha256: omit.From(f.Sha256),
		Metrics: omit.From(types.NewJSON(f.Metrics)), State: omit.From(string(f.State)), CreatedBy: omit.From(f.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) SetStagedState(ctx context.Context, id string, state blobs.StagedState, at time.Time) error {
	set := &models.StagedFileSetter{State: omit.From(string(state)), UpdatedAt: omit.From(at)}
	_, err := models.StagedFiles.Update(set.UpdateMod(), models.UpdateWhere.StagedFiles.ID.EQ(id)).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) DiscardStaged(ctx context.Context, s blobs.StagedSelection, at time.Time) error {
	args := []any{string(blobs.StagedDiscarded), at, string(blobs.StagedPending)}
	arg := func(v any) string {
		args = append(args, v)
		return "?"
	}
	where := []string{}
	if len(s.RunIDs) > 0 {
		in := make([]string, len(s.RunIDs))
		for i, id := range s.RunIDs {
			in[i] = arg(id)
		}
		where = append(where, "run_id IN ("+strings.Join(in, ", ")+")")
	}
	if s.ItemID != "" {
		where = append(where, "item_id = "+arg(s.ItemID))
	}
	if s.WorkflowID != "" {
		where = append(where, "run_id IN (SELECT id FROM workflow_runs WHERE workflow_id = "+arg(s.WorkflowID)+")")
	}
	if s.ExceptRunID != "" {
		where = append(where, "run_id <> "+arg(s.ExceptRunID))
	}
	_, err := q.exec.ExecContext(ctx, "UPDATE staged_files SET state = ?, updated_at = ? WHERE state = ? AND "+strings.Join(where, " AND "), args...)
	return dberr.Translate(err)
}

func (q Queries) BlobStaged(ctx context.Context, objectKey, blobID string) (bool, error) {
	return directoryBool(ctx, q.exec, `SELECT EXISTS(SELECT 1 FROM staged_files WHERE state = ? AND (object_key = ? OR EXISTS(SELECT 1 FROM json_each(sources) WHERE json_extract(value, '$.blob_id') = ?)))`, string(blobs.StagedPending), objectKey, blobID)
}

func (q Queries) BlobLeased(ctx context.Context, blobID string) (bool, error) {
	ok, err := models.BlobLeases.Query(models.SelectWhere.BlobLeases.BlobID.EQ(blobID)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func blobRecord(r *models.Blob) blobs.Blob {
	return blobs.Blob{
		ID: r.ID, CreatedAt: r.CreatedAt.UTC(), ItemID: r.ItemID, Version: int(r.Version), ObjectKey: r.ObjectKey, Filename: r.Filename,
		ContentType: blobs.MediaType(r.ContentType), Languages: r.Languages, Size: r.Size, Sha256: r.Sha256,
		Availability: blobs.Availability(r.Availability), ReclaimAfter: nullable.UTC(r.ReclaimAfter),
	}
}
