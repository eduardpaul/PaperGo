package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"papergo/internal/ai"
	"papergo/internal/forms"
	"papergo/internal/search"
)

// AI steps ask the configured chat model (AI_CHAT_*): ai.complete for
// summaries, extraction and cleanup, search.enrich for context on search
// chunks. Answers use the generic result cache, partitioned by activity and
// keyed by the model endpoint and request.

// ErrNoChat fails AI steps in a deployment without a chat model.
var ErrNoChat = errors.New("AI steps need a chat model; configure AI_CHAT_PROVIDER")

const aiCacheScope = "installation"

// AI step bounds.
const (
	maxPromptChars       = 8000
	defaultInputChars    = 20_000
	maxInputChars        = 200_000
	maxEnrichmentChars   = 1000
	defaultContextChars  = 6000
	maxContextChars      = 50_000
	enrichDocumentPrompt = "\n\nThe passage is part of the document given first. Answer about the passage, in the document's language."
)

func init() {
	register(&Activity{Key: "ai.complete", Kind: "action",
		Description: "Asks the configured chat model to do something with a text: summarize it, extract values, clean it up. The input is text given directly or the text_id of document.read_text. With an output_schema the answer is a JSON object checked against it (json), else text. Answers are cached: the same prompt, schema and input reuse the answer (cached).",
		InputSchema: inputs(itemIDProp+`,
			"prompt": {"type": "string", "title": "Prompt", "description": "What to do with the input."},
			"input": {"type": "string", "title": "Input", "description": "The text to work on."},
			"text_id": {"type": "string", "title": "Text", "description": "Instead of input: the text_id of document.read_text for the item's file."},
			"blob_id": {"type": "string", "title": "File version", "description": "With text_id: the file version it was read from; default the current one."},
			"max_chars": {"type": "integer", "title": "Input size", "minimum": 100, "maximum": 200000, "default": 20000, "description": "Characters of the input sent to the model."},
			"output_schema": {"type": "object", "title": "Output schema", "description": "A form schema of an object: the answer is JSON matching it."}`, "prompt"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"text": {"type": "string"}, "json": {"type": "object"}, "cached": {"type": "boolean"}, "model": {"type": "string"}}}`),
		run: runAIComplete, work: workAIComplete, commit: commitAIComplete})
	register(&Activity{Key: "search.enrich", Kind: "action",
		Description: "Asks the configured chat model to write context for each chunk of a generation, such as which document and clause it is from. The context is searched with the chunk and embedded with it. scope document shows the model the beginning of the document too. Answers are cached per chunk text and instructions.",
		InputSchema: inputs(`
			"generation": {"type": "string", "title": "Generation", "description": "The generation of search.chunk."},
			"instructions": {"type": "string", "title": "Instructions", "description": "What to write for each chunk, such as: one sentence saying which contract and clause this is."},
			"scope": {"type": "string", "title": "Scope", "enum": ["chunk", "document"], "default": "chunk", "description": "chunk: the model sees the chunk; document: also the beginning of the document."},
			"context_chars": {"type": "integer", "title": "Document context", "minimum": 500, "maximum": 50000, "default": 6000, "description": "With scope document: characters of the document shown."}`, "generation", "instructions"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"generation": {"type": "string"}, "enriched": {"type": "integer"}, "cached": {"type": "integer"}, "model": {"type": "string"}}}`),
		run: runSearchEnrich, work: workSearchEnrich, commit: commitSearchEnrich})
}

// cachedAnswer is how answers are kept in the cache.
type cachedAnswer struct {
	Text string         `json:"text,omitempty"`
	JSON map[string]any `json:"json,omitempty"`
}

func (a cachedAnswer) output(model string, cached bool) map[string]any {
	out := map[string]any{"cached": cached, "model": model}
	if a.JSON != nil {
		out["json"] = a.JSON
	} else {
		out["text"] = a.Text
	}
	return out
}

func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// completeJob is ai.complete's work.
type completeJob struct {
	Key       string         `json:"key"`
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	Input     string         `json:"input,omitempty"`
	ObjectKey string         `json:"object_key,omitempty"`
	MaxChars  int            `json:"max_chars"`
	Schema    map[string]any `json:"schema,omitempty"`
}

type completeResult struct {
	Answer cachedAnswer `json:"answer"`
	Cached bool         `json:"cached"`
}

