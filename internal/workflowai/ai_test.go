package workflowai

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"papergo/internal/ai"
	"papergo/internal/forms"
	"papergo/internal/resultcache"
	"papergo/internal/search"
	"papergo/internal/search/sqlitestore"
	"papergo/internal/testutil"
	"papergo/internal/workflow"
)

type resultTestChat struct {
	complete func(ai.Completion) (ai.Answer, error)
}

func (resultTestChat) Key() string { return "test-model" }
func (c resultTestChat) Complete(_ context.Context, in ai.Completion) (ai.Answer, error) {
	return c.complete(in)
}

func TestEnrichmentRetryReusesCompletedChunks(t *testing.T) {
	ctx := context.Background()
	cache := resultcache.New(testutil.Database(t).SQL)
	index, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"), sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	if err = index.StageContent(ctx, "item", "blob", "run:chunk", []search.Chunk{
		{Part: "body", Ordinal: 0, Kind: "text", Text: "first"},
		{Part: "body", Ordinal: 1, Kind: "text", Text: "second"},
		{Part: "body", Ordinal: 2, Kind: "text", Text: "first"},
	}); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	fail := true
	chat := resultTestChat{complete: func(in ai.Completion) (ai.Answer, error) {
		calls[in.Input]++
		if in.Input == "second" && fail {
			return ai.Answer{}, errors.New("provider failed on second chunk")
		}
		return ai.Answer{Text: "context for " + in.Input}, nil
	}}
	tools := workflow.Tools{Search: index, Chat: chat, Results: cache, ResultTTL: time.Hour}
	job := enrichJob{Generation: "run:chunk", Model: chat.Key(), Instructions: "describe", Scope: "chunk"}
	raw, _ := json.Marshal(job)
	if _, err = workSearchEnrich(ctx, tools, raw); err == nil {
		t.Fatal("expected the second chunk to fail")
	}
	// A work failure retains completed answers without publishing half the
	// generation's enrichment. Retrying the exact job finds them in the cache.
	key := enrichKey(job, "", search.StagedChunk{Kind: "text", Text: "first"})
	if cached, err := cache.Get(ctx, "search.enrich", answerScope, key); err != nil || cached == nil {
		t.Fatalf("lost completed answer: %s %v", cached, err)
	}
	chunks, err := index.GenerationChunks(ctx, job.Generation)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if chunk.Enrichment != "" {
			t.Fatal("partially applied enrichment")
		}
	}
	fail = false
	out, err := workSearchEnrich(ctx, tools, raw)
	if err != nil {
		t.Fatal(err)
	}
	var result enrichResult
	if err = json.Unmarshal(out, &result); err != nil || result.Enriched != 3 || result.Cached != 2 {
		t.Fatalf("retry result: %s %v", out, err)
	}
	if calls["first"] != 1 || calls["second"] != 2 {
		t.Fatalf("repeated completed work: %v", calls)
	}
	chunks, err = index.GenerationChunks(ctx, job.Generation)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if chunk.Enrichment != "context for "+chunk.Text {
			t.Fatalf("missing enrichment: %+v", chunk)
		}
	}
}

func TestCompleteCachesOnlyValidatedAnswers(t *testing.T) {
	ctx := context.Background()
	cache := resultcache.New(testutil.Database(t).SQL)
	calls := 0
	chat := resultTestChat{complete: func(ai.Completion) (ai.Answer, error) {
		calls++
		if calls == 1 {
			return ai.Answer{JSON: map[string]any{"total": "invalid"}}, nil
		}
		return ai.Answer{JSON: map[string]any{"total": 12.5}}, nil
	}}
	tools := workflow.Tools{Chat: chat, Results: cache, ResultTTL: time.Hour}
	job := completeJob{Key: "request", Model: chat.Key(), Input: "receipt", Prompt: "extract", Schema: map[string]any{
		"type": "object", "properties": map[string]any{"total": map[string]any{"type": "number"}},
	}}
	raw, _ := json.Marshal(job)
	if _, err := workAIComplete(ctx, tools, raw); err == nil {
		t.Fatal("accepted invalid structured answer")
	}
	if got, err := cache.Get(ctx, "ai.complete", answerScope, job.Key); err != nil || got != nil {
		t.Fatalf("cached invalid answer: %s %v", got, err)
	}
	for _, cached := range []bool{false, true} {
		out, err := workAIComplete(ctx, tools, raw)
		if err != nil {
			t.Fatal(err)
		}
		var result completeResult
		if err = json.Unmarshal(out, &result); err != nil || result.Cached != cached || result.Answer.JSON["total"] != 12.5 {
			t.Fatalf("result: %s %v", out, err)
		}
	}
	if calls != 2 {
		t.Fatalf("asked again after successful work: %d calls", calls)
	}
}

func TestAIActivitySchemas(t *testing.T) {
	want := map[string]bool{"ai.complete": false, "search.enrich": false, "ai.batch.coordinate": false}
	for _, activity := range workflow.Activities() {
		if _, ok := want[activity.Key]; !ok {
			continue
		}
		want[activity.Key] = true
		if err := forms.Validate(activity.InputSchema, activity.Key); err != nil {
			t.Fatalf("%s input: %v", activity.Key, err)
		}
		if err := forms.Validate(activity.OutputSchema, activity.Key+" output"); err != nil {
			t.Fatalf("%s output: %v", activity.Key, err)
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("missing activity %s", key)
		}
	}
}
