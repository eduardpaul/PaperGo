package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"

	"papergo/internal/ocr"
	"papergo/internal/storage"
)

// Tools are what activities with heavy work use in their work steps,
// outside any transaction.
type Tools struct {
	Storage storage.Store
	// OCR reads text in images; nil when the deployment has no OCR.
	OCR ocr.Engine
	// MaxFileBytes bounds the files work steps produce; default 1 GiB.
	MaxFileBytes int64
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
