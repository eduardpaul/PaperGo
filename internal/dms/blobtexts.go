package dms

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/blobtext"
)

// ErrNoText means the file's text has not been read yet.
var ErrNoText = errors.New("the file's text has not been read")

// BlobTextInput is the text read from a file, as document.read_text stores
// it.
type BlobTextInput struct {
	SHA256       string
	Settings     string
	ObjectKey    string
	ObjectSize   int64
	ObjectSHA256 string
	Pages        int
	Chars        int
	OCRPages     int
	SkippedPages int
	Truncated    bool
	Language     string
	Extractor    string
	Models       json.RawMessage
}

// FindBlobText returns the text read from files with sha256 with these
// settings, or nil.
func (s *Service) FindBlobText(ctx context.Context, sha256, settings string) (*ent.BlobText, error) {
	row, err := s.Client.BlobText.Query().Where(blobtext.Sha256EQ(sha256), blobtext.SettingsEQ(settings)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return row, err
}

// SaveBlobText records text read from a file. When another run recorded
// the same text first, its row wins and this object is left to the storage
// collector. The bytes must still belong to a file.
func (s *Service) SaveBlobText(ctx context.Context, in BlobTextInput) (*ent.BlobText, error) {
	held, err := s.Client.Blob.Query().Where(blob.Sha256EQ(in.SHA256)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !held {
		return nil, ErrNotFound
	}
	if len(in.Models) == 0 || string(in.Models) == "null" {
		in.Models = json.RawMessage(`[]`)
	}
	if _, err = s.Client.ExecContext(ctx, `INSERT INTO blob_texts (id,created_at,sha256,settings,object_key,object_size,object_sha256,pages,chars,ocr_pages,skipped_pages,truncated,language,extractor,models)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(sha256,settings) DO NOTHING`, NewEventID(), time.Now().UTC(), in.SHA256, in.Settings, in.ObjectKey, in.ObjectSize, in.ObjectSHA256,
		in.Pages, in.Chars, in.OCRPages, in.SkippedPages, in.Truncated, in.Language, in.Extractor, string(in.Models)); err != nil {
		return nil, err
	}
	return s.Client.BlobText.Query().Where(blobtext.Sha256EQ(in.SHA256), blobtext.SettingsEQ(in.Settings)).Only(ctx)
}

// BlobText returns a text by ID, for later workflow steps.
func (s *Service) BlobText(ctx context.Context, id string) (*ent.BlobText, error) {
	row, err := s.Client.BlobText.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return row, err
}

// ItemText returns the file version the subject may read (the current
// one, or blobID) with the latest text read from its bytes.
func (s *Service) ItemText(ctx context.Context, subject, id, blobID string) (*ent.Blob, *ent.BlobText, error) {
	b, err := s.GetBlob(ctx, subject, id, blobID)
	if err != nil {
		return nil, nil, err
	}
	text, err := s.Client.BlobText.Query().Where(blobtext.Sha256EQ(b.Sha256)).Order(ent.Desc(blobtext.FieldCreatedAt), ent.Desc(blobtext.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return b, nil, ErrNoText
	}
	return b, text, err
}

// collectBlobTexts drops texts of bytes no file holds any more and queues
// their objects for deletion.
func (s *Service) collectBlobTexts(ctx context.Context) error {
	return s.write(ctx, func(t *Service) error {
		rows, err := t.Client.QueryContext(ctx, `SELECT id, object_key FROM blob_texts x WHERE NOT EXISTS (SELECT 1 FROM blobs WHERE sha256=x.sha256) LIMIT 100`)
		if err != nil {
			return err
		}
		var ids, keys []string
		for rows.Next() {
			var id, key string
			if err = rows.Scan(&id, &key); err != nil {
				break
			}
			ids, keys = append(ids, id), append(keys, key)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
		for i, id := range ids {
			if _, err = t.Client.ExecContext(ctx, `INSERT INTO blob_deletions(id,created_at,object_key) VALUES (?,?,?) ON CONFLICT(object_key) DO NOTHING`, NewEventID(), time.Now().UTC(), keys[i]); err != nil {
				return err
			}
			if _, err = t.Client.ExecContext(ctx, `DELETE FROM blob_texts WHERE id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}
