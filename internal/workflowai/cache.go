// Package workflowai is the compiled AI workflow module. It registers
// ai.complete and search.enrich, and the installation batch coordinator.
// Receipt behavior is not part of this package.
package workflowai

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/resultcache"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

const (
	answerScope     = "installation"
	requestCache    = "ai.request"
	kindRequest     = "ai.batch.request"
	kindJoin        = "ai.batch.join"
	kindState       = "ai.batch.state"
	kindFlight      = "ai.batch.flight"
	defaultQueue    = "shared"
	maxAttempts     = 3
	defaultDeadline = 72 * time.Hour
	pollEvery       = 5 * time.Minute
	builtinKey      = "ai.batch"

	maxPromptChars       = 8000
	defaultInputChars    = 20_000
	maxInputChars        = 200_000
	maxEnrichmentChars   = 1000
	defaultContextChars  = 6000
	maxContextChars      = 50_000
	enrichDocumentPrompt = "\n\nThe passage is part of the document given first. Answer about the passage, in the document's language."
)

// ErrNoChat fails an immediate AI step when no chat model is configured.
var ErrNoChat = errors.New("AI steps need a chat model; configure AI_CHAT_PROVIDER")

// ErrNoBatch fails a batch step when no batch provider is configured.
var ErrNoBatch = errors.New("batch execution needs a batch provider; configure AI_BATCH_PROVIDER")

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
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

type imageRef struct {
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
}

type blobPin struct {
	ItemID string `json:"item_id"`
	BlobID string `json:"blob_id"`
}

// requestBody is canonical provider input in durable activity storage.
// Consumer identity and routing are kept separately from the shared payload.
type requestBody struct {
	Activity   string         `json:"activity"`
	ModelKey   string         `json:"model_key"`
	Model      string         `json:"model"`
	Connection string         `json:"connection"`
	Prompt     string         `json:"prompt"`
	Input      string         `json:"input,omitempty"`
	ObjectKey  string         `json:"object_key,omitempty"`
	MaxChars   int            `json:"max_chars,omitempty"`
	Schema     map[string]any `json:"schema,omitempty"`
	Images     []imageRef     `json:"images,omitempty"`
}

type requestData struct {
	Hash       string `json:"hash"`
	Queue      string `json:"queue"`
	Activity   string `json:"activity"`
	Connection string `json:"connection"`
	Model      string `json:"model"`
	Attempts   int    `json:"attempts,omitempty"`
}

func cacheReady(tools workflow.Tools) error {
	if tools.Results == nil || tools.ResultTTL <= 0 {
		return errors.New("AI steps need a result cache with a positive TTL")
	}
	return nil
}

func findAnswer(ctx context.Context, tools workflow.Tools, namespace, key string) (cachedAnswer, bool, error) {
	var answer cachedAnswer
	if err := cacheReady(tools); err != nil {
		return answer, false, err
	}
	raw, err := tools.Results.Get(ctx, namespace, answerScope, key)
	if err != nil || raw == nil {
		return answer, false, err
	}
	err = json.Unmarshal(raw, &answer)
	return answer, true, err
}

// findAnswerStep reads a cached answer through the step's transaction.
// The result cache's own connection would wait on the step's write lock.
func findAnswerStep(ctx context.Context, tx *sql.Tx, tools workflow.Tools, namespace, key string) (cachedAnswer, bool, error) {
	var answer cachedAnswer
	if err := cacheReady(tools); err != nil {
		return answer, false, err
	}
	raw, err := resultcache.NewTx(tx).Get(ctx, namespace, answerScope, key)
	if err != nil || raw == nil {
		return answer, false, err
	}
	err = json.Unmarshal(raw, &answer)
	return answer, true, err
}

func saveAnswer(ctx context.Context, tools workflow.Tools, namespace, key string, answer cachedAnswer) (cachedAnswer, error) {
	raw, err := json.Marshal(answer)
	if err != nil {
		return cachedAnswer{}, err
	}
	raw, err = tools.Results.PutIfAbsent(ctx, namespace, answerScope, key, raw, tools.ResultTTL)
	if err != nil {
		return cachedAnswer{}, err
	}
	answer = cachedAnswer{}
	err = json.Unmarshal(raw, &answer)
	return answer, err
}

func requestTTL(tools workflow.Tools) time.Duration {
	d := runtime(tools).BatchDeadline
	if d <= 0 {
		d = defaultDeadline
	}
	return d
}

