// Package documenttext reads the text of an item's file as a background
// operation: the text layer of PDF pages, and OCR of pages without one and
// of images. POST /v1/items/{id}/text starts it for the item's current file
// and answers with the pending operation; the text is kept once per file
// bytes and settings, where GET /v1/items/{id}/text reads it.
package documenttext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/ocr"
	"papergo/internal/storage"
)

// Kind is the operation kind.
const Kind = "document.text"

// Reader reads document text with the deployment's PDF reader and OCR
// engine. OCR is nil in a deployment without one.
type Reader struct {
	Storage storage.Store
	PDF     doctext.PDF
	OCR     ocr.Engine
	// Languages are the languages detected among (SEARCH_LANGUAGES).
	Languages []string
	// MaxPixels bounds decoded and rendered images; zero means ocr.MaxPixels.
	MaxPixels int64
	// MaxTextBytes bounds the stored page texts; zero means 1 GiB.
	MaxTextBytes int64
}

// Request chooses how text is read.
type Request struct {
	// OCR is auto (default), always or never.
	OCR string `json:"ocr,omitempty"`
	// MaxPages bounds the pages read, 1 to 2000 (default 500).
	MaxPages int `json:"max_pages,omitempty"`
	// Languages are OCR language codes joined with + (eng+deu); default the
	// file's languages.
	Languages string `json:"languages,omitempty"`
}

// Result is a finished operation's result.
type Result struct {
	TextID       string `json:"text_id"`
	ItemID       string `json:"item_id"`
	BlobID       string `json:"blob_id"`
	Pages        int    `json:"pages"`
	Chars        int    `json:"chars"`
	OCRPages     int    `json:"ocr_pages"`
	SkippedPages int    `json:"skipped_pages"`
	Truncated    bool   `json:"truncated"`
	Language     string `json:"language,omitempty"`
	// Reused says the text had been read before with the same settings.
	Reused bool `json:"reused"`
}

// ErrNoOCR refuses OCR in a deployment without an OCR engine.
var ErrNoOCR = errors.New("OCR is not available in this deployment; configure ONNXRUNTIME_LIB and OCR_MODELS")

// job is the operation's payload: the file version to read and how.
type job struct {
	ItemID      string          `json:"item_id"`
	BlobID      string          `json:"blob_id"`
	SHA256      string          `json:"sha256"`
	ObjectKey   string          `json:"object_key"`
	ContentType string          `json:"content_type"`
	Settings    string          `json:"settings"`
	Options     doctext.Options `json:"options"`
}

// Register makes s run the operation.
func (r *Reader) Register(s *dms.Service) error {
	return s.RegisterOperationHandler(Kind, dms.OperationHandler{Scope: "document.write", Validate: r.validate, ExternalStep: r.step})
}

// Start reads the text of the item's current file as subject, who needs
// write access to the item. The operation reads that file version even if
// the item gets another file meanwhile.
func (r *Reader) Start(ctx context.Context, s *dms.Service, subject, itemID string, in Request) (*dms.OperationView, error) {
	o := doctext.Options{OCR: in.OCR, MaxPages: in.MaxPages, MaxPixels: r.MaxPixels, Languages: r.Languages}
	if o.OCR == "" {
		o.OCR = doctext.OCRAuto
	}
	switch o.OCR {
	case doctext.OCRAuto, doctext.OCRNever:
	case doctext.OCRAlways:
		if r.OCR == nil {
			return nil, dms.Invalid(ErrNoOCR.Error())
		}
	default:
		return nil, dms.Invalid("ocr must be auto, always or never")
	}
	if o.MaxPages == 0 {
		o.MaxPages = 500
	}
	if o.MaxPages < 1 || o.MaxPages > 2000 {
		return nil, dms.Invalid("max_pages must be from 1 to 2000")
	}
	if o.MaxPixels <= 0 {
		o.MaxPixels = ocr.MaxPixels
	}
	b, err := s.CurrentFile(ctx, subject, itemID)
	if err != nil {
		return nil, err
	}
	o.Hints = b.Languages
	if hints := strings.TrimSpace(in.Languages); hints != "" {
		o.Hints = hints
	}
	payload, err := json.Marshal(job{ItemID: itemID, BlobID: b.ID, SHA256: b.Sha256, ObjectKey: b.ObjectKey, ContentType: string(b.ContentType),
		Settings: doctext.Settings(o, r.PDF, r.OCR), Options: o})
	if err != nil {
		return nil, err
	}
	return s.EnqueueOperation(ctx, subject, itemID, Kind, payload)
}

