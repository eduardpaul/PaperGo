// Package documentfiles makes new files from items' files for a workflow
// run to review: POST /v1/items/{id}/optimize-image proposes a smaller
// copy of an image, and POST /v1/items/{id}/compose-pdf combines photos
// into one searchable PDF. Each is an operation that stages its file for
// the run; POST /v1/staged-files/{id}/promote makes it the item's file and
// POST /v1/staged-files/{id}/discard drops it. Staged files belong to the
// run that made them, so these routes answer only workflow runs.
// POST /v1/items/{id}/content/reclaim frees the bytes of a file version a
// smaller copy replaced.
package documentfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"papergo/internal/compose"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/model"
	"papergo/internal/ocr"
	"papergo/internal/optimize"
	"papergo/internal/storage"
)

// Operation kinds.
const (
	KindOptimize = "document.optimize_image"
	KindCompose  = "document.compose_pdf"
)

// Reasons an optimization keeps the original without analyzing it.
const (
	ReasonSuperseded  = "A newer file replaced the one the workflow was started for."
	ReasonUnsupported = "Only JPEG, PNG and WebP images are optimized."
	ReasonChanged     = "The file changed while it was analyzed."
)

var (
	// ErrNoRun refuses staging outside a workflow run.
	ErrNoRun = errors.New("staged files belong to workflow runs; call this from a workflow script")
	// ErrNoOCR refuses analysis in a deployment without an OCR engine.
	ErrNoOCR = errors.New("image analysis needs OCR; configure ONNXRUNTIME_LIB and OCR_MODELS")
	// errPhotoChanged fails a composition whose photos changed: the
	// reviewed PDF must show the photos as they are.
	errPhotoChanged = errors.New("a photo changed after the composition started; start a new one")
)

// Maker makes files with the deployment's OCR engine.
type Maker struct {
	Storage storage.Store
	OCR     ocr.Engine
	// MaxPixels bounds decoded images; zero means ocr.MaxPixels.
	MaxPixels int64
	// MaxFileBytes bounds the files made; zero means 1 GiB.
	MaxFileBytes int64
}

// Register makes s run the operations.
func (m *Maker) Register(s *dms.Service) error {
	if err := s.RegisterOperationHandler(KindOptimize, dms.OperationHandler{Scope: "document.write", Validate: validate, Execute: m.optimize}); err != nil {
		return err
	}
	return s.RegisterOperationHandler(KindCompose, dms.OperationHandler{Scope: "document.write", Validate: validate, Execute: m.compose})
}

// Module serves the routes.
func (m *Maker) Module() httpapi.Module { return module{m} }

type module struct{ m *Maker }

func (module) Name() string { return "documentfiles" }

func (h module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/optimize-image", OperationID: "optimizeItemImage", Handle: h.optimize},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/compose-pdf", OperationID: "composeItemPDF", Handle: h.compose},
		{Method: http.MethodPost, Pattern: "/v1/staged-files/{id}/promote", OperationID: "promoteStagedFile", Handle: h.promote},
		{Method: http.MethodPost, Pattern: "/v1/staged-files/{id}/discard", OperationID: "discardStagedFile", Handle: h.discard},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/content/reclaim", OperationID: "reclaimFileVersion", Handle: h.reclaim},
	}
}

// OptimizeRequest tunes an image optimization; zero values take the
// defaults. FileVersion, when set, is the file version the workflow was
// started for: a newer one is not optimized.
type OptimizeRequest struct {
	optimize.Options
	FileVersion int `json:"file_version,omitempty"`
}

