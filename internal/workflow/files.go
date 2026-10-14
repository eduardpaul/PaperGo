package workflow

import (
	"encoding/json"
	"errors"

	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/model"
	"papergo/internal/storage"
)

// File activities work with staged files: files a run produced for an item
// (see dms.StageFile). Activities with heavy work stage their results;
// file.promote makes one the item's current file and file.discard drops it.

const stagedProp = `"staged_file_id": {"type": "string", "title": "Staged file", "description": "A staged file of this run, such as {step:prepare.staged_file_id}."}`

func init() {
	register(&Activity{Key: "file.promote", Kind: "action", Description: "Makes a staged file of this run its item's current file, as a new file version. Fails when a file it was made from is no longer current.",
		InputSchema:  inputs(stagedProp, "staged_file_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"item_id": {"type": "string"}, "blob_id": {"type": "string"}, "file_version": {"type": "integer"}}}`), run: runFilePromote})
	register(&Activity{Key: "file.discard", Kind: "action", Description: "Discards a staged file of this run; the item keeps its current file.",
		InputSchema:  inputs(stagedProp, "staged_file_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"discarded": {"type": "boolean"}}}`), run: runFileDiscard})
}

func (e *env) stagedID(in map[string]any) (string, error) {
	id, err := e.scope.text(in["staged_file_id"])
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", errors.New("staged_file_id is empty")
	}
	return id, nil
}

func runFilePromote(e *env, in map[string]any) (outcome, error) {
	id, err := e.stagedID(in)
	if err != nil {
		return outcome{}, err
	}
	b, err := e.t.PromoteStagedFile(e.ctx, e.author, id, e.runID)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"item_id": b.ItemID, "blob_id": b.ID, "file_version": b.Version}}, nil
}

func runFileDiscard(e *env, in map[string]any) (outcome, error) {
	id, err := e.stagedID(in)
	if err != nil {
		return outcome{}, err
	}
	if err = e.t.DiscardStagedFile(e.ctx, e.author, id, e.runID); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"discarded": true}}, nil
}

// stage records a file a work step stored, made from sources, as a staged
// file of the run for itemID, and returns its ID.
func (e *env) stage(itemID string, sources []model.StagedSource, object storage.Object, filename, contentType string, metrics any) (string, error) {
	raw, err := json.Marshal(metrics)
	if err != nil {
		return "", err
	}
	f, err := e.t.StageFile(e.ctx, e.author, dms.StageInput{ItemID: itemID, RunID: e.runID, Sources: sources, ObjectKey: object.Key,
		Filename: filename, ContentType: contentType, Size: object.Size, SHA256: object.SHA256, Metrics: raw})
	if err != nil {
		return "", err
	}
	return f.ID, nil
}
