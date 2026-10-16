package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/model"
	"papergo/internal/optimize"
	"papergo/internal/storage"
)

// file.optimize_image proposes a smaller copy of an item's image: it stages
// a WebP file whose smallest text keeps a target height (see package
// optimize), for an approval to review and file.promote to apply. It follows
// skipped, with the reason, when it keeps the original.

const (
	portCandidate = "candidate"
	portSkipped   = "skipped"
)

// Reasons file.optimize_image skips without analyzing.
const (
	reasonSuperseded  = "A newer file replaced the one this run was started for."
	reasonUnsupported = "Only JPEG, PNG and WebP images are optimized."
	reasonChanged     = "The file changed while it was analyzed."
)

func init() {
	register(&Activity{Key: "file.optimize_image", Kind: "action", Ports: []string{portCandidate, portSkipped},
		Description: "Analyzes the text of the item's current image with OCR and stages a smaller WebP copy whose smallest text keeps the target height, then follows candidate. Follows skipped, with the reason, when it keeps the original: the format is not supported, the image is animated, has no reliable text or would not get smaller, or a newer file replaced the one the run started for.",
		InputSchema: inputs(itemIDProp + `,
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text keeps, 4 to 200.", "minimum": 4, "maximum": 200, "default": 12},
			"min_confidence": {"type": "number", "title": "Minimum confidence", "description": "Detector score a text line needs, 0 to 100.", "minimum": 0, "maximum": 100, "default": 50},
			"percentile": {"type": "number", "title": "Height percentile", "description": "Percentile of the text heights taken as the smallest text, 0 to 100; a low one ignores a few tiny false detections.", "minimum": 0, "maximum": 100, "default": 5},
			"max_analysis_dimension": {"type": "integer", "title": "Analysis size", "description": "Longest side in pixels of the copy the detector analyzes, 256 to 2600.", "minimum": 256, "maximum": 2600, "default": 2600},
			"minimum_scale": {"type": "number", "title": "Minimum scale", "description": "How far the image may shrink, 0.01 to 1.", "minimum": 0.01, "maximum": 1, "default": 0.05},
			"quality": {"type": "integer", "title": "WebP quality", "minimum": 1, "maximum": 100, "default": 80}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"staged_file_id": {"type": "string", "description": "The staged WebP file (candidate)."},
			"reason": {"type": "string", "description": "Why the original is kept (skipped)."},
			"item_id": {"type": "string"}, "blob_id": {"type": "string", "description": "The analyzed file version."}, "file_version": {"type": "integer"},
			"metrics": {"type": "object", "description": "The analysis: text_regions, smallest_text_height, scale, sizes and settings."}}}`),
		run: runOptimizeImage, work: workOptimizeImage, commit: commitOptimizeImage})
}

// optimizeJob is file.optimize_image's work: the file version to analyze.
type optimizeJob struct {
	ItemID      string           `json:"item_id"`
	BlobID      string           `json:"blob_id"`
	FileVersion int              `json:"file_version"`
	ObjectKey   string           `json:"object_key"`
	Filename    string           `json:"filename"`
	Options     optimize.Options `json:"options"`
}

// optimizeResult is the work's result: a stored WebP file, or the reason
// the original is kept.
type optimizeResult struct {
	Reason    string           `json:"reason,omitempty"`
	ObjectKey string           `json:"object_key,omitempty"`
	Size      int64            `json:"size,omitempty"`
	SHA256    string           `json:"sha256,omitempty"`
	Metrics   optimize.Metrics `json:"metrics"`
}