// OptimizeResult is a finished optimization's result: the staged WebP, or
// the reason the original is kept.
type OptimizeResult struct {
	StagedFileID string           `json:"staged_file_id,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	ItemID       string           `json:"item_id"`
	BlobID       string           `json:"blob_id"`
	FileVersion  int              `json:"file_version"`
	Metrics      optimize.Metrics `json:"metrics"`
}

// job is an operation's payload: the run that stages the file, and the
// file versions it is made from, the item's first.
type job struct {
	Run      string           `json:"run"`
	ItemID   string           `json:"item_id"`
	Files    []file           `json:"files"`
	Optimize optimize.Options `json:"optimize,omitempty"`
	Compose  compose.Options  `json:"compose,omitempty"`
	// Reason keeps the original without analyzing it.
	Reason string `json:"reason,omitempty"`
}

type file struct {
	ItemID    string `json:"item_id"`
	BlobID    string `json:"blob_id"`
	Version   int    `json:"version"`
	ObjectKey string `json:"object_key"`
	Filename  string `json:"filename"`
}

func (h module) optimize(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request OptimizeRequest
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	op, err := h.m.StartOptimize(r.Context(), in.DMS, in.Subject, in.Run, r.PathValue("id"), request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

// StartOptimize proposes a smaller WebP copy of the item's current image
// for run, as subject.
func (m *Maker) StartOptimize(ctx context.Context, s *dms.Service, subject, run, itemID string, in OptimizeRequest) (*dms.OperationView, error) {
	if run == "" {
		return nil, dms.Invalid(ErrNoRun.Error())
	}
	o := optimize.Defaults
	if in.TargetHeight != 0 {
		o.TargetHeight = in.TargetHeight
	}
	if in.MinConfidence != 0 {
		o.MinConfidence = in.MinConfidence
	}
	if in.Percentile != 0 {
		o.Percentile = in.Percentile
	}
	if in.MaxAnalysisDimension != 0 {
		o.MaxAnalysisDimension = in.MaxAnalysisDimension
	}
	if in.MinimumScale != 0 {
		o.MinimumScale = in.MinimumScale
	}
	if in.Quality != 0 {
		o.Quality = in.Quality
	}
	if err := o.Validate(); err != nil {
		return nil, dms.Invalid(err.Error())
	}
	b, err := s.CurrentFile(ctx, subject, itemID)
	if err != nil {
		return nil, err
	}
	j := job{Run: run, ItemID: itemID, Optimize: o, Files: []file{{ItemID: itemID, BlobID: b.ID, Version: b.Version, ObjectKey: b.ObjectKey, Filename: b.Filename}}}
	switch {
	case in.FileVersion != 0 && in.FileVersion != b.Version:
		j.Reason = ReasonSuperseded
	case b.ContentType != "image/jpeg" && b.ContentType != "image/png" && b.ContentType != "image/webp":
		j.Reason = ReasonUnsupported
	case m.OCR == nil:
		return nil, dms.Invalid(ErrNoOCR.Error())
	}
	return start(ctx, s, subject, KindOptimize, j)
}

func (h module) compose(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request ComposeRequest
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	op, err := h.m.StartCompose(r.Context(), in.DMS, in.Subject, in.Run, r.PathValue("id"), request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

// ComposeRequest names the photos to combine, in page order, and tunes the
// PDF; zero values take the defaults.
type ComposeRequest struct {
	compose.Options
	// ItemIDs are the photo items in page order, the item itself among
	// them; default the item alone.
	ItemIDs []string `json:"item_ids,omitempty"`
}

// ComposeResult is a finished composition's result.
type ComposeResult struct {
	StagedFileID string          `json:"staged_file_id"`
	ItemID       string          `json:"item_id"`
	Others       []string        `json:"others"`
	Pages        int             `json:"pages"`
	Bytes        int64           `json:"bytes"`
	Metrics      compose.Metrics `json:"metrics"`
}

// StartCompose combines the current files of photo items of the item's
// list or library into one PDF staged for the item, for run, as subject,
// who needs write access to every photo.
func (m *Maker) StartCompose(ctx context.Context, s *dms.Service, subject, run, itemID string, in ComposeRequest) (*dms.OperationView, error) {
	if run == "" {
		return nil, dms.Invalid(ErrNoRun.Error())
	}
	if m.OCR == nil {
		return nil, dms.Invalid(ErrNoOCR.Error())
	}
	o := compose.Defaults
	if in.TargetHeight != 0 {
		o.TargetHeight = in.TargetHeight
	}
	if in.Quality != 0 {
		o.Quality = in.Quality
	}
	if err := o.Validate(); err != nil {
		return nil, dms.Invalid(err.Error())
	}
	ids := in.ItemIDs
	if len(ids) == 0 {
		ids = []string{itemID}
	}
	if len(ids) > compose.MaxPages {
		return nil, dms.Invalid(fmt.Sprintf("a PDF combines at most %d photos", compose.MaxPages))
	}
	primary, err := s.Authorize(ctx, subject, itemID, "write")
	if err != nil {
		return nil, err
	}
	j := job{Run: run, ItemID: itemID, Compose: o}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, dms.Invalid("item_ids repeats " + id)
		}
		seen[id] = true
		r, err := s.Authorize(ctx, subject, id, "write")
		if err != nil {
			return nil, err
		}
		if r.Kind != "item" || r.ContainerID == nil || primary.ContainerID == nil || *r.ContainerID != *primary.ContainerID {
			return nil, dms.Invalid("every photo must be an item of the item's list or library")
		}
		b, err := s.CurrentFile(ctx, subject, id)
		if err != nil {
			return nil, err
		}
		switch b.ContentType {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return nil, dms.Invalid(b.Filename + " is not a JPEG, PNG or WebP photo")
		}
		j.Files = append(j.Files, file{ItemID: id, BlobID: b.ID, Version: b.Version, ObjectKey: b.ObjectKey, Filename: b.Filename})
	}
	if !seen[itemID] {
		return nil, dms.Invalid("item_ids must include the item")
	}
	return start(ctx, s, subject, KindCompose, j)
}

func start(ctx context.Context, s *dms.Service, subject, kind string, j job) (*dms.OperationView, error) {
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	blobs := make([]string, len(j.Files))
	for i, f := range j.Files {
		blobs[i] = f.BlobID
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, j.ItemID, kind, payload); err != nil || out.Replayed {
			return err
		}
		return t.LeaseOperationBlobs(ctx, out.ID, blobs)
	})
	return out, err
}

// validate runs in the accepting transaction.
func validate(_ context.Context, _ *dms.Service, _, itemID string, payload json.RawMessage) error {
	var j job
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID || j.Run == "" || len(j.Files) == 0 {
		return dms.Invalid("invalid file payload")
	}
	return nil
}

func (m *Maker) read(ctx context.Context, key string) ([]byte, error) {
	f, err := m.Storage.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (m *Maker) store(ctx context.Context, data []byte) (storage.Object, error) {
	limit := m.MaxFileBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	return m.Storage.Put(ctx, bytes.NewReader(data), limit)
}

func (m *Maker) maxPixels() int64 {
	if m.MaxPixels > 0 {
		return m.MaxPixels
	}
	return ocr.MaxPixels
}

// optimize analyzes the image outside the writer and stages the smaller
// copy, unless the original is kept.
func (m *Maker) optimize(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	f := j.Files[0]
	res := OptimizeResult{ItemID: j.ItemID, BlobID: f.BlobID, FileVersion: f.Version, Reason: j.Reason}
	if res.Reason != "" {
		return finish(ctx, s, op, res)
	}
	if m.OCR == nil {
		return ErrNoOCR
	}
	data, err := m.read(ctx, f.ObjectKey)
	if err != nil {
		return err
	}
	r, err := optimize.Image(ctx, m.OCR, data, m.maxPixels(), j.Optimize)
	if err != nil {
		return fmt.Errorf("optimize %s: %w", f.Filename, err)
	}
	res.Reason, res.Metrics = r.Reason, r.Metrics
	if r.WebP == nil {
		return finish(ctx, s, op, res)
	}
	obj, err := m.store(ctx, r.WebP)
	if err != nil {
		return err
	}
	metrics, err := json.Marshal(r.Metrics)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(f.Filename, path.Ext(f.Filename)) + ".webp"
	return s.Write(ctx, func(t *dms.Service) error {
		staged, err := t.StageFile(ctx, op.CreatedBy, dms.StageInput{ItemID: j.ItemID, RunID: j.Run, Sources: []model.StagedSource{{ItemID: j.ItemID, BlobID: f.BlobID}},
			ObjectKey: obj.Key, Filename: name, ContentType: "image/webp", Size: obj.Size, SHA256: obj.SHA256, Metrics: metrics})
		switch {
		case errors.Is(err, dms.ErrConflict):
			// The stored copy is freed by the storage collector.
			res.Reason = ReasonChanged
		case err != nil:
			return err
		default:
			res.StagedFileID = staged.ID
		}
		return save(ctx, t, op, res)
	})
}

// compose combines the photos outside the writer and stages the PDF.
func (m *Maker) compose(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	if m.OCR == nil {
		return ErrNoOCR
	}
	var doc bytes.Buffer
	metrics, err := compose.PDF(ctx, m.OCR, &doc, len(j.Files), func(i int) ([]byte, error) {
		data, err := m.read(ctx, j.Files[i].ObjectKey)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", j.Files[i].Filename, err)
		}
		return data, nil
	}, m.maxPixels(), j.Compose)
	if err != nil {
		return fmt.Errorf("compose PDF: %w", err)
	}
	obj, err := m.store(ctx, doc.Bytes())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	res := ComposeResult{ItemID: j.ItemID, Others: []string{}, Pages: metrics.Pages, Bytes: obj.Size, Metrics: metrics}
	sources := make([]model.StagedSource, len(j.Files))
	name := ""
	for i, f := range j.Files {
		sources[i] = model.StagedSource{ItemID: f.ItemID, BlobID: f.BlobID}
		if f.ItemID == j.ItemID {
			name = strings.TrimSuffix(f.Filename, path.Ext(f.Filename)) + ".pdf"
		} else {
			res.Others = append(res.Others, f.ItemID)
		}
	}
	return s.Write(ctx, func(t *dms.Service) error {
		staged, err := t.StageFile(ctx, op.CreatedBy, dms.StageInput{ItemID: j.ItemID, RunID: j.Run, Sources: sources,
			ObjectKey: obj.Key, Filename: name, ContentType: "application/pdf", Size: obj.Size, SHA256: obj.SHA256, Metrics: raw})
		if errors.Is(err, dms.ErrConflict) {
			return errPhotoChanged
		}
		if err != nil {
			return err
		}
		res.StagedFileID = staged.ID
		return save(ctx, t, op, res)
	})
}

func finish(ctx context.Context, s *dms.Service, op *operations.Operation, result any) error {
	return s.Write(ctx, func(t *dms.Service) error { return save(ctx, t, op, result) })
}

// save ends op with result in t's write.
func save(ctx context.Context, t *dms.Service, op *operations.Operation, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	completed := time.Now().UTC()
	op.Status, op.Result, op.CompletedAt = operations.StatusSucceeded, raw, &completed
	return t.SaveOperation(ctx, op)
}

// PromoteResult is the file version a promotion made.
type PromoteResult struct {
	ItemID      string `json:"item_id"`
	BlobID      string `json:"blob_id"`
	FileVersion int    `json:"file_version"`
}

func (h module) promote(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request struct {
		// RecycleSources moves the other source items to the recycle bin
		// with the promotion.
		RecycleSources bool `json:"recycle_sources"`
	}
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	if in.Run == "" {
		return dms.Invalid(ErrNoRun.Error())
	}
	b, err := in.DMS.PromoteStagedFile(r.Context(), in.Subject, r.PathValue("id"), in.Run, request.RecycleSources)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, PromoteResult{ItemID: b.ItemID, BlobID: b.ID, FileVersion: b.Version})
	return nil
}

func (h module) discard(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	if in.Run == "" {
		return dms.Invalid(ErrNoRun.Error())
	}
	if err := in.DMS.DiscardStagedFile(r.Context(), in.Subject, r.PathValue("id"), in.Run); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ReclaimResult says whether a file version's bytes will be freed.
type ReclaimResult struct {
	BlobID       string     `json:"blob_id"`
	Availability string     `json:"availability"`
	Deferred     bool       `json:"deferred"`
	Reason       string     `json:"reason,omitempty"`
	ReclaimAfter *time.Time `json:"reclaim_after,omitempty"`
}

// reclaim asks to free a file version's bytes after the grace period. The
// version keeps its number, filename, media type, size and checksum.
// Current, published and staged files defer it. Asking again does not move
// the deadline.
func (h module) reclaim(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request struct {
		BlobID string `json:"blob_id"`
	}
	if !in.Decode(w, r, &request) {
		return nil
	}
	if request.BlobID == "" {
		return dms.Invalid("blob_id is required")
	}
	out, err := in.DMS.ReclaimBlob(r.Context(), in.Subject, r.PathValue("id"), request.BlobID)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, ReclaimResult{BlobID: out.BlobID, Availability: out.Availability, Deferred: out.Deferred, Reason: out.Reason, ReclaimAfter: out.ReclaimAfter})
	return nil
}
