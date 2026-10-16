package workflowai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/forms"
	"papergo/internal/search"
	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key:         "search.enrich",
		Kind:        "action",
		Description: "Asks the configured chat model to write context for each chunk of a generation, such as which document and clause it is from. The context is searched with the chunk and embedded with it. scope document shows the model the beginning of the document too. execution immediate calls the model in this step; batch parks each uncached chunk on a named queue and writes the generation when every chunk has an answer. Answers are cached per chunk text and instructions.",
		InputSchema: inputSchema(`
			"generation": {"type": "string", "title": "Generation", "description": "The generation of search.chunk."},
			"instructions": {"type": "string", "title": "Instructions", "description": "What to write for each chunk, such as: one sentence saying which contract and clause this is."},
			"scope": {"type": "string", "title": "Scope", "enum": ["chunk", "document"], "default": "chunk", "description": "chunk: the model sees the chunk; document: also the beginning of the document."},
			"context_chars": {"type": "integer", "title": "Document context", "minimum": 500, "maximum": 50000, "default": 6000, "description": "With scope document: characters of the document shown."},
			"execution": {"type": "string", "title": "Execution", "enum": ["immediate", "batch"], "description": "immediate calls the model now. batch waits for the installation coordinator."},
			"queue": {"type": "string", "title": "Queue", "description": "Named batch queue. Empty uses shared.", "minLength": 1, "maxLength": 64}`, "generation", "instructions"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"generation": {"type": "string"}, "enriched": {"type": "integer"}, "cached": {"type": "integer"}, "model": {"type": "string"}}}`),
		Prepare: prepareEnrich, Work: workSearchEnrich, Commit: commitSearchEnrich,
	})
}

type enrichJob struct {
	Generation   string `json:"generation"`
	Model        string `json:"model"`
	Instructions string `json:"instructions"`
	Scope        string `json:"scope"`
	ContextChars int    `json:"context_chars"`
}

type enrichResult struct {
	Enriched int `json:"enriched"`
	Cached   int `json:"cached"`
}

func enrichKey(job enrichJob, context string, c search.StagedChunk) string {
	return ai.CacheKey(job.Model, "search.enrich", job.Scope, job.Instructions, hashText(context), hashText(c.Kind+"\n"+c.Text))
}

func documentContext(chunks []search.StagedChunk, scope string, limit int) string {
	if scope != "document" {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, c := range chunks {
		r := []rune(c.Text)
		if n+len(r) > limit {
			b.WriteString(string(r[:limit-n]))
			break
		}
		b.WriteString(c.Text)
		b.WriteString("\n")
		n += len(r) + 1
	}
	return strings.TrimSpace(b.String())
}

type manifestChunk struct {
	Part    string `json:"part"`
	Ordinal int    `json:"ordinal"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Hash    string `json:"hash"`
	WaitKey string `json:"wait_key,omitempty"`
	Cached  bool   `json:"cached,omitempty"`
}

type enrichManifest struct {
	Generation   string          `json:"generation"`
	Model        string          `json:"model"`
	Scope        string          `json:"scope"`
	Instructions string          `json:"instructions"`
	Context      string          `json:"context"`
	Chunks       []manifestChunk `json:"chunks"`
}

type joinData struct {
	Manifest string `json:"manifest"`
	Queue    string `json:"queue"`
	Model    string `json:"model"`
}

