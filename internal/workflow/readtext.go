package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/forms"
)

// document.read_text reads the text of an item's current file, page by
// page, and keeps it once per file bytes and settings (dms.BlobText):
// identical files and later runs reuse it.

const (
	portNoText      = "no_text"
	portUnsupported = "unsupported"
)

func init() {
	register(&Activity{Key: "document.read_text", Kind: "action", Ports: []string{"done", portNoText, portUnsupported},
		Description: "Reads the text of the item's current file, page by page: the text layer of PDF pages, and OCR of pages without one and of images when OCR is configured. The text is kept for the file's bytes, so an identical file or a later run reuses it. Follows done with text, no_text when the file holds none, and unsupported when it cannot be read (another format, a protected or broken PDF).",
		InputSchema: inputs(itemIDProp + `,
			"ocr": {"type": "string", "title": "OCR", "enum": ["auto", "always", "never"], "default": "auto", "description": "auto reads text layers and OCRs pages and images without one; always OCRs every page; never reads text layers only."},
			"max_pages": {"type": "integer", "title": "Maximum pages", "minimum": 1, "maximum": 2000, "default": 500, "description": "Later pages are left out (truncated)."},
			"languages": {"type": "string", "title": "Languages", "description": "OCR language codes joined with + (eng+deu); default the file's languages."}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"text_id": {"type": "string", "description": "The kept text, for later steps."},
			"item_id": {"type": "string"}, "blob_id": {"type": "string", "description": "The file version read."},
			"pages": {"type": "integer"}, "chars": {"type": "integer"}, "language": {"type": "string"},
			"ocr_pages": {"type": "integer"}, "skipped_pages": {"type": "integer", "description": "Pages without a text layer that were not OCRed."},
			"truncated": {"type": "boolean"}, "reused": {"type": "boolean", "description": "The text was read before."},
			"reason": {"type": "string", "description": "Why the file cannot be read (unsupported)."}}}`),
		run: runReadText, work: workReadText, commit: commitReadText})
}

// readTextJob is document.read_text's work: the file version to read.
type readTextJob struct {
	ItemID      string          `json:"item_id"`
	BlobID      string          `json:"blob_id"`
	SHA256      string          `json:"sha256"`
	ObjectKey   string          `json:"object_key"`
	ContentType string          `json:"content_type"`
	Settings    string          `json:"settings"`
	Options     doctext.Options `json:"options"`
}

// readTextResult is the work's result: the stored page texts, or why the
// file cannot be read.
type readTextResult struct {
	Reason       string          `json:"reason,omitempty"`
	ObjectKey    string          `json:"object_key,omitempty"`
	ObjectSize   int64           `json:"object_size,omitempty"`
	ObjectSHA256 string          `json:"object_sha256,omitempty"`
	Pages        int             `json:"pages"`
	Chars        int             `json:"chars"`
	OCRPages     int             `json:"ocr_pages"`
	SkippedPages int             `json:"skipped_pages"`
	Truncated    bool            `json:"truncated"`
	Language     string          `json:"language,omitempty"`
	Extractor    string          `json:"extractor"`
	Models       json.RawMessage `json:"models,omitempty"`
}

// textObject is the stored page texts of a file.
type textObject struct {
	Pages []doctext.Page `json:"pages"`
}

func runReadText(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	o := doctext.Options{OCR: doctext.OCRAuto, MaxPages: 500, MaxPixels: e.tools.maxPixels(), Languages: e.tools.Languages}
	if raw, ok := in["ocr"]; ok {
		if o.OCR, err = e.scope.text(raw); err != nil {
			return outcome{}, err
		}
		if o.OCR == "" {
			o.OCR = doctext.OCRAuto
		}
	}
	switch o.OCR {
	case doctext.OCRAuto, doctext.OCRNever:
	case doctext.OCRAlways:
		if e.tools.OCR == nil {
			return outcome{}, ErrNoOCR
		}
	default:
		return outcome{}, errors.New("ocr must be auto, always or never")
	}
	pages := float64(o.MaxPages)
	if err = e.number(in, "max_pages", &pages); err != nil {
		return outcome{}, err
	}
	if pages != float64(int(pages)) || pages < 1 || pages > 2000 {
		return outcome{}, errors.New("max_pages must be a whole number from 1 to 2000")
	}
	o.MaxPages = int(pages)
	b, err := e.t.CurrentFile(e.ctx, e.author, itemID)
	if err != nil {
		return outcome{}, err
	}
	o.Hints = b.Languages
	if raw, ok := in["languages"]; ok {
		hints, err := e.scope.text(raw)
		if err != nil {
			return outcome{}, err
		}
		if hints = strings.TrimSpace(hints); hints != "" {
			o.Hints = hints
		}
	}
	settings := e.tools.textSettings(o)
	if row, err := e.t.FindBlobText(e.ctx, b.Sha256, settings); err != nil || row != nil {
		if err != nil {
			return outcome{}, err
		}
		return textOutcome(itemID, b.ID, row, true), nil
	}
	job, err := json.Marshal(readTextJob{ItemID: itemID, BlobID: b.ID, SHA256: b.Sha256, ObjectKey: b.ObjectKey, ContentType: b.ContentType, Settings: settings, Options: o})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: job, Output: map[string]any{"item_id": itemID, "blob_id": b.ID}}, nil
}

