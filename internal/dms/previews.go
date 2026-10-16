package dms

import (
	"context"
	"errors"
	"time"

	"papergo/ent"
	"papergo/ent/blobpreview"
	"papergo/internal/storage"
)

func (s *Service) FindPreview(ctx context.Context, sha256 string) (*ent.BlobPreview, error) {
	row, err := s.Client.BlobPreview.Query().Where(blobpreview.Sha256EQ(sha256)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return row, err
}

// SavePreview pins rendition bytes without changing items, revisions or file events.
// A successful concurrent rendition wins over an unsuccessful one.
func (s *Service) SavePreview(ctx context.Context, sha256 string, object storage.Object, width, height int) error {
	var held bool
	if err := s.scan(ctx, []any{&held}, "SELECT EXISTS(SELECT 1 FROM blobs WHERE sha256=? AND availability<>'reclaimed')", sha256); err != nil {
		return err
	}
	if !held {
		return ErrNotFound
	}
	status := "ready"
	if object.Key == "" {
		status = "failed"
	}
	_, err := s.Client.ExecContext(ctx, `INSERT INTO blob_previews(id,created_at,sha256,status,object_key,object_sha256,object_size,width,height) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(sha256) DO UPDATE SET status=excluded.status,object_key=excluded.object_key,object_sha256=excluded.object_sha256,object_size=excluded.object_size,width=excluded.width,height=excluded.height WHERE blob_previews.status<>'ready'`, NewEventID(), time.Now().UTC(), sha256, status, object.Key, object.SHA256, object.Size, width, height)
	return err
}

// ItemPreview resolves the authorized visible file before looking up shared bytes.
func (s *Service) ItemPreview(ctx context.Context, subject, id, blobID string) (*ent.Blob, *ent.BlobPreview, error) {
	type result struct {
		file    *ent.Blob
		preview *ent.BlobPreview
	}
	out, err := read(ctx, s, func(t *Service) (result, error) {
		file, err := t.GetBlob(ctx, subject, id, blobID)
		if err != nil {
			return result{}, err
		}
		preview, err := t.FindPreview(ctx, file.Sha256)
		return result{file, preview}, err
	})
	return out.file, out.preview, err
}

func (s *Service) collectPreviews(ctx context.Context) error {
	return s.write(ctx, func(t *Service) error {
		rows, err := t.Client.QueryContext(ctx, `SELECT id,object_key FROM blob_previews p WHERE NOT EXISTS(SELECT 1 FROM blobs WHERE sha256=p.sha256 AND availability<>'reclaimed') LIMIT 100`)
		if err != nil {
			return err
		}
		var ids, keys []string
		for rows.Next() {
			var id, key string
			if err = rows.Scan(&id, &key); err != nil {
				break
			}
			ids = append(ids, id)
			keys = append(keys, key)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
		for i, id := range ids {
			if keys[i] != "" {
				if _, err = t.Client.ExecContext(ctx, `INSERT INTO blob_deletions(id,created_at,object_key) VALUES(?,?,?) ON CONFLICT(object_key) DO NOTHING`, NewEventID(), time.Now().UTC(), keys[i]); err != nil {
					return err
				}
			}
			if err = t.Client.BlobPreview.DeleteOneID(id).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}
