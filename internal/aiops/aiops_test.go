package aiops_test

import (
	"fmt"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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

// A deployment runs only the operations of the models it has, and refuses
// requests for the others. With no model it runs none.
func TestKindsFollowModels(t *testing.T) {
	kinds := func(a *aiops.Assistant) []string {
		t.Helper()
		s := dms.NewService(daltest.Database(t).SQL)
		before := s.OperationKinds()
		if err := a.Register(s); err != nil {
			t.Fatal(err)
		}
		var added []string
		for _, kind := range s.OperationKinds() {
			if !slices.Contains(before, kind) {
				added = append(added, kind)
			}
		}
		slices.Sort(added)
		return added
	}
	for _, tc := range []struct {
		name string
		a    *aiops.Assistant
		want []string
	}{
		{"none", &aiops.Assistant{}, nil},
		{"chat", &aiops.Assistant{Chat: &fakeChat{}}, []string{aiops.KindComplete}},
		{"batch", &aiops.Assistant{Batch: &fakeBatch{}}, []string{aiops.KindBatch}},
		{"decisions", &aiops.Assistant{Decisions: fakeDecisions{}}, []string{aiops.KindDecide, aiops.KindTaxonomy}},
	} {
		if got := kinds(tc.a); !slices.Equal(got, tc.want) {
			t.Errorf("%s: kinds %v, want %v", tc.name, got, tc.want)
		}
	}
	f := setup(t)
	f.a.Chat = nil
	f.refused(t, "/v1/ai/completions", `{"prompt":"Summarize","input":"text","workspace_id":"`+f.ws+`","execution":"immediate"}`, 422)
}

// Work queued for a model this start lacks ends with what it needs, whether
// it waits, a stopped process claimed it or a provider batch holds it, and
// work for the models it has stays queued.
func TestEndOrphans(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	completion := func(execution, input string) string {
		return f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"`+input+`","workspace_id":"`+f.ws+`","execution":"`+execution+`"}`).ID
	}
	waiting, claimed := completion("immediate", "waiting"), completion("immediate", "claimed")
	// A process stopped while it ran this one, leaving its claim behind.
	if _, err := f.s.DAL.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{aiops.KindComplete}, IDs: []string{claimed}, Now: time.Now(), Limit: 1}, Holder: "stopped", TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	parked := completion("batch", "parked")
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if op := f.operation(t, parked); op.Status != operations.StatusPending || op.Waiting != dms.WaitingRemote {
		t.Fatalf("the batch member: %+v", op)
	}
	queued := completion("batch", "queued")

	// The next start has the batch provider but no chat model.
	if err := (&aiops.Assistant{Batch: f.batch}).EndOrphans(ctx, f.s); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{waiting, claimed} {
		if op := f.operation(t, id); op.Status != operations.StatusFailed || op.Error != aiops.ErrNoChat.Error() {
			t.Fatalf("immediate completion %s: %+v", id, op)
		}
	}
	for _, id := range []string{parked, queued} {
		if op := f.operation(t, id); op.Status != operations.StatusPending {
			t.Fatalf("batch completion %s with its provider: %+v", id, op)
		}
	}
	// The one after has no model at all.
	if err := (&aiops.Assistant{}).EndOrphans(ctx, f.s); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parked, queued} {
		if op := f.operation(t, id); op.Status != operations.StatusFailed || op.Error != aiops.ErrNoBatch.Error() {
			t.Fatalf("batch completion %s: %+v", id, op)
		}
	}
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
	if again.Kind != aiops.KindBatch || again.Status != operations.StatusSucceeded {
		t.Fatalf("a cached request was queued: %+v", again)
	}
	if op := f.operation(t, again.ID); op.Status != operations.StatusSucceeded || json.Unmarshal(op.Result, &answer) != nil || !answer.Cached || f.chat.calls() != 0 {
		t.Fatalf("%+v %+v", op, answer)
	}
}

// Decisions run on the local engine; a document decision fails when the
// item's file changed while it ran.
func TestBatchIdempotencySurvivesCacheChanges(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	body := `{"prompt":"Classify","input":"one","workspace_id":"` + f.ws + `","execution":"batch"}`
	call := func(key string, want int) dms.OperationView {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/ai/completions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		var op dms.OperationView
		if w.Code != want || json.Unmarshal(w.Body.Bytes(), &op) != nil {
			t.Fatalf("%s: %d %s", key, w.Code, w.Body.String())
		}
		return op
	}
	first := call("cold", http.StatusAccepted)
	if replay := call("cold", http.StatusOK); replay.ID != first.ID {
		t.Fatal("pending request was not replayed")
	}
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || !worked {
		t.Fatal(worked, err)
	}
	f.batch.finish()
	if err := f.a.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if replay := call("cold", http.StatusOK); replay.ID != first.ID || replay.Status != operations.StatusSucceeded {
		t.Fatalf("cache warming changed identity: %+v", replay)
	}
	warm := call("warm", http.StatusAccepted)
	if warm.Kind != aiops.KindBatch || warm.Status != operations.StatusSucceeded {
		t.Fatalf("cache hit was not completed at acceptance: %+v", warm)
	}
	// Workflow requests already own a transaction; cache lookup and
	// completion must use that same transaction.
	err := f.s.Write(ctx, func(s *dms.Service) error {
		op, err := f.a.StartCompletion(ctx, s, "alice", aiops.CompletionRequest{Prompt: "Classify", Input: "one", WorkspaceID: f.ws, Execution: "batch"})
		if err == nil && (op.Kind != aiops.KindBatch || op.Status != operations.StatusSucceeded) {
			t.Fatalf("transactional cache hit: %+v", op)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.a.Collect(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if replay := call("warm", http.StatusOK); replay.ID != warm.ID || replay.Status != operations.StatusSucceeded {
		t.Fatalf("cache expiry changed identity: %+v", replay)
	}
	if len(f.batch.batches) != 1 || f.chat.calls() != 0 {
		t.Fatal("replays or cache hits called a provider")
	}
}

// A request the model refused fails at once: asking again would end the same way.
func TestBatchRefusedAnswerFails(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.batch.refusing = "refuse me"
	op := f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"refuse me","workspace_id":"`+f.ws+`","execution":"batch"}`)
	if _, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil {
		t.Fatal(err)
	}
	f.batch.finish()
	if err := f.a.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ended := f.operation(t, op.ID); ended.Status != operations.StatusFailed || !strings.Contains(ended.Error, "declined") || len(f.batch.batches) != 1 {
		t.Fatalf("a refused request: %+v, %d batches", ended, len(f.batch.batches))
	}
}

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