func putRequest(ctx context.Context, tx *sql.Tx, tools workflow.Tools, hash string, body requestBody) error {
	if tools.Records == nil {
		return errors.New("durable activity storage is not configured")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	until := time.Now().UTC().Add(requestTTL(tools))
	return tools.Records.WithTx(tx).PutImmutable(ctx, activitystore.Record{Namespace: requestCache, Scope: answerScope, Key: hash, Payload: raw, ExpiresAt: &until})
}
func putManifest(step workflow.Step, key string, payload json.RawMessage) error {
	until := time.Now().UTC().Add(requestTTL(step.Tools()))
	store := step.Tools().Records.WithTx(step.DMS().SQLTx())
	if err := store.PutImmutable(step.Context(), activitystore.Record{Namespace: "ai.enrich.manifest", Scope: answerScope, Key: key, Payload: payload, ExpiresAt: &until}); err != nil {
		return err
	}
	return store.Reference(step.Context(), activitystore.RunOwner(step.RunID()), activitystore.ID("ai.enrich.manifest", answerScope, key))
}
func loadManifest(step workflow.Step, key string) (json.RawMessage, error) {
	record, err := step.Tools().Records.WithTx(step.DMS().SQLTx()).Get(step.Context(), "ai.enrich.manifest", answerScope, key)
	if err != nil || record == nil {
		return nil, err
	}
	return record.Payload, nil
}
func loadRequest(ctx context.Context, tools workflow.Tools, hash string) (requestBody, error) {
	var body requestBody
	if tools.Records == nil {
		return body, errors.New("durable activity storage is not configured")
	}
	record, err := tools.Records.Get(ctx, requestCache, answerScope, hash)
	if err != nil {
		return body, err
	}
	if record == nil {
		return body, errors.New("the durable batch request is unavailable")
	}
	return body, json.Unmarshal(record.Payload, &body)
}

func readObject(ctx context.Context, tools workflow.Tools, key string) ([]byte, error) {
	if tools.Storage == nil {
		return nil, errors.New("file storage is not available to workflows")
	}
	f, err := tools.Storage.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 20 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > limit {
		return nil, errors.New("the LLM source exceeds the 20 MiB input limit")
	}
	return data, err
}

func executionMode(step workflow.Step, in map[string]any) (string, error) {
	mode := runtime(step.Tools()).DefaultExecution
	if mode != "batch" {
		mode = "immediate"
	}
	if err := step.Choice(in, "execution", &mode, []string{"immediate", "batch"}); err != nil {
		return "", err
	}
	return mode, nil
}

func queueName(step workflow.Step, in map[string]any) (string, error) {
	queue := defaultQueue
	text, err := step.OptionalText(in, "queue")
	if err != nil {
		return "", err
	}
	if text != "" {
		queue = text
	}
	if queue == "" || len(queue) > 64 {
		return "", errors.New("queue needs 1 to 64 characters")
	}
	for _, r := range queue {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "", errors.New("queue uses letters, digits, hyphens and underscores")
		}
	}
	return queue, nil
}

func deadlineAt(tools workflow.Tools, now time.Time) time.Time {
	d := runtime(tools).BatchDeadline
	if d <= 0 {
		d = defaultDeadline
	}
	return now.Add(d)
}

func inputSchema(properties string, required ...string) map[string]any {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return forms.MustParse(`{"type": "object", "properties": {` + properties + `}, "required": ` + string(req) + `}`)
}

func chatConnection(tools workflow.Tools) string {
	if runtime(tools).Chat == nil {
		return ""
	}
	if runtime(tools).Batch != nil && runtime(tools).Batch.Key() == runtime(tools).Chat.Key() {
		return runtime(tools).Batch.Connection()
	}
	return "chat:" + runtime(tools).Chat.Key()
}

func contentHash(model, activity, connection, queue, prompt, source string, schema []byte, maxChars int, images []imageRef) string {
	parts := []string{activity, connection, prompt, source, string(schema), strconv.Itoa(maxChars)}
	for _, image := range images {
		parts = append(parts, image.ObjectKey, image.ContentType)
	}
	return ai.CacheKey(model, parts...)
}

func shortText(text string, limit int) string {
	if r := []rune(text); len(r) > limit {
		return string(r[:limit])
	}
	return text
}

func notFound(err error) bool { return errors.Is(err, dms.ErrNotFound) }

func saveResponse(ctx context.Context, tools workflow.Tools, key, responseKey string, answer cachedAnswer) error {
	raw, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	until := time.Now().UTC().Add(requestTTL(tools))
	return tools.Records.Write(ctx, func(store *activitystore.Store) error {
		if err := store.PutImmutable(ctx, activitystore.Record{Namespace: "ai.response", Scope: answerScope, Key: responseKey, Payload: raw, ExpiresAt: &until}); err != nil {
			return err
		}
		request, err := store.Get(ctx, requestCache, answerScope, key)
		if err != nil {
			return err
		}
		// Late provider results must not create a permanent reference from
		// a request already expired and collected after cancellation.
		if request == nil {
			return nil
		}
		return store.Reference(ctx, activitystore.RecordOwner(request.ID), activitystore.ID("ai.response", answerScope, responseKey))
	})
}
func responseStep(step workflow.Step, key string) (cachedAnswer, error) {
	var answer cachedAnswer
	r, err := step.Tools().Records.WithTx(step.DMS().SQLTx()).Get(step.Context(), "ai.response", answerScope, key)
	if err != nil {
		return answer, err
	}
	if r == nil {
		return answer, errors.New("durable provider response is unavailable")
	}
	return answer, json.Unmarshal(r.Payload, &answer)
}
