package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
)

// ErrNoText means the file's text has not been read yet.
var ErrNoText = errors.New("the file's text has not been read")

// ItemText returns the authorized file version and its newest extracted text.
func (s *Service) ItemText(ctx context.Context, subject, id, blobID string) (*blobs.Blob, *blobs.Text, error) {
	b, err := s.GetBlob(ctx, subject, id, blobID)
	if err != nil {
		return nil, nil, err
	}
	text, err := s.DAL.Blobs().LatestText(ctx, b.Sha256)
	if dberr.IsNotFound(err) {
		return b, nil, ErrNoText
	}
	return b, text, err
}

func (s *Service) collectTexts(ctx context.Context) error {
	return s.write(ctx, func(t *Service) error { return t.DAL.Blobs().CollectTexts(ctx) })
}