// validate runs in the transaction that enqueues the operation: the file
// must still be the item's current file.
func (r *Reader) validate(ctx context.Context, t *dms.Service, subject, itemID string, payload json.RawMessage) error {
	var j job
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID {
		return dms.Invalid("invalid document text payload")
	}
	b, err := t.CurrentFile(ctx, subject, itemID)
	if err != nil {
		return err
	}
	if b.ID != j.BlobID {
		return dms.ErrConflict
	}
	return nil
}

// step reads the text outside the database writer, unless it was read
// before with the same settings, and finishes the operation. A file whose
// text cannot be read fails it.
func (r *Reader) step(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	row, err := s.DAL.Blobs().FindText(ctx, j.SHA256, j.Settings)
	if err != nil {
		return err
	}
	reused := row != nil
	if row == nil {
		if row, err = r.read(ctx, s, j); err != nil {
			return err
		}
	}
	result, err := json.Marshal(Result{TextID: row.ID, ItemID: j.ItemID, BlobID: j.BlobID, Pages: row.Pages, Chars: row.Chars, OCRPages: row.OcrPages,
		SkippedPages: row.SkippedPages, Truncated: row.Truncated, Language: row.Language, Reused: reused})
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		current, err := t.DAL.Operations().Find(ctx, op.ID)
		if err != nil || current.Status != operations.StatusPending {
			return err
		}
		completed := time.Now().UTC()
		current.Status, current.Result, current.Progress, current.Processed, current.CompletedAt = operations.StatusSucceeded, result, 100, row.Pages, &completed
		return t.DAL.Operations().Save(ctx, &current)
	})
}

func (r *Reader) read(ctx context.Context, s *dms.Service, j job) (*blobs.Text, error) {
	f, err := r.Storage.Open(ctx, j.ObjectKey)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	text, err := doctext.Extract(ctx, data, j.ContentType, j.Options, r.PDF, r.OCR)
	if err != nil {
		return nil, fmt.Errorf("read the text of %s: %w", j.BlobID, err)
	}
	pages, err := json.Marshal(struct {
		Pages []doctext.Page `json:"pages"`
	}{text.Pages})
	if err != nil {
		return nil, err
	}
	limit := r.MaxTextBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	obj, err := r.Storage.Put(ctx, bytes.NewReader(pages), limit)
	if err != nil {
		return nil, err
	}
	extractor := doctext.Version
	if r.PDF != nil {
		extractor += "+" + r.PDF.Name()
	}
	var models json.RawMessage
	if r.OCR != nil && j.Options.OCR != doctext.OCRNever {
		if models, err = json.Marshal(r.OCR.Info().Models); err != nil {
			return nil, err
		}
	}
	var row *blobs.Text
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		row, err = t.DAL.Blobs().SaveText(ctx, blobs.TextInput{SHA256: j.SHA256, Settings: j.Settings, ObjectKey: obj.Key, ObjectSize: obj.Size, ObjectSHA256: obj.SHA256,
			Pages: len(text.Pages), Chars: text.Chars, OCRPages: text.OCRPages, SkippedPages: text.Skipped, Truncated: text.Truncated, Language: text.Language,
			Extractor: extractor, Models: models})
		return err
	})
	return row, err
}