func runAIComplete(e *env, in map[string]any) (outcome, error) {
	if e.tools.Chat == nil {
		return outcome{}, ErrNoChat
	}
	job := completeJob{Model: e.tools.Chat.Key(), MaxChars: defaultInputChars}
	var err error
	if job.Prompt, err = e.optionalText(in, "prompt"); err != nil {
		return outcome{}, err
	}
	if job.Prompt == "" || len([]rune(job.Prompt)) > maxPromptChars {
		return outcome{}, fmt.Errorf("prompt needs 1 to %d characters", maxPromptChars)
	}
	if err = e.whole(in, "max_chars", &job.MaxChars, 100, maxInputChars); err != nil {
		return outcome{}, err
	}
	// The schema is taken as written: its texts are not tokens.
	if raw, ok := in["output_schema"]; ok && raw != nil {
		schema, ok := raw.(map[string]any)
		if !ok {
			return outcome{}, errors.New("output_schema must be an object")
		}
		if err = forms.Validate(schema, "output_schema"); err != nil {
			return outcome{}, err
		}
		if schema["type"] != "object" {
			return outcome{}, errors.New("output_schema must describe an object")
		}
		job.Schema = schema
	}
	textID, err := e.optionalText(in, "text_id")
	if err != nil {
		return outcome{}, err
	}
	if raw, ok := in["input"]; ok {
		if job.Input, err = e.scope.text(raw); err != nil {
			return outcome{}, err
		}
	}
	var source string
	switch {
	case textID != "" && job.Input != "":
		return outcome{}, errors.New("give input or text_id, not both")
	case textID != "":
		// The author must read the file version the text was read from.
		itemID, err := e.target(in)
		if err != nil {
			return outcome{}, err
		}
		blobID, err := e.optionalText(in, "blob_id")
		if err != nil {
			return outcome{}, err
		}
		b, err := e.t.GetBlob(e.ctx, e.author, itemID, blobID)
		if err != nil {
			return outcome{}, err
		}
		text, err := e.t.BlobText(e.ctx, textID)
		if err != nil {
			return outcome{}, err
		}
		if text.Sha256 != b.Sha256 {
			return outcome{}, errors.New("text_id is the text of another file")
		}
		job.ObjectKey, source = text.ObjectKey, "text_id:"+textID
	default:
		if strings.TrimSpace(job.Input) == "" {
			return outcome{}, errors.New("give input or text_id")
		}
		if r := []rune(job.Input); len(r) > job.MaxChars {
			job.Input = string(r[:job.MaxChars])
		}
		source = "input:" + hashText(job.Input)
	}
	schema, err := json.Marshal(job.Schema)
	if err != nil {
		return outcome{}, err
	}
	job.Key = ai.CacheKey(job.Model, "ai.complete", job.Prompt, string(schema), source, strconv.Itoa(job.MaxChars))
	raw, err := json.Marshal(job)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw, Output: map[string]any{"model": job.Model}}, nil
}

func workAIComplete(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job completeJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if answer, found, err := w.findAnswer("ai.complete", job.Key); err != nil {
		return nil, err
	} else if found {
		return json.Marshal(completeResult{Answer: answer, Cached: true})
	}
	if w.tools.Chat == nil {
		return nil, ErrNoChat
	}
	if w.tools.Chat.Key() != job.Model {
		return nil, errors.New("the chat model changed before the request ran")
	}
	input := job.Input
	if job.ObjectKey != "" {
		data, err := w.read(job.ObjectKey)
		if err != nil {
			return nil, err
		}
		var stored textObject
		if err = json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("the stored text is unreadable: %w", err)
		}
		texts := make([]string, len(stored.Pages))
		for i, p := range stored.Pages {
			texts[i] = p.Text
		}
		if r := []rune(strings.Join(texts, "\n\n")); len(r) > job.MaxChars {
			input = string(r[:job.MaxChars])
		} else {
			input = string(r)
		}
	}
	answer, err := w.tools.Chat.Complete(w.ctx, ai.Completion{Instructions: job.Prompt, Input: input, Schema: job.Schema})
	if err != nil {
		return nil, err
	}
	out := cachedAnswer{Text: answer.Text}
	if job.Schema != nil {
		// The answer must be a value of the schema; a model that answers
		// otherwise fails the step, which its retries can repeat.
		values, err := forms.Values(job.Schema, answer.JSON, "answer")
		if err != nil {
			return nil, fmt.Errorf("the model's answer does not match output_schema: %w", err)
		}
		out = cachedAnswer{JSON: values}
	}
	// Check the final step output before persisting a result that a run
	// cannot consume. This includes model identity and output metadata.
	output, err := json.Marshal(outcome{Output: out.output(job.Model, false)})
	if err != nil {
		return nil, err
	}
	if len(output) > MaxOutputBytes {
		return nil, errors.New("the AI answer exceeds the workflow output limit")
	}
	out, err = w.saveAnswer("ai.complete", job.Key, out)
	if err != nil {
		return nil, err
	}
	return json.Marshal(completeResult{Answer: out})
}

func commitAIComplete(_ *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job completeJob
	var result completeResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return outcome{}, err
	}
	return outcome{Output: result.Answer.output(job.Model, result.Cached)}, nil
}

// enrichJob is search.enrich's work. Cached answers are read in the work
// phase, including after a retry of a partially completed generation.
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

// enrichKey is the cache key of one chunk's enrichment: the chunk's kind
// and text (not its enrichment), the instructions and the document context.
func enrichKey(job enrichJob, context string, c search.StagedChunk) string {
	return ai.CacheKey(job.Model, "search.enrich", job.Scope, job.Instructions, hashText(context), hashText(c.Kind+"\n"+c.Text))
}

