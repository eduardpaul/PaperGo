package blobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

// Text is the text read from file bytes with one set of settings. The text
// itself is a stored object; reading it again can mean OCR, so it is kept
// once per bytes and settings while a file holds the bytes.
type Text struct {
	ID           string          `json:"id"`
	CreatedAt    time.Time       `json:"created_at"`
	Sha256       string          `json:"sha256"`
	Settings     string          `json:"settings"`
	ObjectKey    string          `json:"object_key"`
	ObjectSize   int64           `json:"object_size"`
	ObjectSha256 string          `json:"object_sha256"`
	Pages        int             `json:"pages"`
	Chars        int             `json:"chars"`
	OcrPages     int             `json:"ocr_pages"`
	SkippedPages int             `json:"skipped_pages"`
	Truncated    bool            `json:"truncated"`
	Language     string          `json:"language"`
	Extractor    string          `json:"extractor"`
	Models       json.RawMessage `json:"models"`
}

// TextInput is the text read from a file, as document.read_text stores it.
type TextInput struct {
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

// StoredObject is an object a file, a text or a preview references.
type StoredObject struct {
	Key    string
	Size   int64
	SHA256 string
}

// TextQueries are the statements of extracted texts.
type TextQueries interface {
	// FindText returns the text of bytes with settings.
	FindText(ctx context.Context, sha256, settings string) (Text, error)
	TextByID(ctx context.Context, id string) (Text, error)
	// LatestText returns the newest text of bytes.
	LatestText(ctx context.Context, sha256 string) (Text, error)
	// InsertText inserts t unless the bytes already have a text with its
	// settings.
	InsertText(ctx context.Context, t Text) error
	// CountTexts counts the texts of bytes, or all texts for an empty
	// sha256.
	CountTexts(ctx context.Context, sha256 string) (int, error)
	// OrphanTexts returns texts of bytes no file holds.
	OrphanTexts(ctx context.Context, limit int) ([]Text, error)
	DeleteText(ctx context.Context, id string) error
	// StoredObjects lists each object a blob, text or ready preview
	// references, once per key and metadata.
	StoredObjects(ctx context.Context) ([]StoredObject, error)
}

// FindText returns the text read from bytes with settings, or nil.
func (r *Repo) FindText(ctx context.Context, sha256, settings string) (*Text, error) {
	t, err := r.read.FindText(ctx, sha256, settings)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Text returns a text by ID, for later workflow steps.
func (r *Repo) Text(ctx context.Context, id string) (*Text, error) {
	t, err := r.read.TextByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// LatestText returns the newest text read from bytes.
func (r *Repo) LatestText(ctx context.Context, sha256 string) (*Text, error) {
	t, err := r.read.LatestText(ctx, sha256)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SaveText records text read from a file. When another run recorded the
// same text first, its row wins and this object is left to the storage
// collector. The bytes must still belong to a file.
func (r *Repo) SaveText(ctx context.Context, in TextInput) (*Text, error) {
	if len(in.Models) == 0 || string(in.Models) == "null" {
		in.Models = json.RawMessage(`[]`)
	}
	t := Text{
		ID: uuid.Must(uuid.NewV7()).String(), CreatedAt: now(), Sha256: in.SHA256, Settings: in.Settings,
		ObjectKey: in.ObjectKey, ObjectSize: in.ObjectSize, ObjectSha256: in.ObjectSHA256,
		Pages: in.Pages, Chars: in.Chars, OcrPages: in.OCRPages, SkippedPages: in.SkippedPages,
		Truncated: in.Truncated, Language: in.Language, Extractor: in.Extractor, Models: in.Models,
	}
	var saved Text
	err := r.write(ctx, func(q Queries) error {
		held, err := q.BytesHeld(ctx, in.SHA256)
		if err != nil {
			return err
		}
		if !held {
			return dberr.ErrNotFound
		}
		if err = q.InsertText(ctx, t); err != nil {
			return err
		}
		saved, err = q.FindText(ctx, in.SHA256, in.Settings)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// TextCount counts the texts of bytes, or all texts for an empty sha256.
func (r *Repo) TextCount(ctx context.Context, sha256 string) (int, error) {
	return r.read.CountTexts(ctx, sha256)
}

// CollectTexts drops texts of bytes no file holds any more and queues their
// objects for deletion.
func (r *Repo) CollectTexts(ctx context.Context) error {
	return r.write(ctx, func(q Queries) error {
		texts, err := q.OrphanTexts(ctx, 100)
		if err != nil {
			return err
		}
		for _, t := range texts {
			if t.ObjectKey != "" {
				if err = q.QueueDeletion(ctx, Deletion{ID: uuid.Must(uuid.NewV7()).String(), ObjectKey: t.ObjectKey, CreatedAt: now()}); err != nil {
					return err
				}
			}
			if err = q.DeleteText(ctx, t.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// StoredObjects lists each object a blob, text or ready preview references,
// including conflicting metadata for one key, so an archive can reject
// inconsistent files.
func (r *Repo) StoredObjects(ctx context.Context) ([]StoredObject, error) {
	return r.read.StoredObjects(ctx)
}
