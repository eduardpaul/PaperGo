package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
)

// Texts are activity records in the blob_text namespace: scoped by the
// bytes' SHA-256 and keyed by the reading settings.
const textNamespace = "blob_text"

func readText(r scan.Rows) (blobs.Text, error) {
	var id, scope, key string
	var created time.Time
	var payload []byte
	if err := r.Scan(&id, &created, &scope, &key, &payload); err != nil {
		return blobs.Text{}, err
	}
	var t blobs.Text
	if err := json.Unmarshal(payload, &t); err != nil {
		return blobs.Text{}, err
	}
	t.ID, t.CreatedAt, t.Sha256, t.Settings = id, created.UTC(), scope, key
	return t, nil
}

const textColumns = `SELECT id, created_at, scope, key, payload FROM activity_records WHERE namespace = $1`

func (q Queries) oneText(ctx context.Context, query string, args ...any) (blobs.Text, error) {
	rows, err := directoryRows(ctx, q.exec, query, readText, args...)
	if err != nil {
		return blobs.Text{}, err
	}
	if len(rows) == 0 {
		return blobs.Text{}, dberr.ErrNotFound
	}
	return rows[0], nil
}

func (q Queries) FindText(ctx context.Context, sha256, settings string) (blobs.Text, error) {
	return q.oneText(ctx, textColumns+` AND scope = $2 AND key = $3`, textNamespace, sha256, settings)
}

func (q Queries) TextByID(ctx context.Context, id string) (blobs.Text, error) {
	return q.oneText(ctx, textColumns+` AND id = $2`, textNamespace, id)
}

func (q Queries) LatestText(ctx context.Context, sha256 string) (blobs.Text, error) {
	return q.oneText(ctx, textColumns+` AND scope = $2 ORDER BY created_at DESC, id DESC LIMIT 1`, textNamespace, sha256)
}

func (q Queries) InsertText(ctx context.Context, t blobs.Text) error {
	payload, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = q.exec.ExecContext(ctx, `INSERT INTO activity_records (id, created_at, updated_at, namespace, scope, key, owner_id, payload, state)
VALUES ($1, $2, $2, $3, $4, $5, '', CAST($6 AS jsonb), 'ready') ON CONFLICT (namespace, scope, key) DO NOTHING`, t.ID, t.CreatedAt, textNamespace, t.Sha256, t.Settings, string(payload))
	return dberr.Translate(err)
}

func (q Queries) CountTexts(ctx context.Context, sha256 string) (int, error) {
	query, args := `SELECT count(*) FROM activity_records WHERE namespace = $1`, []any{textNamespace}
	if sha256 != "" {
		query, args = query+` AND scope = $2`, append(args, sha256)
	}
	rows, err := directoryRows(ctx, q.exec, query, func(r scan.Rows) (int, error) { var n int; err := r.Scan(&n); return n, err }, args...)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return rows[0], nil
}

func (q Queries) OrphanTexts(ctx context.Context, limit int) ([]blobs.Text, error) {
	return directoryRows(ctx, q.exec, `SELECT id, created_at, scope, key, payload FROM activity_records x WHERE x.namespace = $1
AND NOT EXISTS (SELECT 1 FROM blobs WHERE sha256 = x.scope) ORDER BY id LIMIT $2`, readText, textNamespace, limit)
}

func (q Queries) DeleteText(ctx context.Context, id string) error {
	_, err := q.exec.ExecContext(ctx, `DELETE FROM activity_records WHERE namespace = $1 AND id = $2`, textNamespace, id)
	return dberr.Translate(err)
}

func (q Queries) StoredObjects(ctx context.Context) ([]blobs.StoredObject, error) {
	return directoryRows(ctx, q.exec, `SELECT DISTINCT object_key, size, sha256 FROM blobs
UNION SELECT payload->>'object_key', CAST(payload->>'object_size' AS BIGINT), payload->>'object_sha256' FROM activity_records WHERE namespace = $1
UNION SELECT object_key, object_size, object_sha256 FROM blob_previews WHERE status = $2 ORDER BY 1`, func(r scan.Rows) (blobs.StoredObject, error) {
		var o blobs.StoredObject
		err := r.Scan(&o.Key, &o.Size, &o.SHA256)
		return o, err
	}, textNamespace, string(blobs.PreviewReady))
}
