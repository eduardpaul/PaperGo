package workflow

import (
	"encoding/json"
	"errors"

	"papergo/internal/dal/blobs"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/preview"
	"papergo/internal/storage"
)

func init() {
	register(&Activity{Key: "file.preview", Kind: "action", Ports: []string{"done", portUnsupported, portStale}, Description: "Creates a first-page WebP rendition under 100 KiB and 800 pixels. Reuses successful results for identical bytes without changing the source or emitting file events.", InputSchema: inputs(itemIDProp), OutputSchema: forms.MustParse(`{"type":"object","properties":{"item_id":{"type":"string"},"blob_id":{"type":"string"},"ready":{"type":"boolean"}}}`), run: runPreview, work: workPreview, commit: commitPreview})
	RegisterRole(&Role{Key: "file.preview", Name: "File previews", Description: "Generates lightweight first-page previews asynchronously."})
	RegisterBuiltIn(&BuiltIn{Key: "documents.preview", Name: "Generate file previews", Role: "file.preview", Default: true, System: true, Description: "Generates bounded WebP previews when files are added or replaced. Also runs on demand.", ParametersSchema: forms.MustParse(`{"type":"object","properties":{}}`), Definition: `{"triggers":[{"type":"file.added","collection_id":"{param:collection}"},{"type":"file.updated","collection_id":"{param:collection}"},{"type":"manual","collection_id":"{param:collection}","selection":"per_item"}],"concurrency":"coalesce","flow":{"start":"preview","nodes":{"preview":{"activity":"file.preview"}}}}`})
}

type previewJob struct{ ItemID, BlobID, SHA256, ObjectKey, Media string }
type previewResult struct {
	Object        storage.Object
	Width, Height int
	Failed        bool
}

func runPreview(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	file, err := e.t.CurrentFile(e.ctx, e.author, id)
	if err != nil {
		return outcome{}, err
	}
	out := map[string]any{"item_id": id, "blob_id": file.ID}
	if trigger := e.scope.trigger; trigger["type"] == dms.EventFileAdded || trigger["type"] == dms.EventFileUpdated {
		data, _ := trigger["data"].(map[string]any)
		if blobID, _ := data["blob_id"].(string); blobID != "" && blobID != file.ID {
			return outcome{Port: portStale, Output: out}, nil
		}
	}
	cached, err := e.t.FindPreview(e.ctx, file.Sha256)
	if err != nil {
		return outcome{}, err
	}
	if cached != nil && cached.Status == blobs.PreviewReady {
		out["ready"] = true
		return outcome{Port: "done", Output: out}, nil
	}
	job, err := json.Marshal(previewJob{id, file.ID, file.Sha256, file.ObjectKey, string(file.ContentType)})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: job, BlobIDs: []string{file.ID}, Output: out}, nil
}
func workPreview(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job previewJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	var result previewResult
	err := func() error {
		data, err := w.read(job.ObjectKey)
		if err != nil {
			return err
		}
		image, err := preview.Generate(w.ctx, data, job.Media, w.tools.PDF, w.tools.maxPixels())
		if err != nil {
			if w.ctx.Err() != nil {
				return w.ctx.Err()
			}
			result.Failed = true
			return nil
		}
		result.Object, err = w.store(image.WebP)
		result.Width, result.Height = image.Width, image.Height
		return err
	}()
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func commitPreview(e *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job previewJob
	var result previewResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return outcome{}, err
	}
	out := map[string]any{"item_id": job.ItemID, "blob_id": job.BlobID, "ready": !result.Failed}
	err := e.t.SavePreview(e.ctx, job.SHA256, result.Object, result.Width, result.Height)
	if errors.Is(err, dms.ErrNotFound) {
		return outcome{Port: portStale, Output: out}, nil
	}
	if err != nil {
		return outcome{}, err
	}
	port := "done"
	if result.Failed {
		port = portUnsupported
	}
	return outcome{Port: port, Output: out}, nil
}