func runOptimizeImage(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	o := optimize.Defaults
	for name, field := range map[string]*float64{"target_height": &o.TargetHeight, "min_confidence": &o.MinConfidence, "percentile": &o.Percentile, "minimum_scale": &o.MinimumScale} {
		if err = e.number(in, name, field); err != nil {
			return outcome{}, err
		}
	}
	for name, field := range map[string]*int{"max_analysis_dimension": &o.MaxAnalysisDimension, "quality": &o.Quality} {
		f := float64(*field)
		if err = e.number(in, name, &f); err != nil {
			return outcome{}, err
		}
		if f != float64(int(f)) {
			return outcome{}, fmt.Errorf("%s must be a whole number", name)
		}
		*field = int(f)
	}
	if err = o.Validate(); err != nil {
		return outcome{}, err
	}
	b, err := e.t.CurrentFile(e.ctx, e.author, itemID)
	if err != nil {
		return outcome{}, err
	}
	out := map[string]any{"item_id": itemID, "blob_id": b.ID, "file_version": b.Version}
	skip := func(reason string) (outcome, error) {
		out["reason"] = reason
		return outcome{Port: portSkipped, Output: out}, nil
	}
	// A run a superseded upload started must not optimize its replacement.
	if trigger := e.scope.trigger; (trigger["type"] == dms.EventFileAdded || trigger["type"] == dms.EventFileUpdated) && trigger["resource_id"] == itemID {
		data, _ := trigger["data"].(map[string]any)
		if v, ok := forms.Number(data["file_version"]); ok && int(v) != b.Version {
			return skip(reasonSuperseded)
		}
	}
	switch b.ContentType {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return skip(reasonUnsupported)
	}
	job, err := json.Marshal(optimizeJob{ItemID: itemID, BlobID: b.ID, FileVersion: b.Version, ObjectKey: b.ObjectKey, Filename: b.Filename, Options: o})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: job, BlobIDs: []string{b.ID}, Output: out}, nil
}

// number reads an optional numeric input, literal or token, into v.
func (e *env) number(in map[string]any, name string, v *float64) error {
	raw, ok := in[name]
	if !ok || raw == nil {
		return nil
	}
	r, err := e.scope.resolve(raw)
	if err != nil {
		return err
	}
	if s, isText := r.(string); isText {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return fmt.Errorf("%s must be a number", name)
		}
		*v = f
		return nil
	}
	f, ok := forms.Number(r)
	if !ok {
		return fmt.Errorf("%s must be a number", name)
	}
	*v = f
	return nil
}

func workOptimizeImage(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job optimizeJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.OCR == nil {
		return nil, ErrNoOCR
	}
	var res optimizeResult
	err := w.exclusive(func() error {
		data, err := w.read(job.ObjectKey)
		if err != nil {
			return err
		}
		r, err := optimize.Image(w.ctx, w.tools.OCR, data, w.tools.maxPixels(), job.Options)
		if err != nil {
			return err
		}
		res = optimizeResult{Reason: r.Reason, Metrics: r.Metrics}
		if r.WebP == nil {
			return nil
		}
		obj, err := w.store(r.WebP)
		if err != nil {
			return err
		}
		res.ObjectKey, res.Size, res.SHA256 = obj.Key, obj.Size, obj.SHA256
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("optimize %s: %w", job.Filename, err)
	}
	return json.Marshal(res)
}

func commitOptimizeImage(e *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job optimizeJob
	var res optimizeResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	out := map[string]any{"item_id": job.ItemID, "blob_id": job.BlobID, "file_version": job.FileVersion, "metrics": res.Metrics}
	if res.Reason != "" {
		out["reason"] = res.Reason
		return outcome{Port: portSkipped, Output: out}, nil
	}
	name := strings.TrimSuffix(job.Filename, path.Ext(job.Filename)) + ".webp"
	id, err := e.stage(job.ItemID, []model.StagedSource{{ItemID: job.ItemID, BlobID: job.BlobID}},
		storage.Object{Key: res.ObjectKey, Size: res.Size, SHA256: res.SHA256}, name, "image/webp", res.Metrics)
	if errors.Is(err, dms.ErrConflict) {
		// The stored copy is freed by the storage collector.
		out["reason"] = reasonChanged
		return outcome{Port: portSkipped, Output: out}, nil
	}
	if err != nil {
		return outcome{}, err
	}
	out["staged_file_id"] = id
	return outcome{Port: portCandidate, Output: out}, nil
}
