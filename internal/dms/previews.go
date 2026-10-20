package dms

import (
	"context"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/storage"
)

// FindPreview returns the preview of the bytes with a SHA-256, or nil when
// none was rendered.
func (s *Service) FindPreview(ctx context.Context, sha256 string) (*blobs.Preview, error) {
	return s.DAL.Blobs().Preview(ctx, sha256)
}

// SavePreview pins rendition bytes without changing items, revisions or file events.
// A successful concurrent rendition wins over an unsuccessful one.
func (s *Service) SavePreview(ctx context.Context, sha256 string, object storage.Object, width, height int) error {
	err := s.DAL.Blobs().SavePreview(ctx, blobs.Preview{SHA256: sha256, ObjectKey: object.Key, ObjectSHA256: object.SHA256, ObjectSize: object.Size, Width: width, Height: height})
	if dberr.IsNotFound(err) {
		return ErrNotFound
	}
	return err
}

// ItemPreview resolves the authorized visible file before looking up shared bytes.
func (s *Service) ItemPreview(ctx context.Context, subject, id, blobID string) (*blobs.Blob, *blobs.Preview, error) {
	type result struct {
		file    *blobs.Blob
		preview *blobs.Preview
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
	return s.write(ctx, func(t *Service) error { return t.DAL.Blobs().CollectPreviews(ctx) })
}