func prepareEnrich(step workflow.Step, in map[string]any) (workflow.Prepared, error) {
	tools := step.Tools()
	if tools.Search == nil {
		return workflow.Prepared{}, workflow.ErrNoSearch
	}
	mode, err := executionMode(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	queue, err := queueName(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	job := enrichJob{Scope: "chunk", ContextChars: defaultContextChars}
	if job.Generation, err = step.OptionalText(in, "generation"); err != nil {
		return workflow.Prepared{}, err
	}
	if !strings.HasPrefix(job.Generation, step.RunID()+":") {
		return workflow.Prepared{}, errors.New("generation must be one this run started")
	}
	if job.Instructions, err = step.OptionalText(in, "instructions"); err != nil {
		return workflow.Prepared{}, err
	}
	if job.Instructions == "" || len([]rune(job.Instructions)) > maxPromptChars {
		return workflow.Prepared{}, fmt.Errorf("instructions need 1 to %d characters", maxPromptChars)
	}
	if err = step.Choice(in, "scope", &job.Scope, []string{"chunk", "document"}); err != nil {
		return workflow.Prepared{}, err
	}
	if err = step.Whole(in, "context_chars", &job.ContextChars, 500, maxContextChars); err != nil {
		return workflow.Prepared{}, err
	}
	if mode == "batch" {
		if tools.Batch == nil {
			return workflow.Prepared{}, ErrNoBatch
		}
		job.Model = tools.Batch.Key()
		return parkEnrich(step, job, queue)
	}
	if tools.Chat == nil {
		return workflow.Prepared{}, ErrNoChat
	}
	job.Model = tools.Chat.Key()
	raw, err := json.Marshal(job)
	if err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Job: raw, Output: map[string]any{"generation": job.Generation, "model": job.Model}}, nil
}

func parkEnrich(step workflow.Step, job enrichJob, queue string) (workflow.Prepared, error) {
	tools := step.Tools()
	chunks, err := tools.Search.GenerationChunks(step.Context(), job.Generation)
	if err != nil {
		return workflow.Prepared{}, err
	}
	contextText := documentContext(chunks, job.Scope, job.ContextChars)
	instructions := job.Instructions
	if job.Scope == "document" {
		instructions += enrichDocumentPrompt
	}
	manifest := enrichManifest{
		Generation: job.Generation, Model: job.Model, Scope: job.Scope, Instructions: job.Instructions, Context: contextText,
	}
	pending := 0
	for i, chunk := range chunks {
		key := enrichKey(job, contextText, chunk)
		entry := manifestChunk{Part: chunk.Part, Ordinal: chunk.Ordinal, Kind: chunk.Kind, Text: chunk.Text, Hash: key}
		if _, found, err := findAnswerStep(step.Context(), step.DMS().Client, tools, "search.enrich", key); err != nil {
			return workflow.Prepared{}, err
		} else if found {
			entry.Cached = true
			manifest.Chunks = append(manifest.Chunks, entry)
			continue
		}
		input := chunk.Text
		if job.Scope == "document" {
			input = "<document>\n" + contextText + "\n</document>\n\n<passage>\n" + chunk.Text + "\n</passage>"
		}
		body := requestBody{
			Activity: "search.enrich", ModelKey: job.Model, Model: tools.Batch.Model(), Connection: tools.Batch.Connection(),
			Queue: queue, Prompt: instructions, Input: input,
		}
		if err = putRequest(step.Context(), step.DMS().Client, tools, key, body); err != nil {
			return workflow.Prepared{}, err
		}
		entry.WaitKey = fmt.Sprintf("%s:%d", step.ExecutionID(), i)
		data, err := json.Marshal(requestData{Hash: key, Queue: queue, Activity: "search.enrich", Connection: body.Connection, Model: job.Model})
		if err != nil {
			return workflow.Prepared{}, err
		}
		if err = workflow.PutWait(step.Context(), step.DMS(), workflow.Wait{
			Kind: kindRequest, Key: entry.WaitKey, WorkspaceID: step.WorkspaceID(), RunID: step.RunID(),
			Node: step.Node(), ExecutionID: entry.WaitKey, Data: data, Deadline: deadlinePtr(deadlineAt(tools, tools.Clock())),
		}); err != nil {
			return workflow.Prepared{}, err
		}
		manifest.Chunks = append(manifest.Chunks, entry)
		pending++
	}
	if pending == 0 {
		raw, err := json.Marshal(job)
		if err != nil {
			return workflow.Prepared{}, err
		}
		return workflow.Prepared{Job: raw, Output: map[string]any{"generation": job.Generation, "model": job.Model}}, nil
	}
	manifestKey := "manifest:" + step.ExecutionID()
	raw, err := json.Marshal(manifest)
	if err != nil {
		return workflow.Prepared{}, err
	}
	if err = putCachedStep(step.Context(), step.DMS().Client, tools, "search.enrich", manifestKey, raw, requestTTL(tools)); err != nil {
		return workflow.Prepared{}, err
	}
	data, err := json.Marshal(joinData{Manifest: manifestKey, Queue: queue, Model: job.Model})
	if err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Park: &workflow.Park{Kind: kindJoin, Key: step.ExecutionID(), Data: data, Deadline: deadlineAt(tools, tools.Clock())}}, nil
}

func deadlinePtr(at time.Time) *time.Time { return &at }

func workSearchEnrich(ctx context.Context, tools workflow.Tools, raw json.RawMessage) (json.RawMessage, error) {
	var job enrichJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if tools.Search == nil {
		return nil, workflow.ErrNoSearch
	}
	chunks, err := tools.Search.GenerationChunks(ctx, job.Generation)
	if err != nil {
		return nil, err
	}
	contextText := documentContext(chunks, job.Scope, job.ContextChars)
	instructions := job.Instructions
	if job.Scope == "document" {
		instructions += enrichDocumentPrompt
	}
	res := enrichResult{}
	var enrichments []search.Enrichment
	for _, c := range chunks {
		key := enrichKey(job, contextText, c)
		cached, found, err := findAnswer(ctx, tools, "search.enrich", key)
		if err != nil {
			return nil, err
		}
		if found {
			res.Cached++
		} else {
			if tools.Chat == nil {
				return nil, ErrNoChat
			}
			if tools.Chat.Key() != job.Model {
				return nil, errors.New("the chat model changed before the request ran")
			}
			input := c.Text
			if job.Scope == "document" {
				input = "<document>\n" + contextText + "\n</document>\n\n<passage>\n" + c.Text + "\n</passage>"
			}
			answer, err := tools.Chat.Complete(ctx, ai.Completion{Instructions: instructions, Input: input})
			if err != nil {
				return nil, err
			}
			text := shortText(answer.Text, maxEnrichmentChars)
			cached, err = saveAnswer(ctx, tools, "search.enrich", key, cachedAnswer{Text: text})
			if err != nil {
				return nil, err
			}
		}
		enrichments = append(enrichments, search.Enrichment{Part: c.Part, Ordinal: c.Ordinal, Text: cached.Text})
	}
	if err = tools.Search.SetEnrichments(ctx, job.Generation, enrichments); err != nil {
		return nil, err
	}
	res.Enriched = len(enrichments)
	return json.Marshal(res)
}

func commitSearchEnrich(_ workflow.Step, rawJob, rawResult json.RawMessage) (workflow.Prepared, error) {
	var job enrichJob
	var res enrichResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return workflow.Prepared{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Output: map[string]any{"generation": job.Generation, "enriched": res.Enriched, "cached": res.Cached, "model": job.Model}}, nil
}
