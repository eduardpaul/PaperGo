// Package documentpreview renders a first-page WebP preview of an item's
// file as a background operation. Its module route, POST
// /v1/items/{id}/preview, starts it for the item's current file and
// answers with the pending operation; GET /v1/items/{id}/preview serves the
// preview. Previews are kept once per file bytes, so a file whose bytes
// were rendered before finishes at once.
package documentpreview

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/forms"
	"papergo/internal/httpapi"
	"papergo/internal/ocr"
	"papergo/internal/preview"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

// Kind is the operation kind.
const Kind = "document.preview"

// Renderer renders previews with the deployment's PDF renderer.
type Renderer struct {
	Storage storage.Store
	PDF     doctext.PDF
	// MaxPixels bounds decoded and rendered images; zero means ocr.MaxPixels.
	MaxPixels int64
}

// Result is a finished operation's result.
type Result struct {
	ItemID string `json:"item_id"`
	BlobID string `json:"blob_id"`
	// Ready says a preview exists; false means the file cannot be rendered.
	Ready bool `json:"ready"`
	// Reused says the bytes had been rendered before.
	Reused bool `json:"reused"`
}

// job is the operation's payload: the file version to render.
type job struct {
	ItemID      string `json:"item_id"`
	BlobID      string `json:"blob_id"`
	SHA256      string `json:"sha256"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
}

// Role is the workflow role whose filler renders previews.
const Role = "file.preview"

//go:embed builtin/documents.preview.ts
var builtinSource string

// Register makes s run the operation, and adds the documents.preview
// built-in workflow, which every collection runs by default, filling Role.
func (r *Renderer) Register(s *dms.Service) error {
	workflow.RegisterRole(&workflow.Role{Key: Role, Name: "File previews", Description: "Generates lightweight first-page previews asynchronously."})
	workflow.RegisterBuiltIn(&workflow.BuiltIn{Key: "documents.preview", Name: "Generate file previews", Role: Role, Default: true, System: true,
		Description:      "Generates bounded WebP previews when files are added or replaced. Also runs on demand.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`), Source: builtinSource})
	return s.RegisterOperationHandler(Kind, dms.OperationHandler{Scope: "document.write", Validate: validate, Execute: r.execute})
}

// Module serves POST /v1/items/{id}/preview.
func (r *Renderer) Module() httpapi.Module { return module{r} }

type module struct{ renderer *Renderer }

func (module) Name() string { return "documentpreview" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{{Method: http.MethodPost, Pattern: "/v1/items/{id}/preview", OperationID: "renderItemPreview", Handle: m.render}}
}

func (m module) render(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	op, err := Start(r.Context(), in.DMS, in.Subject, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

// Start renders the preview of the item's current file as subject, who
// needs write access to the item. The operation leases that file version.
func Start(ctx context.Context, s *dms.Service, subject, itemID string) (*dms.OperationView, error) {
	b, err := s.CurrentFile(ctx, subject, itemID)
	if err != nil {
		return nil, err
	}
	if !preview.Supported(string(b.ContentType)) {
		return nil, fmt.Errorf("%w: PaperGo does not render previews of %s files", storage.ErrUnsupportedFile, b.ContentType)
	}
	payload, err := json.Marshal(job{ItemID: itemID, BlobID: b.ID, SHA256: b.Sha256, ObjectKey: b.ObjectKey, ContentType: string(b.ContentType)})
	if err != nil {
		return nil, err
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, itemID, Kind, payload); err != nil || out.Replayed {
			return err
		}
		return t.LeaseOperationBlobs(ctx, out.ID, []string{b.ID})
	})
	return out, err
}

// validate runs in the transaction that enqueues the operation: the file
// must still be the item's current file.
func validate(ctx context.Context, t *dms.Service, subject, itemID string, payload json.RawMessage) error {
	var j job
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID {
		return dms.Invalid("invalid preview payload")
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

// execute renders the preview outside the database writer, unless the
// bytes were rendered before, and finishes the operation under its claim.
// A file that cannot be rendered succeeds with ready false, and records
// that so it is not rendered again.
func (r *Renderer) execute(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j job
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	result := Result{ItemID: j.ItemID, BlobID: j.BlobID}
	cached, err := s.FindPreview(ctx, j.SHA256)
	if err != nil {
		return err
	}
	var object storage.Object
	var width, height int
	if cached != nil {
		result.Ready, result.Reused = cached.Status == blobs.PreviewReady, true
	} else {
		if object, width, height, result.Ready, err = r.render(ctx, j); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		if cached == nil {
			if err := t.SavePreview(ctx, j.SHA256, object, width, height); err != nil {
				return err
			}
		}
		completed := time.Now().UTC()
		op.Status, op.Result, op.Progress, op.CompletedAt = operations.StatusSucceeded, raw, 100, &completed
		return t.SaveOperation(ctx, op)
	})
}

// render stores the preview of j's bytes. A file it cannot render reports
// ready false with a zero object, which SavePreview records as unavailable.
func (r *Renderer) render(ctx context.Context, j job) (storage.Object, int, int, bool, error) {
	f, err := r.Storage.Open(ctx, j.ObjectKey)
	if err != nil {
		return storage.Object{}, 0, 0, false, err
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return storage.Object{}, 0, 0, false, err
	}
	maxPixels := r.MaxPixels
	if maxPixels <= 0 {
		maxPixels = ocr.MaxPixels
	}
	image, err := preview.Generate(ctx, data, j.ContentType, r.PDF, maxPixels)
	if err != nil {
		if ctx.Err() != nil {
			return storage.Object{}, 0, 0, false, ctx.Err()
		}
		return storage.Object{}, 0, 0, false, nil
	}
	object, err := r.Storage.Put(ctx, bytes.NewReader(image.WebP), int64(len(image.WebP))+1)
	if err != nil {
		return storage.Object{}, 0, 0, false, errors.Join(errors.New("store the preview"), err)
	}
	return object, image.Width, image.Height, true, nil
}
