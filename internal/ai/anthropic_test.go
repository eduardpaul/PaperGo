package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/ai"
)

// messageBatches is a Message Batches API whose batches, results and
// create failures a test sets.
type messageBatches struct {
	mu       sync.Mutex
	creates  []map[string]any
	headers  []http.Header
	fail     int // the next create's status, once
	batches  []map[string]any
	results  map[string][]map[string]any
	resultsN int
}

func newMessageBatches(t *testing.T) (*messageBatches, *httptest.Server) {
	m := &messageBatches{results: map[string][]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(server.Close)
	return m, server
}

func (m *messageBatches) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := strings.TrimSuffix(r.URL.Path, "/")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && path == "/v1/messages/batches":
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		m.creates = append(m.creates, body)
		m.headers = append(m.headers, r.Header.Clone())
		if m.fail != 0 {
			status := m.fail
			m.fail = 0
			w.WriteHeader(status)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"no"}}`)
			return
		}
		batch := batchJSON(fmt.Sprintf("msgbatch_%d", len(m.creates)), "in_progress", time.Now())
		m.batches = append([]map[string]any{batch}, m.batches...)
		json.NewEncoder(w).Encode(batch)
	case r.Method == http.MethodGet && path == "/v1/messages/batches":
		json.NewEncoder(w).Encode(map[string]any{"data": m.batches, "has_more": false})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/results"):
		m.resultsN++
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/messages/batches/"), "/results")
		w.Header().Set("Content-Type", "application/x-jsonl")
		for _, line := range m.results[id] {
			raw, _ := json.Marshal(line)
			w.Write(append(raw, '\n'))
		}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/messages/batches/"):
		id := strings.TrimPrefix(path, "/v1/messages/batches/")
		for _, b := range m.batches {
			if b["id"] == id {
				json.NewEncoder(w).Encode(b)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func batchJSON(id, status string, created time.Time) map[string]any {
	return map[string]any{
		"id": id, "type": "message_batch", "processing_status": status, "created_at": created.UTC().Format(time.RFC3339Nano),
		"expires_at":     created.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano),
		"request_counts": map[string]int{"processing": 0, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0},
	}
}

func (m *messageBatches) end(id string, results ...map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.batches {
		if b["id"] == id {
			b["processing_status"] = "ended"
		}
	}
	m.results[id] = results
}

func succeeded(key, text, stop string) map[string]any {
	return map[string]any{"custom_id": key, "result": map[string]any{"type": "succeeded", "message": map[string]any{
		"id": "msg_" + key, "type": "message", "role": "assistant", "model": "claude-haiku-5-5", "stop_reason": stop,
		"content": []any{map[string]any{"type": "text", "text": text}}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
	}}}
}

func anthropicClient(t *testing.T, server *httptest.Server, maxTokens int) ai.Batcher {
	t.Helper()
	b, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderAnthropic, Endpoint: server.URL, Model: "claude-haiku-5-5", APIKey: "test-key", MaxTokens: maxTokens}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var receiptSchema = map[string]any{
	"type":     "object",
	"required": []any{"store", "lines"},
	"properties": map[string]any{
		"store":   map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"pattern": map[string]any{"type": "string"},
		"lines": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{
			"type": "object", "required": []any{"category"},
			"properties": map[string]any{"category": map[string]any{"type": "string", "enum": []any{"Bakery", "Other"}}},
		}},
	},
}

// A batch is one create with each request inline: the instructions as the
// system prompt, the images before the text and the schema as the output
// format, made fit for structured outputs.
func TestAnthropicBatchSendsMessagesRequestsInline(t *testing.T) {
	m, server := newMessageBatches(t)
	client := anthropicClient(t, server, 0)
	ctx := context.Background()
	requests := []ai.BatchRequest{{Key: strings.Repeat("a", 64), Completion: ai.Completion{
		Instructions: "Read this receipt.", Images: []ai.Image{{MediaType: "image/jpeg", Bytes: []byte("photo")}}, Schema: receiptSchema,
	}}}
	input, err := client.Prepare(ctx, requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.creates) != 0 {
		t.Fatal("prepare created a batch")
	}
	id, err := client.Create(ctx, input, "tag")
	if err != nil || id != "msgbatch_1" {
		t.Fatalf("create: %q %v", id, err)
	}
	if got := m.headers[0].Get("Authorization"); got != "Bearer test-key" || m.headers[0].Get("anthropic-beta") != "" {
		t.Fatalf("headers: %v", m.headers[0])
	}
	req := m.creates[0]["requests"].([]any)[0].(map[string]any)
	params := req["params"].(map[string]any)
	if req["custom_id"] != strings.Repeat("a", 64) || params["model"] != "claude-haiku-5-5" || params["max_tokens"] != float64(ai.DefaultMaxTokens) {
		t.Fatalf("request: %v", req)
	}
	if params["system"].([]any)[0].(map[string]any)["text"] != "Read this receipt." {
		t.Fatalf("system: %v", params["system"])
	}
	content := params["messages"].([]any)[0].(map[string]any)["content"].([]any)
	image := content[0].(map[string]any)
	source := image["source"].(map[string]any)
	if len(content) != 1 || image["type"] != "image" || source["type"] != "base64" || source["media_type"] != "image/jpeg" || source["data"] != "cGhvdG8=" {
		t.Fatalf("content: %v", content)
	}
	format := params["output_config"].(map[string]any)["format"].(map[string]any)
	schema := format["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	lines := props["lines"].(map[string]any)
	items := lines["items"].(map[string]any)
	if format["type"] != "json_schema" || schema["additionalProperties"] != false || items["additionalProperties"] != false {
		t.Fatalf("schema not closed: %v", schema)
	}
	if _, ok := props["store"].(map[string]any)["minLength"]; ok || lines["minItems"] != nil || props["pattern"] == nil {
		t.Fatalf("schema constraints: %v", schema)
	}
	if _, ok := receiptSchema["additionalProperties"]; ok || receiptSchema["properties"].(map[string]any)["store"].(map[string]any)["minLength"] != 1 {
		t.Fatal("the caller's schema changed")
	}
}

// A create that may have landed is uncertain and not retried; a refused
// one is a definite failure; long answers ask for extended output.
func TestAnthropicBatchCreateFailuresAndExtendedOutput(t *testing.T) {
	m, server := newMessageBatches(t)
	client := anthropicClient(t, server, 200_000)
	ctx := context.Background()
	input, err := client.Prepare(ctx, []ai.BatchRequest{{Key: "k", Completion: ai.Completion{Input: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	m.fail = http.StatusInternalServerError
	if _, err = client.Create(ctx, input, "t"); !errors.Is(err, ai.ErrUncertain) || len(m.creates) != 1 {
		t.Fatalf("5xx create: %v, %d creates", err, len(m.creates))
	}
	m.fail = http.StatusBadRequest
	_, err = client.Create(ctx, input, "t")
	var provider *ai.Error
	if !errors.As(err, &provider) || errors.Is(err, ai.ErrUncertain) || provider.Status != 400 {
		t.Fatalf("4xx create: %v", err)
	}
	if _, err = client.Create(ctx, input, "t"); err != nil {
		t.Fatal(err)
	}
	if got := m.headers[2].Get("anthropic-beta"); got != "output-300k-2026-03-24" {
		t.Fatalf("extended output header %q", got)
	}
	params := m.creates[2]["requests"].([]any)[0].(map[string]any)["params"].(map[string]any)
	if params["max_tokens"] != float64(200_000) || params["output_config"] != nil || params["system"] != nil {
		t.Fatalf("params: %v", params)
	}
	if _, err = ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderAnthropic, Endpoint: server.URL, Model: "m", MaxTokens: ai.MaxExtendedTokens + 1}, nil); err == nil {
		t.Fatal("accepted max tokens past extended output")
	}
	if _, err = ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: server.URL, Model: "m", MaxTokens: 10}, nil); err == nil {
		t.Fatal("accepted max tokens for an OpenAI batch")
	}
}

// An ended batch's results are answers by key: JSON for structured
// requests, failures for errored, expired, refused and cut-off ones.
func TestAnthropicBatchResults(t *testing.T) {
	m, server := newMessageBatches(t)
	client := anthropicClient(t, server, 0)
	ctx := context.Background()
	input, _ := client.Prepare(ctx, []ai.BatchRequest{{Key: "json", Completion: ai.Completion{Input: "x"}}})
	id, err := client.Create(ctx, input, "t")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := client.Get(ctx, id); err != nil || b.Ended {
		t.Fatalf("running batch: %+v %v", b, err)
	}
	m.end(id,
		succeeded("json", `{"store":"North Market"}`, "end_turn"),
		succeeded("text", "plain answer", "end_turn"),
		succeeded("refused", "", "refusal"),
		succeeded("cut", `{"store":`, "max_tokens"),
		map[string]any{"custom_id": "bad", "result": map[string]any{"type": "errored", "error": map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": "bad image"}}}},
		map[string]any{"custom_id": "late", "result": map[string]any{"type": "expired"}},
	)
	b, err := client.Get(ctx, id)
	if err != nil || !b.Ended {
		t.Fatalf("ended batch: %+v %v", b, err)
	}
	results, err := client.Results(ctx, b, map[string]bool{"json": true, "cut": true})
	if err != nil || len(results) != 6 {
		t.Fatalf("results: %+v %v", results, err)
	}
	got := map[string]ai.BatchResult{}
	for _, r := range results {
		got[r.Key] = r
	}
	if r := got["json"]; r.Err != nil || r.Answer.JSON["store"] != "North Market" {
		t.Fatalf("json: %+v", r)
	}
	if r := got["text"]; r.Err != nil || r.Answer.Text != "plain answer" {
		t.Fatalf("text: %+v", r)
	}
	for key, want := range map[string]string{"refused": "declined", "cut": "cut off", "bad": "bad image", "late": "expired"} {
		if r := got[key]; r.Err == nil || !strings.Contains(r.Err.Error(), want) {
			t.Fatalf("%s: %+v", key, r)
		}
	}
}

// Message batches have no metadata: an unconfirmed submission is the
// ended batch since it was prepared that holds exactly its keys, and while
// one that could be it runs, nobody can tell.
func TestAnthropicBatchFindsAnUnconfirmedSubmissionByItsKeys(t *testing.T) {
	m, server := newMessageBatches(t)
	client := anthropicClient(t, server, 0)
	ctx := context.Background()
	since := time.Now()
	m.batches = []map[string]any{
		batchJSON("running", "in_progress", since),
		batchJSON("other", "ended", since),
		batchJSON("ours", "ended", since.Add(time.Minute)),
		batchJSON("old", "ended", since.Add(-2*time.Hour)),
	}
	m.results["other"] = []map[string]any{succeeded("x", "a", "end_turn")}
	m.results["ours"] = []map[string]any{succeeded("k2", "a", "end_turn"), succeeded("k1", "b", "end_turn")}
	m.results["old"] = []map[string]any{succeeded("k1", "a", "end_turn")}
	want := ai.BatchSubmission{Tag: "t", Keys: []string{"k1", "k2", "k1"}, Since: since}
	if id, ok, err := client.Find(ctx, want); err != nil || !ok || id != "ours" {
		t.Fatalf("find: %q %v %v", id, ok, err)
	}
	missing := ai.BatchSubmission{Tag: "t", Keys: []string{"k9"}, Since: since}
	if _, ok, err := client.Find(ctx, missing); !errors.Is(err, ai.ErrUndetermined) || ok {
		t.Fatalf("a running candidate: %v %v", ok, err)
	}
	m.end("running", succeeded("y", "a", "end_turn"))
	m.resultsN = 0
	if _, ok, err := client.Find(ctx, missing); err != nil || ok {
		t.Fatalf("absent: %v %v", ok, err)
	}
	if m.resultsN != 3 {
		t.Fatalf("read the results of %d batches, want the 3 since the submission", m.resultsN)
	}
}