// textSettings identify how text is read, so a result is reused only for
// the same reader, OCR engine and models, mode, page bound and languages.
func (t Tools) textSettings(o doctext.Options) string {
	parts := []string{doctext.Version, "ocr=" + o.OCR, "pages=" + strconv.Itoa(o.MaxPages), "languages=" + o.Hints + "/" + strings.Join(o.Languages, ",")}
	if t.PDF != nil {
		parts = append(parts, "pdf="+t.PDF.Name())
	}
	if t.OCR != nil && o.OCR != doctext.OCRNever {
		info := t.OCR.Info()
		parts = append(parts, "engine="+info.Engine)
		for _, m := range info.Models {
			parts = append(parts, m.Name+"="+m.SHA256)
		}
	} else {
		parts = append(parts, "engine=none")
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:])
}

func textOutcome(itemID, blobID string, row *ent.BlobText, reused bool) outcome {
	out := map[string]any{"text_id": row.ID, "item_id": itemID, "blob_id": blobID, "pages": row.Pages, "chars": row.Chars, "language": row.Language,
		"ocr_pages": row.OcrPages, "skipped_pages": row.SkippedPages, "truncated": row.Truncated, "reused": reused}
	port := "done"
	if row.Chars == 0 {
		port = portNoText
	}
	return outcome{Port: port, Output: out}
}

func workReadText(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job readTextJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	res := readTextResult{Extractor: doctext.Version}
	if w.tools.PDF != nil {
		res.Extractor += "+" + w.tools.PDF.Name()
	}
	if w.tools.OCR != nil && job.Options.OCR != doctext.OCRNever {
		models, err := json.Marshal(w.tools.OCR.Info().Models)
		if err != nil {
			return nil, err
		}
		res.Models = models
	}
	err := w.exclusive(func() error {
		data, err := w.read(job.ObjectKey)
		if err != nil {
			return err
		}
		r, err := doctext.Extract(w.ctx, data, job.ContentType, job.Options, w.tools.PDF, w.tools.OCR)
		if errors.Is(err, doctext.ErrUnsupported) {
			res.Reason = err.Error()
			return nil
		}
		if err != nil {
			return err
		}
		pages, err := json.Marshal(textObject{Pages: r.Pages})
		if err != nil {
			return err
		}
		obj, err := w.store(pages)
		if err != nil {
			return err
		}
		res.ObjectKey, res.ObjectSize, res.ObjectSHA256 = obj.Key, obj.Size, obj.SHA256
		res.Pages, res.Chars, res.OCRPages, res.SkippedPages, res.Truncated, res.Language = len(r.Pages), r.Chars, r.OCRPages, r.Skipped, r.Truncated, r.Language
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the text of %s: %w", job.BlobID, err)
	}
	return json.Marshal(res)
}

func commitReadText(e *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job readTextJob
	var res readTextResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	if res.Reason != "" {
		return outcome{Port: portUnsupported, Output: map[string]any{"item_id": job.ItemID, "blob_id": job.BlobID, "reason": res.Reason}}, nil
	}
	row, err := e.t.SaveBlobText(e.ctx, dmsBlobText(job, res))
	if err != nil {
		return outcome{}, err
	}
	return textOutcome(job.ItemID, job.BlobID, row, false), nil
}

func dmsBlobText(job readTextJob, res readTextResult) dms.BlobTextInput {
	return dms.BlobTextInput{SHA256: job.SHA256, Settings: job.Settings, ObjectKey: res.ObjectKey, ObjectSize: res.ObjectSize, ObjectSHA256: res.ObjectSHA256,
		Pages: res.Pages, Chars: res.Chars, OCRPages: res.OCRPages, SkippedPages: res.SkippedPages, Truncated: res.Truncated, Language: res.Language,
		Extractor: res.Extractor, Models: res.Models}
}
