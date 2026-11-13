// Package documentfiles makes new files from items' files:
// POST /v1/items/{id}/compose-pdf combines photos into one PDF, searchable
// with their OCR text, and POST /v1/items/{id}/optimize-image makes a photo
// a JPEG. Both size and color their photos with the same options
// (compose.Options). Each is an operation whose result is an artifact, kept for reuse by
// identical requests whoever asks. A consumer stages an artifact with
// POST /v1/artifacts/{id}/stage to review it; POST
// /v1/staged-files/{id}/promote makes it the item's file and POST
// /v1/staged-files/{id}/discard drops the claim. A file staged by a
// workflow run answers only that run. POST /v1/items/{id}/content/reclaim
// frees the bytes of a file version a new file replaced, such as the photo
// a searchable PDF replaced.
package documentfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"papergo/internal/compose"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/model"
	"papergo/internal/ocr"
	"papergo/internal/storage"
	"papergo/internal/transfer"
)

// The operation kinds.
const (
	KindCompose  = "document.compose_pdf"
	KindOptimize = "document.optimize_image"
)

var (
	// ErrNoOCR refuses a text height or a text layer in a deployment without
	// an OCR engine.
	ErrNoOCR = errors.New("a text height and a PDF text layer need OCR; configure ONNXRUNTIME_LIB and OCR_MODELS")
	// errPhotoChanged fails a file made from photos that changed: the
	// reviewed file must show the photos as they are.
	errPhotoChanged = errors.New("a photo changed after the operation started; start a new one")
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
	if err := s.RegisterOperationHandler(KindCompose, dms.OperationHandler{Scope: "document.write", Validate: validate, Execute: m.compose}); err != nil {
		return err
	}
	return s.RegisterOperationHandler(KindOptimize, dms.OperationHandler{Scope: "document.write", Validate: validate, Execute: m.optimize})
}

// Module serves the routes.
func (m *Maker) Module() httpapi.Module { return module{m} }

type module struct{ m *Maker }

func (module) Name() string { return "documentfiles" }

func (h module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/compose-pdf", OperationID: "composeItemPDF", Handle: h.compose},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/optimize-image", OperationID: "optimizeItemImage", Handle: h.optimizeImage},
		{Method: http.MethodGet, Pattern: "/v1/artifacts/{id}", OperationID: "getArtifact", Handle: h.getArtifact},
		{Method: http.MethodGet, Pattern: "/v1/artifacts/{id}/content", OperationID: "getArtifactContent", Handle: h.artifactContent},
		{Method: http.MethodPost, Pattern: "/v1/artifacts/{id}/stage", OperationID: "stageArtifact", Handle: h.stage},
		{Method: http.MethodPost, Pattern: "/v1/staged-files/{id}/promote", OperationID: "promoteStagedFile", Handle: h.promote},
		{Method: http.MethodPost, Pattern: "/v1/staged-files/{id}/discard", OperationID: "discardStagedFile", Handle: h.discard},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/content/reclaim", OperationID: "reclaimFileVersion", Handle: h.reclaim},
	}
}

// job is an operation's payload: the item, the file versions its file is
// made from (one for an optimized image), and the key identical requests
// share.
type job struct {
	ItemID   string          `json:"item_id"`
	InputKey string          `json:"input_key"`
	Files    []file          `json:"files"`
	Compose  compose.Options `json:"compose"`
	// TextLayer puts the OCR text in a PDF.
	TextLayer bool `json:"text_layer,omitempty"`
}

type file struct {
	ItemID    string `json:"item_id"`
	BlobID    string `json:"blob_id"`
	Version   int    `json:"version"`
	ObjectKey string `json:"object_key"`
	Filename  string `json:"filename"`
}

