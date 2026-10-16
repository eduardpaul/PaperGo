package workflow

import (
	"encoding/json"
	"errors"
	"time"

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
	register(&Activity{Key: "file.promote", Kind: "action", Description: "Makes a staged file of this run its item's current file, as a new file version. Fails when a file it was made from is no longer current. With recycle_sources, moves the other source items to the recycle bin in the same transaction.",
		InputSchema:  inputs(stagedProp+`, "recycle_sources": {"type": "boolean", "title": "Recycle source items", "description": "Move the other source items to the recycle bin atomically with promotion.", "default": false}`, "staged_file_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"item_id": {"type": "string"}, "blob_id": {"type": "string"}, "file_version": {"type": "integer"}}}`), run: runFilePromote})
	register(&Activity{Key: "file.discard", Kind: "action", Description: "Discards a staged file of this run; the item keeps its current file.",
		InputSchema:  inputs(stagedProp, "staged_file_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"discarded": {"type": "boolean"}}}`), run: runFileDiscard})
	register(&Activity{Key: "file.reclaim", Kind: "action", Description: "Asks to reclaim a file version's bytes after the grace period. The version keeps its id, number, filename, media type, size and checksum. Current files, published files, staged files and open waits that still name the version defer reclamation. A retry does not move the deadline.",
		InputSchema: inputs(itemIDProp+`, "blob_id": {"type": "string", "title": "File version", "description": "The file version to reclaim, such as {trigger:blob_id}."}`, "blob_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"blob_id": {"type": "string"}, "availability": {"type": "string"}, "deferred": {"type": "boolean"},
			"reason": {"type": "string"}, "reclaim_after": {"type": "string"}}}`),
		run: runFileReclaim})
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
	recycle := false
	if v, ok := in["recycle_sources"]; ok {
		v, err := e.scope.resolve(v)
		if err != nil {
			return outcome{}, err
		}
		var valid bool
		if recycle, valid = v.(bool); !valid {
			return outcome{}, errors.New("recycle_sources must be true or false")
		}
	}
	b, err := e.t.PromoteStagedFile(e.ctx, e.author, id, e.runID, recycle)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"item_id": b.ItemID, "blob_id": b.ID, "file_version": b.Version}}, nil
}

func runFileReclaim(e *env, in map[string]any) (outcome, error) {
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	blobID, err := e.scope.text(in["blob_id"])
	if err != nil {
		return outcome{}, err
	}
	if blobID == "" {
		return outcome{}, errors.New("blob_id is empty")
	}
	out, err := e.t.ReclaimBlob(e.ctx, e.author, itemID, blobID)
	if err != nil {
		return outcome{}, err
	}
	result := map[string]any{"blob_id": out.BlobID, "availability": out.Availability, "deferred": out.Deferred}
	if out.Reason != "" {
		result["reason"] = out.Reason
	}
	if out.ReclaimAfter != nil {
		result["reclaim_after"] = out.ReclaimAfter.UTC().Format(time.RFC3339)
	}
	return outcome{Output: result}, nil
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