// documentContext is the beginning of a generation's text, for scope
// document.
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

func runSearchEnrich(e *env, in map[string]any) (outcome, error) {
	if e.tools.Search == nil {
		return outcome{}, ErrNoSearch
	}
	if e.tools.Chat == nil {
		return outcome{}, ErrNoChat
	}
	job := enrichJob{Model: e.tools.Chat.Key(), Scope: "chunk", ContextChars: defaultContextChars}
	var err error
	if job.Generation, err = e.optionalText(in, "generation"); err != nil {
		return outcome{}, err
	}
	if !strings.HasPrefix(job.Generation, e.runID+":") {
		return outcome{}, errors.New("generation must be one this run started")
	}
	if job.Instructions, err = e.optionalText(in, "instructions"); err != nil {
		return outcome{}, err
	}
	if job.Instructions == "" || len([]rune(job.Instructions)) > maxPromptChars {
		return outcome{}, fmt.Errorf("instructions need 1 to %d characters", maxPromptChars)
	}
	if err = e.choice(in, "scope", &job.Scope, []string{"chunk", "document"}); err != nil {
		return outcome{}, err
	}
	if err = e.whole(in, "context_chars", &job.ContextChars, 500, maxContextChars); err != nil {
		return outcome{}, err
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw, Output: map[string]any{"generation": job.Generation, "model": job.Model}}, nil
}

// workSearchEnrich asks for each chunk without a cached answer and writes
// every chunk's enrichment to the search store.
func workSearchEnrich(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job enrichJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.Search == nil {
		return nil, ErrNoSearch
	}
	chunks, err := w.tools.Search.GenerationChunks(w.ctx, job.Generation)
	if err != nil {
		return nil, err
	}
	context := documentContext(chunks, job.Scope, job.ContextChars)
	instructions := job.Instructions
	if job.Scope == "document" {
		instructions += enrichDocumentPrompt
	}
	res := enrichResult{}
	var enrichments []search.Enrichment
	for _, c := range chunks {
		key := enrichKey(job, context, c)
		cached, found, err := w.findAnswer("search.enrich", key)
		if err != nil {
			return nil, err
		}
		if found {
			res.Cached++
		} else {
			if w.tools.Chat == nil {
				return nil, ErrNoChat
			}
			if w.tools.Chat.Key() != job.Model {
				return nil, errors.New("the chat model changed before the request ran")
			}
			input := c.Text
			if job.Scope == "document" {
				input = "<document>\n" + context + "\n</document>\n\n<passage>\n" + c.Text + "\n</passage>"
			}
			answer, err := w.tools.Chat.Complete(w.ctx, ai.Completion{Instructions: instructions, Input: input})
			if err != nil {
				return nil, err
			}
			text := answer.Text
			if r := []rune(text); len(r) > maxEnrichmentChars {
				text = string(r[:maxEnrichmentChars])
			}
			cached, err = w.saveAnswer("search.enrich", key, cachedAnswer{Text: text})
			if err != nil {
				return nil, err
			}
		}
		enrichments = append(enrichments, search.Enrichment{Part: c.Part, Ordinal: c.Ordinal, Text: cached.Text})
	}
	if err = w.tools.Search.SetEnrichments(w.ctx, job.Generation, enrichments); err != nil {
		return nil, err
	}
	res.Enriched = len(enrichments)
	return json.Marshal(res)
}

func commitSearchEnrich(_ *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job enrichJob
	var res enrichResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"generation": job.Generation, "enriched": res.Enriched, "cached": res.Cached, "model": job.Model}}, nil
}

// The AI activities own their cache payloads. The store has no knowledge
// of models, prompts, answer schemas or chunks.
func (w *workEnv) findAnswer(namespace, key string) (cachedAnswer, bool, error) {
	var answer cachedAnswer
	if w.tools.Results == nil || w.tools.ResultTTL <= 0 {
		return answer, false, errors.New("AI steps need a result cache with a positive TTL")
	}
	raw, err := w.tools.Results.Get(w.ctx, namespace, aiCacheScope, key)
	if err != nil || raw == nil {
		return answer, false, err
	}
	err = json.Unmarshal(raw, &answer)
	return answer, true, err
}

func (w *workEnv) saveAnswer(namespace, key string, answer cachedAnswer) (cachedAnswer, error) {
	raw, err := json.Marshal(answer)
	if err != nil {
		return cachedAnswer{}, err
	}
	raw, err = w.tools.Results.PutIfAbsent(w.ctx, namespace, aiCacheScope, key, raw, w.tools.ResultTTL)
	if err != nil {
		return cachedAnswer{}, err
	}
	// Concurrent computations use the same winner, even if the model gave
	// different answers to the equal requests.
	answer = cachedAnswer{}
	err = json.Unmarshal(raw, &answer)
	return answer, err
}