// A submission that stops once the provider holds its batch leaves its
// members with the batch, which Poll finds by its tag: they are never sent
// in a second batch.
func TestBatchSubmissionStoppingAfterCreate(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.batch.crash = true
	op := f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"one","workspace_id":"`+f.ws+`","execution":"batch"}`)
	if _, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err == nil {
		t.Fatal("the submission did not stop")
	}
	f.batch.crash = false
	if parked := f.operation(t, op.ID); parked.Status != operations.StatusPending || parked.Waiting != dms.WaitingRemote {
		t.Fatalf("the member left its batch: %+v", parked)
	}
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || worked {
		t.Fatal("the member was claimable again", worked, err)
	}
	f.batch.finish()
	if err := f.a.Poll(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if done := f.operation(t, op.ID); done.Status != operations.StatusSucceeded || len(f.batch.batches) != 1 {
		t.Fatalf("%d batches: %+v", len(f.batch.batches), done)
	}
}

// A batch the provider refuses gives its members back with the reason, to
// try again after the poll interval.
func TestBatchRefusedByProvider(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	f.batch.refuse = true
	op := f.start(t, "/v1/ai/completions", `{"prompt":"Classify","input":"one","workspace_id":"`+f.ws+`","execution":"batch"}`)
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || !worked {
		t.Fatal(worked, err)
	}
	back := f.operation(t, op.ID)
	if back.Status != operations.StatusPending || back.Waiting == dms.WaitingRemote || back.Attempts != 1 || !strings.Contains(back.Error, "refused") {
		t.Fatalf("%+v", back)
	}
	if worked, err := f.s.RunOperationGroup(ctx, aiops.KindBatch, "shared", nil, 20); err != nil || worked {
		t.Fatal("the member was retried before its delay", worked, err)
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
	requests []ai.BatchRequest
	finished bool
}

// fakeBatch answers every request, except those whose input is failing.
type fakeBatch struct {
	mu       sync.Mutex
	prepared []ai.BatchRequest
	batches  map[string]*fakeBatchJob
	failing  string
	// refusing is what the model declines to answer.
	refusing string
	// crash stops the submission once the provider created the batch;
	// refuse makes the provider refuse to create it.
	crash, refuse bool
}

func (*fakeBatch) Key() string        { return "fake-batch" }
func (*fakeBatch) Connection() string { return "fake" }
func (*fakeBatch) Model() string      { return "fake-model" }

func (b *fakeBatch) Prepare(_ context.Context, requests []ai.BatchRequest) (ai.BatchInput, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prepared = requests
	return ai.BatchInput{FileID: "prepared"}, nil
}

func (b *fakeBatch) Create(_ context.Context, _ ai.BatchInput, tag string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refuse {
		return "", errors.New("the provider refused the batch")
	}
	b.batches[tag] = &fakeBatchJob{requests: b.prepared}
	if b.crash {
		panic("the process stopped")
	}
	return tag, nil
}

func (b *fakeBatch) Find(_ context.Context, s ai.BatchSubmission) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.batches[s.Tag]
	return s.Tag, ok, nil
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
	return ai.Batch{ID: id, Ended: b.batches[id].finished}, nil
}

func (b *fakeBatch) Results(_ context.Context, batch ai.Batch, _ map[string]bool) ([]ai.BatchResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []ai.BatchResult
	for _, r := range b.batches[batch.ID].requests {
		result := ai.BatchResult{Key: r.Key, Answer: ai.Answer{Text: "batched answer"}}
		if b.failing != "" && strings.Contains(r.Completion.Input, b.failing) {
			result = ai.BatchResult{Key: r.Key, Err: errors.New("the provider rejected the request")}
		}
		if b.refusing != "" && strings.Contains(r.Completion.Input, b.refusing) {
			result = ai.BatchResult{Key: r.Key, Err: fmt.Errorf("%w: it declined to answer", ai.ErrNoAnswer)}
		}
		out = append(out, result)
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
