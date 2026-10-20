package aiops_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/ai"
	"papergo/internal/aiops"
	"papergo/internal/auth"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resultcache"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const token = "0123456789abcdef0123456789abcdef"

type fixture struct {
	s     *dms.Service
	h     http.Handler
	a     *aiops.Assistant
	ws    string
	chat  *fakeChat
	batch *fakeBatch
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: s, chat: &fakeChat{}, batch: &fakeBatch{batches: map[string]*fakeBatchJob{}}}
	f.a = &aiops.Assistant{Storage: store, Chat: f.chat, Batch: f.batch, Decisions: fakeDecisions{}, Results: resultcache.New(db.SQL), ResultTTL: time.Hour, Records: activitystore.New(db.SQL)}
	if err = f.a.Register(s); err != nil {
		t.Fatal(err)
	}
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Modules: []httpapi.Module{f.a.Module()}}
	if err = a.Validate(); err != nil {
		t.Fatal(err)
	}
	f.h = a.Handler()
	ws, err := s.Create(context.Background(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws.ID
	return f
}

func (f *fixture) start(t *testing.T, path, body string) dms.OperationView {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	var op dms.OperationView
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &op) != nil {
		t.Fatal(path, w.Code, w.Body.String())
	}
	return op
}

func (f *fixture) refused(t *testing.T, path, body string, code int) {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != code {
		t.Fatalf("%s %s: %d %s", path, body, w.Code, w.Body.String())
	}
}

