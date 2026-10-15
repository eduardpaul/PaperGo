package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"papergo/internal/forms"
	"papergo/internal/search"
)

// Embedding: search.embed gives the chunks of a generation their vectors
// for semantic search, reusing vectors of equal chunk inputs; the sweep
// (SweepEmbeddings) embeds what search.embed deferred and what an embedding
// model change left without vectors.

const portDeferred = "deferred"

// Sweep bounds: chunk inputs per embedding call and per sweep, and how long
// a vector no chunk uses is kept.
const (
	sweepBatch     = 64
	sweepLimit     = 2000
	vectorKeepTime = 7 * 24 * time.Hour
)

func init() {
	register(&Activity{Key: "search.embed", Kind: "action", Ports: []string{"done", portDeferred},
		Description: "Gives the chunks of a generation their embedding vectors, so semantic search finds them by meaning. Chunks with the same text reuse vectors. mode later leaves the work to the embedding sweep (every 15 minutes); a provider failure does too, following deferred instead of failing the run. Without an embedding model configured it does nothing.",
		InputSchema: inputs(`
			"generation": {"type": "string", "title": "Generation", "description": "The generation of search.chunk."},
			"mode": {"type": "string", "title": "Mode", "enum": ["immediate", "later"], "default": "immediate", "description": "immediate embeds now; later leaves it to the sweep."}`, "generation"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"generation": {"type": "string"}, "embedded": {"type": "integer"}, "reused": {"type": "integer"},
			"model": {"type": "string"}, "reason": {"type": "string"}}}`),
		run: runSearchEmbed, work: workSearchEmbed, commit: commitSearchEmbed})
}

// embedJob is search.embed's work.
type embedJob struct {
	Generation string `json:"generation"`
}

type embedResult struct {
	Model    string `json:"model,omitempty"`
	Embedded int    `json:"embedded"`
	Reused   int    `json:"reused"`
	// Deferred is why the vectors are left to the sweep, empty when done.
	Deferred string `json:"deferred,omitempty"`
}

func runSearchEmbed(e *env, in map[string]any) (outcome, error) {
	if e.tools.Search == nil {
		return outcome{}, ErrNoSearch
	}
	generation, err := e.optionalText(in, "generation")
	if err != nil {
		return outcome{}, err
	}
	if generation == "" {
		return outcome{}, errors.New("generation is required")
	}
	if !strings.HasPrefix(generation, e.runID+":") {
		return outcome{}, errors.New("generation must be one this run started")
	}
	mode := "immediate"
	if err = e.choice(in, "mode", &mode, []string{"immediate", "later"}); err != nil {
		return outcome{}, err
	}
	out := map[string]any{"generation": generation, "embedded": 0, "reused": 0}
	if e.tools.Embeddings == nil {
		out["reason"] = "No embedding model is configured."
		return outcome{Output: out}, nil
	}
	if mode == "later" {
		out["reason"] = "Left to the embedding sweep."
		return outcome{Port: portDeferred, Output: out}, nil
	}
	raw, err := json.Marshal(embedJob{Generation: generation})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw, Output: out}, nil
}

// workSearchEmbed embeds the generation's inputs that have no vector of the
// model. A provider failure is a result, not an error: the sweep embeds the
// chunks once they are published.
func workSearchEmbed(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job embedJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.Search == nil {
		return nil, ErrNoSearch
	}
	res := embedResult{}
	if w.tools.Embeddings == nil {
		return json.Marshal(res)
	}
	model, err := w.tools.Embeddings.Key(w.ctx)
	if err != nil {
		if w.ctx.Err() != nil {
			return nil, w.ctx.Err()
		}
		res.Deferred = "The embedding model is unavailable: " + err.Error()
		return json.Marshal(res)
	}
	res.Model = model
	todo, inputs, err := w.tools.Search.GenerationToEmbed(w.ctx, model, job.Generation)
	if err != nil {
		return nil, err
	}
	res.Reused = inputs - len(todo)
	embedded, err := embedInputs(w.ctx, w.tools, model, todo)
	res.Embedded = embedded
	if err != nil {
		if w.ctx.Err() != nil {
			return nil, w.ctx.Err()
		}
		res.Deferred = "The embedding model failed: " + err.Error()
	}
	return json.Marshal(res)
}

func commitSearchEmbed(_ *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job embedJob
	var res embedResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	out := map[string]any{"generation": job.Generation, "embedded": res.Embedded, "reused": res.Reused, "model": res.Model}
	if res.Deferred != "" {
		out["reason"] = res.Deferred
		return outcome{Port: portDeferred, Output: out}, nil
	}
	return outcome{Output: out}, nil
}

// embedInputs embeds chunk inputs in batches and stores each batch's
// vectors, so a failure keeps what was done; it reports how many were.
func embedInputs(ctx context.Context, tools Tools, model string, todo []search.ChunkToEmbed) (int, error) {
	done := 0
	for start := 0; start < len(todo); start += sweepBatch {
		batch := todo[start:min(len(todo), start+sweepBatch)]
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Input
		}
		vectors, err := tools.Embeddings.Embed(ctx, texts)
		if err != nil {
			return done, err
		}
		pairs := make([]search.HashVector, len(batch))
		for i, c := range batch {
			pairs[i] = search.HashVector{InputHash: c.InputHash, Vector: vectors[i]}
		}
		if err = tools.Search.SetVectors(ctx, model, pairs); err != nil {
			return done, err
		}
		done += len(batch)
	}
	return done, nil
}

// SweepEmbeddings embeds published chunks without a vector of the current
// model, at most 2,000 per call, then deletes vectors no chunk has used for
// a week. It reports how many inputs it embedded; a provider failure stops
// it until the next sweep.
func (s *Service) SweepEmbeddings(ctx context.Context) (int, error) {
	tools := s.Tools
	if tools.Search == nil || tools.Embeddings == nil {
		return 0, nil
	}
	model, err := tools.Embeddings.Key(ctx)
	if err != nil {
		return 0, err
	}
	embedded := 0
	for embedded < sweepLimit {
		todo, err := tools.Search.ChunksToEmbed(ctx, model, min(sweepBatch, sweepLimit-embedded))
		if err != nil || len(todo) == 0 {
			if err == nil {
				break
			}
			return embedded, err
		}
		n, err := embedInputs(ctx, tools, model, todo)
		embedded += n
		if err != nil {
			return embedded, err
		}
	}
	_, err = tools.Search.CollectVectors(ctx, model, time.Now(), vectorKeepTime)
	return embedded, err
}
