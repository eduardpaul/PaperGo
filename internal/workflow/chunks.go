package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/forms"
	"papergo/internal/search"
)

// ChunkReference identifies a complete immutable snapshot owned by this run.
type ChunkReference struct {
	Key string `json:"key"`
}

type chunkSnapshot struct {
	ItemID string             `json:"item_id"`
	BlobID string             `json:"blob_id"`
	Chunk  search.StagedChunk `json:"chunk"`
}

func init() {
	register(&Activity{Key: "search.read_chunks", Kind: "action", Description: "Reads a bounded page of the run's chunk snapshots. Each entry has text and a complete chunk reference for ai.complete, independent of search publication.",
		InputSchema:  inputs(`"generation":{"type":"string","title":"Generation"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100,"default":10}`, "generation"),
		OutputSchema: forms.MustParse(`{"type":"object","properties":{"chunks":{"type":"array","items":{"type":"object"}},"next_offset":{"type":"integer"},"more":{"type":"boolean"}}}`), run: runReadChunks})
}

// snapshotChunks runs outside the step writer. Run references keep every
// snapshot through arbitrary waits; terminal cleanup releases them for expiry.
func snapshotChunks(ctx context.Context, store *activitystore.Store, runID, itemID, blobID, generation, part string, chunks []search.Chunk) error {
	if store == nil {
		return errors.New("chunk snapshots need durable activity storage")
	}
	return store.Write(ctx, func(tx *activitystore.Store) error {
		until := time.Now().UTC().Add(24 * time.Hour)
		keys := make([]string, 0, len(chunks))
		for _, c := range chunks {
			value := chunkSnapshot{ItemID: itemID, BlobID: blobID, Chunk: search.StagedChunk{Part: c.Part, Ordinal: c.Ordinal, Page: c.Page, Kind: c.Kind, Text: c.Text, Enrichment: c.Enrichment}}
			payload, err := json.Marshal(value)
			if err != nil {
				return err
			}
			key := hashText(string(payload))
			if err = tx.PutImmutable(ctx, activitystore.Record{Namespace: "workflow.chunk", Scope: runID, Key: key, Payload: payload, ExpiresAt: &until}); err != nil {
				return err
			}
			if err = tx.Reference(ctx, activitystore.RunOwner(runID), activitystore.ID("workflow.chunk", runID, key)); err != nil {
				return err
			}
			keys = append(keys, key)
		}
		manifest, err := json.Marshal(keys)
		if err != nil {
			return err
		}
		if err = tx.Put(ctx, activitystore.Record{Namespace: "workflow.chunk.parts", Scope: generation, Key: part, Payload: manifest, ExpiresAt: &until}); err != nil {
			return err
		}
		return tx.Reference(ctx, activitystore.RunOwner(runID), activitystore.ID("workflow.chunk.parts", generation, part))
	})
}

// ReadChunk resolves a reference without depending on the mutable search index.
// Authorization uses the snapshot's actual item and file, including when a
// workflow chunks another item through an explicit item_id.
func (s Step) ReadChunk(ref ChunkReference) (search.StagedChunk, error) { return s.e.readChunk(ref) }
func (e *env) readChunk(ref ChunkReference) (search.StagedChunk, error) {
	if e.tools.Records == nil {
		return search.StagedChunk{}, errors.New("chunk snapshots need durable activity storage")
	}
	record, err := e.tools.Records.WithTx(e.t.SQLTx()).Get(e.ctx, "workflow.chunk", e.runID, ref.Key)
	if err != nil {
		return search.StagedChunk{}, err
	}
	if record == nil {
		return search.StagedChunk{}, errors.New("the chunk reference does not belong to this run or has expired")
	}
	var value chunkSnapshot
	if err = json.Unmarshal(record.Payload, &value); err != nil {
		return search.StagedChunk{}, err
	}
	if _, err = e.t.GetBlob(e.ctx, e.author, value.ItemID, value.BlobID); err != nil {
		return search.StagedChunk{}, err
	}
	return value.Chunk, nil
}

func runReadChunks(e *env, in map[string]any) (outcome, error) {
	generation, err := e.optionalText(in, "generation")
	if err != nil {
		return outcome{}, err
	}
	if !strings.HasPrefix(generation, e.runID+":") {
		return outcome{}, fmt.Errorf("generation must belong to this run")
	}
	if e.tools.Records == nil {
		return outcome{}, errors.New("chunk snapshots need durable activity storage")
	}
	offset, limit := 0, 10
	if err = e.whole(in, "offset", &offset, 0, 100000); err != nil {
		return outcome{}, err
	}
	if err = e.whole(in, "limit", &limit, 1, 100); err != nil {
		return outcome{}, err
	}
	store := e.tools.Records.WithTx(e.t.SQLTx())
	var keys []string
	after := ""
	for {
		parts, err := store.List(e.ctx, "workflow.chunk.parts", generation, "pending", after, 100)
		if err != nil {
			return outcome{}, err
		}
		if len(parts) == 0 {
			break
		}
		for _, part := range parts {
			var values []string
			if err = json.Unmarshal(part.Payload, &values); err != nil {
				return outcome{}, err
			}
			keys = append(keys, values...)
			after = part.Key
		}
	}
	out := make([]any, 0)
	budget, next := 40000, offset
	for next < len(keys) && len(out) < limit && budget > 0 {
		ref := ChunkReference{Key: keys[next]}
		c, err := e.readChunk(ref)
		if err != nil {
			return outcome{}, err
		}
		text, truncated := c.Text, false
		if len(text) > budget {
			text = string([]rune(text)[:min(len([]rune(text)), budget/4)])
			truncated = true
		}
		budget -= len(text)
		out = append(out, map[string]any{"chunk": ref, "text": text, "kind": c.Kind, "truncated": truncated})
		next++
	}
	return outcome{Output: map[string]any{"chunks": out, "next_offset": next, "more": next < len(keys)}}, nil
}