func (f *fixture) operation(t *testing.T, id string) *dms.OperationView {
	t.Helper()
	op, err := f.s.Operation(context.Background(), "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

// Immediate completions call the chat model once per distinct request;
// the same request reuses the cached answer, and an answer that does not
// match the output schema fails the operation.
func TestImmediateCompletion(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.refused(t, "/v1/ai/completions", `{"prompt":"Summarize","input":"text"}`, 422)
	f.refused(t, "/v1/ai/completions", `{"prompt":"","input":"text","workspace_id":"`+f.ws+`"}`, 422)
	body := `{"prompt":"Summarize","input":"A long letter","workspace_id":"` + f.ws + `","execution":"immediate"}`
	first := f.start(t, "/v1/ai/completions", body)
	if err := worker.Drain(ctx, f.s); err != nil {
		t.Fatal(err)
	}
	var answer aiops.Answer
	if op := f.operation(t, first.ID); op.Status != operations.StatusSucceeded || json.Unmarshal(op.Result, &answer) != nil || answer.Text != "answer to Summarize" || answer.Cached {
		t.Fatalf("%+v %+v", op, answer)
	}
	second := f.start(t, "/v1/ai/completions", body)
	if err := worker.Drain(ctx, f.s); err != nil {
		t.Fatal(err)
	}
	if op := f.operation(t, second.ID); json.Unmarshal(op.Result, &answer) != nil || !answer.Cached || f.chat.calls() != 1 {
		t.Fatalf("%+v %+v after %d calls", op, answer, f.chat.calls())
	}
	schema := `{"prompt":"Extract","input":"Total 7","workspace_id":"` + f.ws + `","execution":"immediate","output_schema":{"type":"object","required":["total"],"properties":{"total":{"type":"string"}}}}`
	bad := f.start(t, "/v1/ai/completions", schema)
	if err := worker.Drain(ctx, f.s); err == nil {
		t.Fatal("the mismatched answer was accepted")
	}
	if op := f.operation(t, bad.ID); op.Status != operations.StatusFailed || !strings.Contains(op.Error, "output_schema") {
		t.Fatalf("%+v", op)
	}
}

// Batch completions are admitted in groups per queue and sent as one
// provider batch; their operations park until the batch finishes, and a
// request the provider failed returns to the queue.
func TestBatchCompletion(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.batch.failing = "fail me"
	var ids []string
	for _, input := range []string{"one", "two", "fail me", "one"} {
		ids = append(ids, f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"`+input+`","workspace_id":"`+f.ws+`","execution":"batch","queue":"receipts"}`).ID)
	}
	groups, err := f.s.ReadyOperationGroups(ctx, []string{aiops.KindBatch})
	if err != nil || len(groups) != 1 || groups[0].Key != "receipts" || groups[0].Inputs != 3 {
		t.Fatalf("%+v %v", groups, err)
	}
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "receipts", nil, 20); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if n := len(f.batch.batches); n != 1 {
		t.Fatalf("%d provider batches", n)
	}
	for _, id := range ids {
		if op := f.operation(t, id); op.Status != operations.StatusPending || op.Waiting != dms.WaitingRemote {
			t.Fatalf("%s: %+v", id, op)
		}
	}
	// The provider has not finished: the members stay parked.
	if err = f.a.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if op := f.operation(t, ids[0]); op.Status != operations.StatusPending {
		t.Fatalf("%+v", op)
	}
	f.batch.finish()
	if err = f.a.Poll(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var answer aiops.Answer
	for _, i := range []int{0, 1, 3} {
		if op := f.operation(t, ids[i]); op.Status != operations.StatusSucceeded || json.Unmarshal(op.Result, &answer) != nil || answer.Text == "" {
			t.Fatalf("%d: %+v", i, op)
		}
	}
	if op := f.operation(t, ids[2]); op.Status != operations.StatusPending || op.Waiting == dms.WaitingRemote {
		t.Fatalf("the failed request did not return to the queue: %+v", op)
	}
	// A request answered before does not wait for the next batch.
	again := f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"one","workspace_id":"`+f.ws+`","execution":"batch","queue":"receipts"}`)
	if again.Kind != aiops.KindComplete {
		t.Fatalf("a cached request was queued: %+v", again)
	}
	if worked, err := f.s.RunOperation(ctx, []string{aiops.KindComplete}); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if op := f.operation(t, again.ID); op.Status != operations.StatusSucceeded || json.Unmarshal(op.Result, &answer) != nil || !answer.Cached || f.chat.calls() != 0 {
		t.Fatalf("%+v %+v", op, answer)
	}
}

// Decisions run on the local engine; a document decision fails when the
// item's file changed while it ran.
func TestBatchRetriesExhaustAttempts(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.batch.failing = "fail me"
	op := f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"fail me","workspace_id":"`+f.ws+`","execution":"batch"}`)
	for attempt := 1; attempt <= dms.DefaultMaxAttempts; attempt++ {
		worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20)
		if err != nil || !worked {
			t.Fatalf("attempt %d: %v %v", attempt, worked, err)
		}
		if batches := len(f.batch.batches); batches != attempt {
			t.Fatalf("attempt %d reused submission identity: %d batches", attempt, batches)
		}
		if parked := f.operation(t, op.ID); parked.Attempts != attempt || parked.Waiting != dms.WaitingRemote {
			t.Fatalf("attempt %d: %+v", attempt, parked)
		}
		f.batch.finish()
		if err := f.a.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if ended := f.operation(t, op.ID); ended.Status != operations.StatusFailed || ended.ErrorCode != operations.ErrorRetriesExhausted {
		t.Fatalf("retry budget not exhausted: %+v", ended)
	}
	if len(f.batch.batches) != dms.DefaultMaxAttempts {
		t.Fatal("submitted another provider batch after exhausting retries")
	}
}

func TestDecision(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.refused(t, "/v1/ai/decisions", `{"workspace_id":"`+f.ws+`","questions":[{"key":"route","instructions":"Which team?","options":[{"key":"a","text":"A"},{"key":"b","text":"B"}]}]}`, 422)
	op := f.start(t, "/v1/ai/decisions", `{"workspace_id":"`+f.ws+`","input":"Refund my invoice","questions":[{"key":"route","type":"choice","instructions":"Which team?","options":[{"key":"billing","text":"Billing"},{"key":"technical","text":"Technical"}]}]}`)
	if err := worker.Drain(ctx, f.s); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Answers map[string]struct {
			Selected string `json:"selected"`
		} `json:"answers"`
	}
	if got := f.operation(t, op.ID); got.Status != operations.StatusSucceeded || json.Unmarshal(got.Result, &result) != nil || result.Answers["route"].Selected != "billing" {
		t.Fatalf("%+v %+v", got, result)
	}
}

type fakeChat struct {
	mu sync.Mutex
	n  int
}

func (*fakeChat) Key() string { return "fake-chat" }

func (c *fakeChat) Complete(_ context.Context, in ai.Completion) (ai.Answer, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	if in.Schema != nil {
		return ai.Answer{JSON: map[string]any{"wrong": true}}, nil
	}
	return ai.Answer{Text: "answer to " + in.Instructions}, nil
}

func (c *fakeChat) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type fakeBatchJob struct {
	lines    []byte
	finished bool
}

// fakeBatch answers every line, except those whose input is failing.
type fakeBatch struct {
	mu      sync.Mutex
	files   []string
	batches map[string]*fakeBatchJob
	failing string
}

func (*fakeBatch) Key() string        { return "fake-batch" }
func (*fakeBatch) Connection() string { return "fake" }
func (*fakeBatch) Model() string      { return "fake-model" }

func (b *fakeBatch) Upload(_ context.Context, jsonl []byte) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files = append(b.files, string(jsonl))
	return fmt.Sprint("file-", len(b.files)), nil
}

func (b *fakeBatch) Create(_ context.Context, file, tag string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var n int
	fmt.Sscanf(file, "file-%d", &n)
	b.batches[tag] = &fakeBatchJob{lines: []byte(b.files[n-1])}
	return tag, nil
}

func (b *fakeBatch) FindByTag(_ context.Context, tag string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.batches[tag]
	return tag, ok, nil
}

func (b *fakeBatch) finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, job := range b.batches {
		job.finished = true
	}
}

func (b *fakeBatch) Get(_ context.Context, id string) (ai.Batch, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.batches[id].finished {
		return ai.Batch{ID: id, Status: "in_progress"}, nil
	}
	return ai.Batch{ID: id, Status: "completed", OutputFileID: id}, nil
}

func (b *fakeBatch) Download(_ context.Context, id string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []byte
	for _, row := range strings.Split(strings.TrimSpace(string(b.batches[id].lines)), "\n") {
		var line struct {
			CustomID string `json:"custom_id"`
		}
		_ = json.Unmarshal([]byte(row), &line)
		status, body := 200, `{"choices":[{"message":{"content":"batched answer"}}]}`
		if b.failing != "" && strings.Contains(row, b.failing) {
			status, body = 500, `{}`
		}
		raw, _ := json.Marshal(map[string]any{"custom_id": line.CustomID, "response": map[string]any{"status_code": status, "body": json.RawMessage(body)}})
		out = append(append(out, raw...), '\n')
	}
	return out, nil
}

// fakeDecisions picks the first option with certainty.
type fakeDecisions struct{}

func (fakeDecisions) ID() string { return "fake-decisions" }

func (fakeDecisions) Decide(_ context.Context, rows []decision.Request) ([]decision.Result, error) {
	out := make([]decision.Result, len(rows))
	for i, r := range rows {
		p := make([]float64, len(r.Options))
		p[0] = 1
		out[i] = decision.Result{Probabilities: p, Tokens: 1}
	}
	return out, nil
}
