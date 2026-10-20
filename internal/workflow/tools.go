package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/resultcache"
	"papergo/internal/decision"
	"papergo/internal/doctext"
	"papergo/internal/ocr"
	"papergo/internal/search"
	"papergo/internal/storage"
)

// Tools are what activities with heavy work use in their work steps,
// outside any transaction.
type Tools struct {
	// Decisions is the single local decision engine shared by both actions.
	Decisions decision.Model
	Storage   storage.Store
	// OCR reads text in images; nil when the deployment has no OCR.
	OCR ocr.Engine
	// PDF reads PDFs' text layers and renders their pages for OCR; nil
	// leaves PDFs unread.
	PDF doctext.PDF
	// Search is the search store the search.* activities write file
	// content to; nil when search is not configured.
	Search search.Store
	// Embeddings is the embedding model search.embed and the sweep use; nil
	// when semantic search is off.
	Embeddings ai.Embedder
	// Records holds durable activity inputs and coordinator state.
	Records *activitystore.Store
	// Extensions are module dependencies supplied by the host before execution.
	Extensions map[string]any
	// Now is the clock activities use. Empty means time.Now.
	Now func() time.Time
	// Results holds reusable action results outside domain transactions.
	Results resultcache.Cache
	// ResultTTL is the idle lifetime of reusable activity results.
	ResultTTL time.Duration
	// Languages are the languages search stems (SEARCH_LANGUAGES), among
	// which document.read_text detects a text's language.
	Languages []string
	// MaxFileBytes bounds the files work steps produce; default 1 GiB.
	MaxFileBytes int64
	// MaxPixels bounds the images work steps decode; default ocr.MaxPixels.
	MaxPixels int64
}

func (t Tools) maxPixels() int64 {
	if t.MaxPixels > 0 {
		return t.MaxPixels
	}
	return ocr.MaxPixels
}

// Clock is the time activities use. Tests set Now; otherwise it is time.Now.
func (t Tools) Clock() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// ErrNoOCR fails OCR activities in a deployment without an OCR engine.
var ErrNoOCR = errors.New("OCR is not available in this deployment; configure ONNXRUNTIME_LIB and OCR_MODELS")

// workEnv is what an activity's work step can use.
type workEnv struct {
	ctx   context.Context
	tools Tools
	runID string
}

// workGate lets one heavy work step of the process decode, analyze and
// encode files at a time, bounding CPU and memory however many runs queue.
var workGate = make(chan struct{}, 1)

// exclusive runs fn holding the process's work gate.
func (w *workEnv) exclusive(fn func() error) error {
	select {
	case workGate <- struct{}{}:
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
	defer func() { <-workGate }()
	return fn()
}

// read returns a stored object's bytes.
func (w *workEnv) read(key string) ([]byte, error) {
	if w.tools.Storage == nil {
		return nil, errors.New("file storage is not available to workflows")
	}
	f, err := w.tools.Storage.Open(w.ctx, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// store writes bytes as a new object; a staged file records it in commit.
// An object no commit records is removed by the storage collector.
func (w *workEnv) store(data []byte) (storage.Object, error) {
	if w.tools.Storage == nil {
		return storage.Object{}, errors.New("file storage is not available to workflows")
	}
	limit := w.tools.MaxFileBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	return w.tools.Storage.Put(w.ctx, bytes.NewReader(data), limit)
}
