// Package workflowai is the compiled AI workflow module. It registers
// ai.complete and search.enrich, and the installation batch coordinator.
// Receipt behavior is not part of this package.
package workflowai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strconv"
	"time"

	"papergo/ent"
	"papergo/ent/cachedresult"
	"papergo/internal/ai"
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
	batchSize       = 20
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
	ItemID      string `json:"item_id"`
	BlobID      string `json:"blob_id"`
}

type blobPin struct {
	ItemID string `json:"item_id"`
	BlobID string `json:"blob_id"`
}

// requestBody is the provider input, stored in the result cache. Wait rows
// keep only the hash and the file pins.
type requestBody struct {
	Activity   string         `json:"activity"`
	ModelKey   string         `json:"model_key"`
	Model      string         `json:"model"`
	Connection string         `json:"connection"`
	Queue      string         `json:"queue"`
	Prompt     string         `json:"prompt"`
	Input      string         `json:"input,omitempty"`
	ObjectKey  string         `json:"object_key,omitempty"`
	MaxChars   int            `json:"max_chars,omitempty"`
	Schema     map[string]any `json:"schema,omitempty"`
	Images     []imageRef     `json:"images,omitempty"`
}

type requestData struct {
	Hash       string    `json:"hash"`
	Queue      string    `json:"queue"`
	Activity   string    `json:"activity"`
	Connection string    `json:"connection"`
	Model      string    `json:"model"`
	Blobs      []blobPin `json:"blobs,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
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
func findAnswerStep(ctx context.Context, client *ent.Client, tools workflow.Tools, namespace, key string) (cachedAnswer, bool, error) {
	var answer cachedAnswer
	if err := cacheReady(tools); err != nil {
		return answer, false, err
	}
	raw, err := stepCacheGet(ctx, client, namespace, key)
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
	ttl := tools.ResultTTL
	deadline := tools.BatchDeadline
	if deadline <= 0 {
		deadline = defaultDeadline
	}
	if deadline > ttl {
		ttl = deadline
	}
	return ttl
}

func putRequest(ctx context.Context, client *ent.Client, tools workflow.Tools, hash string, body requestBody) error {
	if err := cacheReady(tools); err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	winner, err := stepCachePut(ctx, client, requestCache, hash, raw, requestTTL(tools))
	if err != nil {
		return err
	}
	if string(winner) != string(raw) {
		return errors.New("the stored batch request does not match this one")
	}
	return nil
}

func putCachedStep(ctx context.Context, client *ent.Client, tools workflow.Tools, namespace, key string, payload json.RawMessage, ttl time.Duration) error {
	if err := cacheReady(tools); err != nil {
		return err
	}
	_, err := stepCachePut(ctx, client, namespace, key, payload, ttl)
	return err
}

func stepCacheGet(ctx context.Context, client *ent.Client, namespace, key string) (json.RawMessage, error) {
	now := time.Now().UTC()
	row, err := client.CachedResult.Query().Where(
		cachedresult.NamespaceEQ(namespace), cachedresult.ScopeEQ(answerScope), cachedresult.KeyEQ(key), cachedresult.ExpiresAtGT(now),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ttl := row.ExpiresAt.Sub(row.UsedAt)
	if ttl > 0 && now.Sub(row.UsedAt) >= min(24*time.Hour, ttl/2) {
		if err = client.CachedResult.UpdateOneID(row.ID).SetUsedAt(now).SetExpiresAt(now.Add(ttl)).Exec(ctx); err != nil {
			return nil, err
		}
	}
	if len(row.Payload) == 0 {
		return nil, nil
	}
	return json.RawMessage(row.Payload), nil
}

func stepCachePut(ctx context.Context, client *ent.Client, namespace, key string, payload json.RawMessage, ttl time.Duration) (json.RawMessage, error) {
	if ttl <= 0 || len(payload) > 1<<20 || !json.Valid(payload) {
		return nil, errors.New("result cache requires JSON within 1 MiB and a positive TTL")
	}
	now := time.Now().UTC()
	err := client.CachedResult.Create().
		SetNamespace(namespace).SetScope(answerScope).SetKey(key).
		SetPayload(jsontext.Value(payload)).SetUsedAt(now).SetExpiresAt(now.Add(ttl)).Exec(ctx)
	if err == nil {
		return payload, nil
	}
	if !ent.IsConstraintError(err) {
		return nil, err
	}
	row, err := client.CachedResult.Query().Where(
		cachedresult.NamespaceEQ(namespace), cachedresult.ScopeEQ(answerScope), cachedresult.KeyEQ(key),
	).Only(ctx)
	if err != nil {
		return nil, err
	}
	if row.ExpiresAt.After(now) {
		return json.RawMessage(row.Payload), nil
	}
	if err = client.CachedResult.DeleteOneID(row.ID).Exec(ctx); err != nil {
		return nil, err
	}
	if err = client.CachedResult.Create().
		SetNamespace(namespace).SetScope(answerScope).SetKey(key).
		SetPayload(jsontext.Value(payload)).SetUsedAt(now).SetExpiresAt(now.Add(ttl)).Exec(ctx); err != nil {
		return nil, err
	}
	return payload, nil
}

func loadRequest(ctx context.Context, tools workflow.Tools, hash string) (requestBody, error) {
	var body requestBody
	if tools.Results == nil {
		return body, errors.New("AI steps need a result cache with a positive TTL")
	}
	raw, err := tools.Results.Get(ctx, requestCache, answerScope, hash)
	if err != nil {
		return body, err
	}
	if raw == nil {
		return body, errors.New("the batch request expired before it was sent")
	}
	err = json.Unmarshal(raw, &body)
	return body, err
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
	return io.ReadAll(f)
}

func executionMode(step workflow.Step, in map[string]any) (string, error) {
	mode := step.Tools().DefaultExecution
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
	d := tools.BatchDeadline
	if d <= 0 {
		d = defaultDeadline
	}
	return now.Add(d)
}

func batchOwner(queue string) string { return "ai.batch:" + queue }

func pins(refs ...blobPin) []blobPin {
	var out []blobPin
	seen := map[string]bool{}
	for _, pin := range refs {
		if pin.BlobID == "" || seen[pin.BlobID] {
			continue
		}
		seen[pin.BlobID] = true
		out = append(out, pin)
	}
	return out
}

func inputSchema(properties string, required ...string) map[string]any {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return forms.MustParse(`{"type": "object", "properties": {` + properties + `}, "required": ` + string(req) + `}`)
}

func chatConnection(tools workflow.Tools) string {
	if tools.Chat == nil {
		return ""
	}
	if tools.Batch != nil && tools.Batch.Key() == tools.Chat.Key() {
		return tools.Batch.Connection()
	}
	return "chat:" + tools.Chat.Key()
}

func contentHash(model, activity, connection, queue, prompt, source string, schema []byte, maxChars int, images []imageRef) string {
	parts := []string{activity, connection, queue, prompt, source, string(schema), strconv.Itoa(maxChars)}
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

func waitToken(w workflow.Wait) string {
	if w.ClaimToken == nil {
		return ""
	}
	return *w.ClaimToken
}

func notFound(err error) bool { return errors.Is(err, dms.ErrNotFound) }
