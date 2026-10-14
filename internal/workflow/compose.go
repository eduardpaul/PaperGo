package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"papergo/internal/compose"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/model"
	"papergo/internal/storage"
)

// file.compose_pdf combines the photos of a selection run into one
// searchable PDF and stages it for the run's primary item (see package
// compose). An approval reviews it; file.promote makes it the primary's
// file, and item.delete can recycle the other photos.

func init() {
	register(&Activity{Key: "file.compose_pdf", Kind: "action",
		Description: "Combines the current files of the run's items, JPEG, PNG or WebP photos of one list or library, into one searchable PDF in the run's order, and stages it for the primary item. Each page is turned upright and shrunk so its smallest text keeps target_height; OCR places the recognized text invisibly on it. Fails when a photo changes before the PDF is staged.",
		InputSchema: inputs(`"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text of each page keeps, 8 to 200; pages never grow.", "minimum": 8, "maximum": 200, "default": 32},
			"quality": {"type": "integer", "title": "JPEG quality", "minimum": 1, "maximum": 100, "default": 80}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"staged_file_id": {"type": "string", "description": "The staged PDF."},
			"item_id": {"type": "string", "description": "The primary item the PDF is for."},
			"others": {"type": "array", "items": {"type": "string"}, "description": "The run's other items, in order."},
			"pages": {"type": "integer"}, "bytes": {"type": "integer"},
			"metrics": {"type": "object", "description": "Pages, text lines, sizes, settings and each page's scale."}}}`),
		run: runComposePDF, work: workComposePDF, commit: commitComposePDF})
}

// composeJob is file.compose_pdf's work: the photos, in order.
type composeJob struct {
	ItemID  string          `json:"item_id"`
	Photos  []composePhoto  `json:"photos"`
	Options compose.Options `json:"options"`
}

type composePhoto struct {
	ItemID    string `json:"item_id"`
	BlobID    string `json:"blob_id"`
	ObjectKey string `json:"object_key"`
	Filename  string `json:"filename"`
}

type composeResult struct {
	ObjectKey string          `json:"object_key"`
	Size      int64           `json:"size"`
	SHA256    string          `json:"sha256"`
	Metrics   compose.Metrics `json:"metrics"`
}

// errPhotoChanged fails a conversion whose photos changed: the reviewed PDF
// must show the photos as they are.
var errPhotoChanged = errors.New("a selected photo changed after the conversion started; start a new conversion")

func runComposePDF(e *env, in map[string]any) (outcome, error) {
	if e.itemID == "" {
		return outcome{}, errors.New("file.compose_pdf needs a run with items")
	}
	o := compose.Defaults
	if err := e.number(in, "target_height", &o.TargetHeight); err != nil {
		return outcome{}, err
	}
	q := float64(o.Quality)
	if err := e.number(in, "quality", &q); err != nil {
		return outcome{}, err
	}
	if q != float64(int(q)) {
		return outcome{}, errors.New("quality must be a whole number")
	}
	o.Quality = int(q)
	if err := o.Validate(); err != nil {
		return outcome{}, err
	}
	members := e.items
	if len(members) == 0 {
		members = []string{e.itemID}
	}
	if len(members) > compose.MaxPages {
		return outcome{}, fmt.Errorf("a PDF combines at most %d photos", compose.MaxPages)
	}
	primary, err := e.load(e.itemID)
	if err != nil {
		return outcome{}, err
	}
	job := composeJob{ItemID: e.itemID, Options: o}
	for _, id := range members {
		r, err := e.t.Authorize(e.ctx, e.author, id, "write")
		if err != nil {
			return outcome{}, fmt.Errorf("item %s: %w", id, err)
		}
		if r.Kind != "item" || r.ContainerID == nil || primary.ContainerID == nil || *r.ContainerID != *primary.ContainerID {
			return outcome{}, errors.New("every photo must be an item of the primary item's list or library")
		}
		b, err := e.t.CurrentFile(e.ctx, e.author, id)
		if err != nil {
			return outcome{}, fmt.Errorf("item %s: %w", id, err)
		}
		switch b.ContentType {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return outcome{}, fmt.Errorf("%s is not a JPEG, PNG or WebP photo", b.Filename)
		}
		job.Photos = append(job.Photos, composePhoto{ItemID: id, BlobID: b.ID, ObjectKey: b.ObjectKey, Filename: b.Filename})
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw}, nil
}

func workComposePDF(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job composeJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.OCR == nil {
		return nil, ErrNoOCR
	}
	var res composeResult
	err := w.exclusive(func() error {
		var doc bytes.Buffer
		m, err := compose.PDF(w.ctx, w.tools.OCR, &doc, len(job.Photos), func(i int) ([]byte, error) {
			data, err := w.read(job.Photos[i].ObjectKey)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", job.Photos[i].Filename, err)
			}
			return data, nil
		}, w.tools.maxPixels(), job.Options)
		if err != nil {
			return err
		}
		obj, err := w.store(doc.Bytes())
		if err != nil {
			return err
		}
		res = composeResult{ObjectKey: obj.Key, Size: obj.Size, SHA256: obj.SHA256, Metrics: m}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("compose PDF: %w", err)
	}
	return json.Marshal(res)
}

func commitComposePDF(e *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job composeJob
	var res composeResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	sources := make([]model.StagedSource, len(job.Photos))
	others := []any{}
	name := ""
	for i, p := range job.Photos {
		sources[i] = model.StagedSource{ItemID: p.ItemID, BlobID: p.BlobID}
		if p.ItemID == job.ItemID {
			name = strings.TrimSuffix(p.Filename, path.Ext(p.Filename)) + ".pdf"
		} else {
			others = append(others, p.ItemID)
		}
	}
	id, err := e.stage(job.ItemID, sources, storage.Object{Key: res.ObjectKey, Size: res.Size, SHA256: res.SHA256}, name, "application/pdf", res.Metrics)
	if errors.Is(err, dms.ErrConflict) {
		return outcome{}, errPhotoChanged
	}
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"staged_file_id": id, "item_id": job.ItemID, "others": others, "pages": res.Metrics.Pages, "bytes": res.Size, "metrics": res.Metrics}}, nil
}