func (h module) compose(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request ComposeRequest
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	op, err := h.m.StartCompose(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (h module) optimizeImage(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request OptimizeRequest
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	op, err := h.m.StartOptimize(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

// ComposeRequest names the photos to combine, in page order, and sizes and
// colors their pages; zero values take the defaults.
type ComposeRequest struct {
	compose.Options
	// ItemIDs are the photo items in page order, the item itself among
	// them; default the item alone.
	ItemIDs []string `json:"item_ids,omitempty"`
	// TextLayer puts the text OCR recognizes on each page in the PDF,
	// invisible, so it can be searched; default true.
	TextLayer *bool `json:"text_layer,omitempty"`
}

// ComposeResult is a finished composition's result: the PDF artifact.
// Reused says an identical request made it.
type ComposeResult struct {
	ArtifactID string          `json:"artifact_id"`
	Reused     bool            `json:"reused,omitempty"`
	ItemID     string          `json:"item_id"`
	Others     []string        `json:"others"`
	Pages      int             `json:"pages"`
	Bytes      int64           `json:"bytes"`
	Metrics    compose.Metrics `json:"metrics"`
}

// OptimizeRequest sizes and colors the image; zero values take the
// defaults.
type OptimizeRequest struct {
	compose.Options
}

// OptimizeResult is a finished optimization's result: the JPEG artifact.
// Reused says an identical request made it.
type OptimizeResult struct {
	ArtifactID string          `json:"artifact_id"`
	Reused     bool            `json:"reused,omitempty"`
	ItemID     string          `json:"item_id"`
	Bytes      int64           `json:"bytes"`
	Metrics    compose.Metrics `json:"metrics"`
}

// options are the request's options over the defaults.
func options(in compose.Options) (compose.Options, error) {
	o := in
	if o.Quality == 0 {
		o.Quality = compose.Defaults.Quality
	}
	if o.Color == "" {
		o.Color = compose.Defaults.Color
	}
	if err := o.Validate(); err != nil {
		return o, dms.Invalid(err.Error())
	}
	return o, nil
}

// photo is the current file of an item subject may write, which must be a
// JPEG, PNG or WebP photo.
func photo(ctx context.Context, s *dms.Service, subject, id string) (file, error) {
	b, err := s.CurrentFile(ctx, subject, id)
	if err != nil {
		return file{}, err
	}
	switch b.ContentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return file{}, dms.Invalid(b.Filename + " is not a JPEG, PNG or WebP photo")
	}
	return file{ItemID: id, BlobID: b.ID, Version: b.Version, ObjectKey: b.ObjectKey, Filename: b.Filename}, nil
}

// StartOptimize makes the item's current photo a JPEG artifact for the
// item, sized and colored by the request, as subject, who needs write
// access to the item.
func (m *Maker) StartOptimize(ctx context.Context, s *dms.Service, subject, itemID string, in OptimizeRequest) (*dms.OperationView, error) {
	o, err := options(in.Options)
	if err != nil {
		return nil, err
	}
	if m.OCR == nil && o.NeedsOCR() {
		return nil, dms.Invalid(ErrNoOCR.Error())
	}
	r, err := s.Authorize(ctx, subject, itemID, "write")
	if err != nil {
		return nil, err
	}
	if r.Kind != "item" {
		return nil, dms.Invalid("only an item's photo can be optimized")
	}
	f, err := photo(ctx, s, subject, itemID)
	if err != nil {
		return nil, err
	}
	return start(ctx, s, subject, KindOptimize, job{ItemID: itemID, Compose: o, Files: []file{f}})
}

// StartCompose combines the current files of photo items of the item's
// list or library into one PDF for the item, as subject, who needs write
// access to every photo.
func (m *Maker) StartCompose(ctx context.Context, s *dms.Service, subject, itemID string, in ComposeRequest) (*dms.OperationView, error) {
	o, err := options(in.Options)
	if err != nil {
		return nil, err
	}
	text := in.TextLayer == nil || *in.TextLayer
	if m.OCR == nil && (o.NeedsOCR() || text) {
		return nil, dms.Invalid(ErrNoOCR.Error())
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
	j := job{ItemID: itemID, Compose: o, TextLayer: text}
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
		f, err := photo(ctx, s, subject, id)
		if err != nil {
			return nil, err
		}
		j.Files = append(j.Files, f)
	}
	if !seen[itemID] {
		return nil, dms.Invalid("item_ids must include the item")
	}
	return start(ctx, s, subject, KindCompose, j)
}

func start(ctx context.Context, s *dms.Service, subject, kind string, j job) (*dms.OperationView, error) {
	j.InputKey = inputKey(kind, j)
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

// inputKey identifies what a file is made from and how: the operation kind,
// the item, the source file versions in order and the options.
func inputKey(kind string, j job) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode([]any{kind, j.ItemID, j.Compose, j.TextLayer})
	for _, f := range j.Files {
		_ = enc.Encode([]string{f.ItemID, f.BlobID})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// validate runs in the accepting transaction.
func validate(_ context.Context, _ *dms.Service, _, itemID string, payload json.RawMessage) error {
	var j job
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID || len(j.InputKey) != 64 || len(j.Files) == 0 {
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

// compose combines the photos outside the writer and stages the PDF.
func (m *Maker) compose(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	if a, err := s.ReusableArtifact(ctx, j.ItemID, j.InputKey); err != nil || a != nil {
		if err != nil {
			return err
		}
		res := ComposeResult{ArtifactID: a.ID, Reused: true, ItemID: j.ItemID, Others: others(j), Bytes: a.Size}
		if err = json.Unmarshal(a.Metrics, &res.Metrics); err != nil {
			return err
		}
		res.Pages = res.Metrics.Pages
		return finish(ctx, s, op, res)
	}
	if m.OCR == nil && (j.Compose.NeedsOCR() || j.TextLayer) {
		return ErrNoOCR
	}
	var doc bytes.Buffer
	metrics, err := compose.PDF(ctx, m.OCR, &doc, len(j.Files), func(i int) ([]byte, error) {
		data, err := m.read(ctx, j.Files[i].ObjectKey)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", j.Files[i].Filename, err)
		}
		return data, nil
	}, m.maxPixels(), j.Compose, j.TextLayer)
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
	res := ComposeResult{ItemID: j.ItemID, Others: others(j), Pages: metrics.Pages, Bytes: obj.Size, Metrics: metrics}
	sources := make([]model.ArtifactSource, len(j.Files))
	name := ""
	for i, f := range j.Files {
		sources[i] = model.ArtifactSource{ItemID: f.ItemID, BlobID: f.BlobID}
		if f.ItemID == j.ItemID {
			name = strings.TrimSuffix(f.Filename, path.Ext(f.Filename)) + ".pdf"
		}
	}
	return s.Write(ctx, func(t *dms.Service) error {
		a, err := t.AddArtifact(ctx, op.CreatedBy, dms.ArtifactInput{ItemID: j.ItemID, OperationID: op.ID, InputKey: j.InputKey, Sources: sources,
			ObjectKey: obj.Key, Filename: name, ContentType: "application/pdf", Size: obj.Size, SHA256: obj.SHA256, Metrics: raw})
		if errors.Is(err, dms.ErrConflict) {
			return errPhotoChanged
		}
		if err != nil {
			return err
		}
		res.ArtifactID = a.ID
		return save(ctx, t, op, res)
	})
}

// optimize makes the JPEG outside the writer and keeps it as an artifact.
func (m *Maker) optimize(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	if len(j.Files) != 1 {
		return errors.New("an optimized image is made from one photo")
	}
	if a, err := s.ReusableArtifact(ctx, j.ItemID, j.InputKey); err != nil || a != nil {
		if err != nil {
			return err
		}
		res := OptimizeResult{ArtifactID: a.ID, Reused: true, ItemID: j.ItemID, Bytes: a.Size}
		if err = json.Unmarshal(a.Metrics, &res.Metrics); err != nil {
			return err
		}
		return finish(ctx, s, op, res)
	}
	if m.OCR == nil && j.Compose.NeedsOCR() {
		return ErrNoOCR
	}
	f := j.Files[0]
	data, err := m.read(ctx, f.ObjectKey)
	if err != nil {
		return fmt.Errorf("read %s: %w", f.Filename, err)
	}
	out, metrics, err := compose.Image(ctx, m.OCR, data, m.maxPixels(), j.Compose)
	if err != nil {
		return fmt.Errorf("optimize image: %w", err)
	}
	obj, err := m.store(ctx, out)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(metrics)
	if err != nil {
		return err
	}
	res := OptimizeResult{ItemID: j.ItemID, Bytes: obj.Size, Metrics: metrics}
	name := strings.TrimSuffix(f.Filename, path.Ext(f.Filename)) + ".jpg"
	return s.Write(ctx, func(t *dms.Service) error {
		a, err := t.AddArtifact(ctx, op.CreatedBy, dms.ArtifactInput{ItemID: j.ItemID, OperationID: op.ID, InputKey: j.InputKey,
			Sources:   []model.ArtifactSource{{ItemID: f.ItemID, BlobID: f.BlobID}},
			ObjectKey: obj.Key, Filename: name, ContentType: "image/jpeg", Size: obj.Size, SHA256: obj.SHA256, Metrics: raw})
		if errors.Is(err, dms.ErrConflict) {
			return errPhotoChanged
		}
		if err != nil {
			return err
		}
		res.ArtifactID = a.ID
		return save(ctx, t, op, res)
	})
}

// others are the photo items other than the item a composition is for.
func others(j job) []string {
	out := []string{}
	for _, f := range j.Files {
		if f.ItemID != j.ItemID {
			out = append(out, f.ItemID)
		}
	}
	return out
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

// ArtifactView is an artifact as the API shows it.
type ArtifactView struct {
	ID          string                 `json:"id"`
	ItemID      string                 `json:"item_id"`
	OperationID string                 `json:"operation_id,omitempty"`
	Filename    string                 `json:"filename"`
	ContentType string                 `json:"content_type"`
	Size        int64                  `json:"size"`
	SHA256      string                 `json:"sha256"`
	Metrics     json.RawMessage        `json:"metrics"`
	Sources     []model.ArtifactSource `json:"sources"`
	CreatedAt   time.Time              `json:"created_at"`
	ExpiresAt   time.Time              `json:"expires_at"`
}

func (h module) getArtifact(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	a, err := in.DMS.Artifact(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, ArtifactView{ID: a.ID, ItemID: a.ItemID, OperationID: a.OperationID, Filename: a.Filename, ContentType: a.ContentType,
		Size: a.Size, SHA256: a.Sha256, Metrics: a.Metrics, Sources: a.Sources, CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt})
	return nil
}

// artifactContent serves an artifact's bytes inline, for review screens.
func (h module) artifactContent(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	a, err := in.DMS.Artifact(r.Context(), in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	file, err := h.m.Storage.Open(r.Context(), a.ObjectKey)
	if err != nil {
		return err
	}
	defer file.Close()
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": a.Filename}))
	transfer.StoredFile(w)
	w.Header().Set("ETag", strconv.Quote(a.Sha256))
	http.ServeContent(transfer.Writer(w), r, a.Filename, a.CreatedAt, file)
	return nil
}

// StagedResult is the staged file a consumer's staging made.
type StagedResult struct {
	ID         string `json:"id"`
	ArtifactID string `json:"artifact_id"`
	ItemID     string `json:"item_id"`
	RunID      string `json:"run_id,omitempty"`
}

// stage claims an artifact for the caller: for its workflow run when a run
// calls, otherwise for anyone with write access to the item.
func (h module) stage(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	f, err := in.DMS.StageArtifact(r.Context(), in.Subject, r.PathValue("id"), in.Run)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusCreated, StagedResult{ID: f.ID, ArtifactID: f.ArtifactID, ItemID: f.ItemID, RunID: f.RunID})
	return nil
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
	b, err := in.DMS.PromoteStagedFile(r.Context(), in.Subject, r.PathValue("id"), in.Run, request.RecycleSources)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, PromoteResult{ItemID: b.ItemID, BlobID: b.ID, FileVersion: b.Version})
	return nil
}

func (h module) discard(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
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
