package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
)

var ErrUnsupportedFile = errors.New("only PDF, TIFF, JPEG, PNG and WebP files are supported")

// DocumentType follows PaperDotNet's signature policy. The first bytes decide;
// filenames and declared media types are not evidence of the actual format.
func DocumentType(header []byte) string {
	switch {
	case bytes.HasPrefix(header, []byte("%PDF-")):
		return "application/pdf"
	case bytes.HasPrefix(header, []byte{'I', 'I', 42, 0}), bytes.HasPrefix(header, []byte{'M', 'M', 0, 42}):
		return "image/tiff"
	case bytes.HasPrefix(header, []byte{255, 216, 255}):
		return "image/jpeg"
	case bytes.HasPrefix(header, []byte{137, 80, 78, 71, 13, 10, 26, 10}):
		return "image/png"
	case len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
}
func SupportedDocumentType(media string) bool {
	switch media {
	case "application/pdf", "image/tiff", "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

func ValidateDocumentMediaType(media string) error {
	if !SupportedDocumentType(media) {
		return ErrUnsupportedFile
	}
	return nil
}

type documentReader struct {
	io.Reader
	header []byte
}

func (r *documentReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if len(r.header) < 12 {
		take := min(n, 12-len(r.header))
		r.header = append(r.header, p[:take]...)
	}
	return n, err
}

// PutDocument retains streaming limits/hashing and removes rejected bytes.
// Generic Put remains available for application ZIP packages.
func PutDocument(ctx context.Context, store Store, input io.Reader, max int64) (Object, string, error) {
	reader := &documentReader{Reader: input}
	object, err := store.Put(ctx, reader, max)
	if err != nil {
		return Object{}, "", err
	}
	media := DocumentType(reader.header)
	if media == "" {
		_ = store.Delete(context.WithoutCancel(ctx), object.Key)
		return Object{}, "", ErrUnsupportedFile
	}
	return object, media, nil
}
