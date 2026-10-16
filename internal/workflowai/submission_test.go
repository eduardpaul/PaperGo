package workflowai

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"papergo/internal/activitystore"
	"papergo/internal/ai"
	"papergo/internal/testutil"
	"papergo/internal/workflow"
)

// Both executions reach Upload before either can advance the intent. The
// remote side effect must still occur only once, even without provider-side
// idempotency support.
type racingBatch struct {
	uploads, creates, lookups atomic.Int32
	ready                     chan struct{}
}

func (*racingBatch) Key() string        { return "model" }
func (*racingBatch) Model() string      { return "model" }
func (*racingBatch) Connection() string { return "connection" }
func (b *racingBatch) Upload(ctx context.Context, _ []byte) (string, error) {
	if b.uploads.Add(1) == 2 {
		close(b.ready)
	}
	select {
	case <-b.ready:
		return "file", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (b *racingBatch) Create(context.Context, string, string) (string, error) {
	b.creates.Add(1)
	return "batch", nil
}
func (b *racingBatch) FindByTag(context.Context, string) (string, bool, error) {
	b.lookups.Add(1)
	return "", false, ai.ErrUncertain
}
func (*racingBatch) Get(context.Context, string) (ai.Batch, error) {
	return ai.Batch{ID: "batch", Status: "in_progress"}, nil
}
func (*racingBatch) Download(context.Context, string) ([]byte, error) { panic("not terminal") }

func TestConcurrentSubmissionReplaysCreateOnlyOnce(t *testing.T) {
	db := testutil.Database(t)
	tools := workflow.Tools{Records: activitystore.New(db.SQL)}
	batch := &racingBatch{ready: make(chan struct{})}
	Configure(&tools, Runtime{Batch: batch})
	ctx := context.Background()
	body, _ := json.Marshal(requestBody{Activity: "ai.complete", Model: "model", Input: "receipt", Prompt: "Read"})
	if err := tools.Records.Put(ctx, activitystore.Record{Namespace: requestCache, Scope: answerScope, Key: "request", Payload: body}); err != nil {
		t.Fatal(err)
	}
	intent, _ := json.Marshal(flightData{Status: "prepared", Hashes: []string{"request"}, Queue: "shared", Connection: "connection", Model: "model"})
	if err := tools.Records.Put(ctx, activitystore.Record{Namespace: kindFlight, Scope: flightScope("shared", "connection", "model"), Key: "intent", Payload: intent}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			submitGroup(ctx, tools, "connection", groupSubmit{Tag: "intent", Queue: "shared", Hashes: []string{"request"}})
		})
	}
	wg.Wait()
	if n := batch.lookups.Load(); n != 0 {
		t.Fatalf("a fresh prepared intent depended on provider history: %d lookups", n)
	}
	if n := batch.creates.Load(); n != 1 {
		t.Fatalf("concurrent replays billed %d batches", n)
	}
}

func TestLateResponseWithoutAConsumerCanExpire(t *testing.T) {
	db := testutil.Database(t)
	tools := workflow.Tools{Records: activitystore.New(db.SQL)}
	ctx := context.Background()
	if err := saveResponse(ctx, tools, "collected-request", "late-result", cachedAnswer{Text: "late"}); err != nil {
		t.Fatal(err)
	}
	if err := tools.Records.Purge(ctx, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM activity_records`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late response was retained without a consumer: %d %v", count, err)
	}
}
