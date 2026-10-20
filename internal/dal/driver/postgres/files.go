package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
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
		query.Apply(sm.Where(c.ID.In(psql.Arg(anys(f.IDs)...))))
	}
	if f.ItemID != "" {
		query.Apply(sm.Where(c.ItemID.EQ(psql.Arg(f.ItemID))))
	}
	if f.Version > 0 {
		query.Apply(sm.Where(c.Version.EQ(psql.Arg(f.Version))))
	}
	if f.BeforeVersion > 0 {
		query.Apply(sm.Where(c.Version.LT(psql.Arg(f.BeforeVersion))))
	}
	if f.DueBy != nil {
		query.Apply(sm.Where(c.Availability.EQ(psql.Arg(string(blobs.AvailabilityPending)))),
			sm.Where(psql.Or(c.ReclaimAfter.IsNull(), c.ReclaimAfter.LTE(psql.Arg(*f.DueBy)))))
	}
	if f.AfterID != "" {
		query.Apply(sm.Where(c.ID.GT(psql.Arg(f.AfterID))))
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
	query := models.Blobs.Query(sm.Columns(psql.Raw("coalesce(max(version), 0)")), sm.Where(c.ItemID.EQ(psql.Arg(itemID))))
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

func artifactRecord(row *models.Artifact) blobs.Artifact {
	return blobs.Artifact{
		ID: row.ID, WorkspaceID: row.WorkspaceID, ItemID: row.ItemID, OperationID: row.OperationID.GetOrZero(), InputKey: row.InputKey, Sources: row.Sources.Val,
		ObjectKey: row.ObjectKey, Filename: row.Filename, ContentType: row.ContentType, Size: row.Size, Sha256: row.Sha256, Metrics: row.Metrics.Val,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
	}
}

func (q Queries) Artifact(ctx context.Context, id string) (blobs.Artifact, error) {
	row, err := models.FindArtifact(ctx, q.exec, id)
	if err != nil {
		return blobs.Artifact{}, dberr.Translate(err)
	}
	return artifactRecord(row), nil
}

func (q Queries) InsertArtifact(ctx context.Context, a blobs.Artifact) error {
	_, err := models.Artifacts.Insert(&models.ArtifactSetter{
		ID: omit.From(a.ID), CreatedAt: omit.From(a.CreatedAt), WorkspaceID: omit.From(a.WorkspaceID), ItemID: omit.From(a.ItemID),
		OperationID: nullable.String(a.OperationID), InputKey: omit.From(a.InputKey), Sources: omit.From(types.NewJSON(a.Sources)),
		ObjectKey: omit.From(a.ObjectKey), Filename: omit.From(a.Filename), ContentType: omit.From(a.ContentType), Size: omit.From(a.Size),
		Sha256: omit.From(a.Sha256), Metrics: omit.From(types.NewJSON(a.Metrics)), ExpiresAt: omit.From(a.ExpiresAt), CreatedBy: omit.From(a.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) ReusableArtifact(ctx context.Context, itemID, inputKey string, now time.Time) (blobs.Artifact, error) {
	w := models.SelectWhere.Artifacts
	row, err := models.Artifacts.Query(w.ItemID.EQ(itemID), w.InputKey.EQ(inputKey), w.ExpiresAt.GT(now),
		sm.OrderBy(models.Artifacts.Columns.CreatedAt).Desc(), sm.Limit(1)).One(ctx, q.exec)
	if err != nil {
		return blobs.Artifact{}, dberr.Translate(err)
	}
	return artifactRecord(row), nil
}

func (q Queries) DeleteExpiredArtifacts(ctx context.Context, now time.Time, limit int) (int, error) {
	res, err := q.exec.ExecContext(ctx, `DELETE FROM artifacts WHERE id IN (SELECT a.id FROM artifacts a WHERE a.expires_at <= $1
	AND NOT EXISTS (SELECT 1 FROM staged_files f WHERE f.artifact_id = a.id AND f.state = $2) ORDER BY a.expires_at LIMIT $3)`, now, string(blobs.StagedPending), limit)
	if err != nil {
		return 0, dberr.Translate(err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (q Queries) StagedFile(ctx context.Context, id string) (blobs.StagedFile, error) {
	row, err := models.FindStagedFile(ctx, q.exec, id)
	if err != nil {
		return blobs.StagedFile{}, dberr.Translate(err)
	}
	return blobs.StagedFile{
		ID: row.ID, WorkspaceID: row.WorkspaceID, ItemID: row.ItemID, ArtifactID: row.ArtifactID, RunID: row.RunID.GetOrZero(),
		State: blobs.StagedState(row.State), CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (q Queries) InsertStagedFile(ctx context.Context, f blobs.StagedFile) error {
	_, err := models.StagedFiles.Insert(&models.StagedFileSetter{
		ID: omit.From(f.ID), CreatedAt: omit.From(f.CreatedAt), UpdatedAt: omit.From(f.UpdatedAt), WorkspaceID: omit.From(f.WorkspaceID),
		ItemID: omit.From(f.ItemID), ArtifactID: omit.From(f.ArtifactID), RunID: nullable.String(f.RunID), State: omit.From(string(f.State)), CreatedBy: omit.From(f.CreatedBy),
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
		return "$" + strconv.Itoa(len(args))
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
		where = append(where, "(run_id IS NULL OR run_id <> "+arg(s.ExceptRunID)+")")
	}
	_, err := q.exec.ExecContext(ctx, "UPDATE staged_files SET state = $1, updated_at = $2 WHERE state = $3 AND "+strings.Join(where, " AND "), args...)
	return dberr.Translate(err)
}

func (q Queries) BlobStaged(ctx context.Context, objectKey, blobID string) (bool, error) {
	return directoryBool(ctx, q.exec, `SELECT EXISTS(SELECT 1 FROM staged_files f JOIN artifacts a ON a.id = f.artifact_id WHERE f.state = $1 AND (a.object_key = $2 OR EXISTS(SELECT 1 FROM jsonb_array_elements(a.sources) s WHERE s ->> 'blob_id' = $3)))`, string(blobs.StagedPending), objectKey, blobID)
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
