package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
)

func TestMain(m *testing.M) { daltest.Main(m) }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fixture is a service with alice's list and a runtime over it.
type fixture struct {
	s    *dms.Service
	rt   *Runtime
	list string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	s := dms.NewService(daltest.Database(t).SQL)
	ws, err := s.Create(context.Background(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(context.Background(), "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{s: s, rt: New(s, quiet), list: list.ID}
}

func accept(context.Context, *dms.Service, string, string, json.RawMessage) error { return nil }

// register adds a handler for kind that records which operations ran.
func (f *fixture) register(t *testing.T, kind string, handler dms.OperationHandler) {
	t.Helper()
	handler.Scope, handler.Validate = "list.write", accept
	if err := f.s.RegisterOperationHandler(kind, handler); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) enqueue(t *testing.T, kind, payload string) string {
	t.Helper()
	op, err := f.s.EnqueueOperation(context.Background(), "alice", f.list, kind, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	return op.ID
}

// own gives every built-in kind to one worker, beside the test's.
func (f *fixture) own(extra ...string) {
	var kinds []string
	for _, k := range f.s.OperationKinds() {
		if !strings.HasPrefix(k, "test.") {
			kinds = append(kinds, k)
		}
	}
	f.rt.Add(Definition{Name: "maintenance", Kinds: kinds})
}

func (f *fixture) status(t *testing.T, id string) operations.Operation {
	t.Helper()
	op, err := f.s.DAL.Operations().Find(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f *fixture) start(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = f.rt.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("the runtime did not stop")
		}
	})
	return cancel
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func finish(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	return s.Write(ctx, func(t *dms.Service) error {
		op.Status = operations.StatusSucceeded
		return t.SaveOperation(ctx, op)
	})
}

func TestValidateRequiresOneWorkerPerKind(t *testing.T) {
	f := setup(t)
	f.register(t, "test.a", dms.OperationHandler{Execute: func(context.Context, *dms.Service, *operations.Operation) error { return nil }})
	if err := f.rt.Validate(); err == nil || !strings.Contains(err.Error(), "no worker runs") {
		t.Fatal("unowned kinds accepted", err)
	}
	f.own()
	f.rt.Add(Definition{Name: "a", Kinds: []string{"test.a"}})
	f.rt.Add(Definition{Name: "b", Kinds: []string{"test.a"}})
	if err := f.rt.Validate(); err == nil || !strings.Contains(err.Error(), "belongs to workers") {
		t.Fatal("a kind with two workers accepted", err)
	}
	f.rt.defs = f.rt.defs[:2]
	f.rt.Add(Definition{Name: "c", Kinds: []string{"test.unknown"}})
	if err := f.rt.Validate(); err == nil {
		t.Fatal("an unknown kind accepted")
	}
	f.rt.defs = f.rt.defs[:2]
	f.rt.Worker("a").Admission = Admission{Policy: Threshold}
	if err := f.rt.Validate(); err == nil {
		t.Fatal("a threshold policy without a threshold accepted")
	}
	f.rt.Worker("a").Admission = Admission{Policy: Threshold, Threshold: 2, Batch: 10}
	if err := f.rt.Validate(); err == nil || !strings.Contains(err.Error(), "does not run in groups") {
		t.Fatal("a group policy for a single-execution kind accepted", err)
	}
}

// Long work in one worker does not hold up another worker's work, and
// accepted work wakes its worker at once.
func TestWorkersRunTheirKindsIndependently(t *testing.T) {
	f := setup(t)
	release := make(chan struct{})
	f.register(t, "test.slow", dms.OperationHandler{Execute: func(ctx context.Context, s *dms.Service, op *operations.Operation) error {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return finish(ctx, s, op)
	}})
	f.register(t, "test.fast", dms.OperationHandler{Execute: func(ctx context.Context, s *dms.Service, op *operations.Operation) error {
		return finish(ctx, s, op)
	}})
	f.own()
	f.rt.Add(Definition{Name: "slow", Kinds: []string{"test.slow"}})
	f.rt.Add(Definition{Name: "fast", Kinds: []string{"test.fast"}, Concurrency: 2})
	f.start(t)
	slow := f.enqueue(t, "test.slow", `{}`)
	eventually(t, "the slow operation did not start", func() bool { return f.status(t, slow).StartedAt != nil })
	fast := f.enqueue(t, "test.fast", `{}`)
	eventually(t, "the fast worker waited behind the slow one", func() bool { return f.status(t, fast).Status == operations.StatusSucceeded })
	close(release)
	eventually(t, "the slow operation did not finish", func() bool { return f.status(t, slow).Status == operations.StatusSucceeded })
}

// Stopping gives running executions the drain time, then cancels them and
// gives their operations back for the next process.
func TestShutdownGivesUnfinishedWorkBack(t *testing.T) {
	f := setup(t)
	started := make(chan struct{})
	f.register(t, "test.long", dms.OperationHandler{Execute: func(ctx context.Context, _ *dms.Service, _ *operations.Operation) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	f.own()
	f.rt.Add(Definition{Name: "long", Kinds: []string{"test.long"}})
	f.rt.DrainTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = f.rt.Run(ctx); close(done) }()
	id := f.enqueue(t, "test.long", `{}`)
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime did not stop")
	}
	op := f.status(t, id)
	if op.Status != operations.StatusPending || op.Claim.Token != "" || op.Attempts != 0 {
		t.Fatalf("unfinished work after shutdown: %+v", op)
	}
}

// group is a group handler that finishes its members and records the
// sizes of its executions.
type group struct {
	mu    sync.Mutex
	sizes []int
}

func (g *group) handler() dms.OperationHandler {
	return dms.OperationHandler{
		Admission: func(payload json.RawMessage) (string, string, error) {
			var p struct{ Group, Input string }
			err := json.Unmarshal(payload, &p)
			return p.Group, p.Input, err
		},
		ExecuteGroup: func(ctx context.Context, s *dms.Service, ops []*operations.Operation) error {
			g.mu.Lock()
			g.sizes = append(g.sizes, len(ops))
			g.mu.Unlock()
			for _, op := range ops {
				if err := finish(ctx, s, op); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func (g *group) executions() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int(nil), g.sizes...)
}

// A group runs once it holds the threshold of distinct inputs, in batches,
// and not before.
func TestThresholdAdmission(t *testing.T) {
	f := setup(t)
	g := &group{}
	f.register(t, "test.batch", g.handler())
	f.own()
	f.rt.Add(Definition{Name: "batch", Kinds: []string{"test.batch"}, Admission: Admission{Policy: Threshold, Threshold: 3, Batch: 10}})
	f.start(t)
	f.enqueue(t, "test.batch", `{"group":"a","input":"x"}`)
	f.enqueue(t, "test.batch", `{"group":"a","input":"x"}`)
	f.enqueue(t, "test.batch", `{"group":"a","input":"y"}`)
	f.enqueue(t, "test.batch", `{"group":"b","input":"z"}`)
	time.Sleep(200 * time.Millisecond)
	if sizes := g.executions(); len(sizes) != 0 {
		t.Fatalf("ran below the threshold: %v", sizes)
	}
	// The third distinct input of group a reaches the threshold.
	f.enqueue(t, "test.batch", `{"group":"a","input":"w"}`)
	eventually(t, "the full group did not run", func() bool { return len(g.executions()) == 1 })
	if sizes := g.executions(); sizes[0] != 4 {
		t.Fatalf("executions: %v", sizes)
	}
}

// A grouped worker runs up to its concurrency of groups at once.
func TestGroupedConcurrency(t *testing.T) {
	f := setup(t)
	var running, most atomic.Int64
	both := make(chan struct{})
	var once sync.Once
	f.register(t, "test.batch", dms.OperationHandler{
		Admission: (&group{}).handler().Admission,
		ExecuteGroup: func(ctx context.Context, s *dms.Service, ops []*operations.Operation) error {
			n := running.Add(1)
			defer running.Add(-1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			if n == 2 {
				once.Do(func() { close(both) })
			}
			select {
			case <-both:
			case <-time.After(5 * time.Second):
			}
			for _, op := range ops {
				if err := finish(ctx, s, op); err != nil {
					return err
				}
			}
			return nil
		},
	})
	f.own()
	f.rt.Add(Definition{Name: "batch", Kinds: []string{"test.batch"}, Concurrency: 2, Admission: Admission{Policy: Threshold, Threshold: 1, Batch: 10}})
	a := f.enqueue(t, "test.batch", `{"group":"a","input":"x"}`)
	b := f.enqueue(t, "test.batch", `{"group":"b","input":"y"}`)
	f.start(t)
	eventually(t, "the groups did not finish", func() bool {
		return f.status(t, a).Status == operations.StatusSucceeded && f.status(t, b).Status == operations.StatusSucceeded
	})
	if most.Load() != 2 {
		t.Fatalf("at most %d groups ran at once", most.Load())
	}
}

// Past its maximum wait a partial group drains, in bounded executions, up
// to the cutoff; an operation added later keeps its own deadline.
func TestMaxWaitAdmissionDrainsTheFrozenBacklog(t *testing.T) {
	f := setup(t)
	g := &group{}
	f.register(t, "test.batch", g.handler())
	f.own()
	f.rt.Add(Definition{Name: "batch", Kinds: []string{"test.batch"}, Admission: Admission{Policy: ThresholdOrMaxWait, Threshold: 100, MaxWait: time.Hour, Batch: 2}})
	for _, input := range []string{"a", "b", "c"} {
		f.enqueue(t, "test.batch", `{"group":"g","input":"`+input+`"}`)
	}
	w := &worker{rt: f.rt, def: f.rt.Worker("batch")}
	ctx := context.Background()
	next := time.Now().Add(time.Hour * 24)
	records := recordsOf(f.s)
	if ran, err := w.admit(ctx, ctx, records, time.Now(), false, &next); err != nil || ran {
		t.Fatal("ran before the deadline", ran, err)
	}
	if next.Before(time.Now().Add(59*time.Minute)) || next.After(time.Now().Add(time.Hour)) {
		t.Fatal("next eligibility is not the group's deadline", next)
	}
	later := time.Now().Add(2 * time.Hour)
	for {
		ran, err := w.admit(ctx, ctx, records, later, false, &next)
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			break
		}
	}
	if sizes := g.executions(); len(sizes) != 2 || sizes[0] != 2 || sizes[1] != 1 {
		t.Fatalf("drain executions: %v", sizes)
	}
}

func TestSchedulesAndWindows(t *testing.T) {
	s, err := ParseSchedule("0 1 * * *", "Europe/Madrid", MissedOnce)
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 10, 19, 23, 0, 0, 0, time.UTC) // 01:00 in Madrid
	if _, due := s.Due(last, last.Add(23*time.Hour)); due {
		t.Fatal("due before the next occurrence")
	}
	// Three missed days run once, as the latest occurrence.
	occurrence, due := s.Due(last, last.Add(72*time.Hour+time.Minute))
	if !due || !occurrence.Equal(last.Add(72*time.Hour)) {
		t.Fatal("missed occurrences", occurrence, due)
	}
	skip, _ := ParseSchedule("0 1 * * *", "Europe/Madrid", MissedSkip)
	if _, due = skip.Due(last, last.Add(72*time.Hour+time.Hour)); due {
		t.Fatal("a skipped occurrence ran")
	}
	if _, err = ParseSchedule("0 1 * * *", "", MissedOnce); err == nil {
		t.Fatal("a schedule without a time zone")
	}
	w, err := ParseWindow("UTC", "22:00", "06:00")
	if err != nil {
		t.Fatal(err)
	}
	if open, _ := w.Open(time.Date(2026, 10, 20, 23, 0, 0, 0, time.UTC)); !open {
		t.Fatal("closed inside a window spanning midnight")
	}
	open, opens := w.Open(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	if open || !opens.Equal(time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)) {
		t.Fatal("window opening", open, opens)
	}
}

// A paused worker admits nothing until it resumes.
func TestPauseAndResume(t *testing.T) {
	f := setup(t)
	f.register(t, "test.fast", dms.OperationHandler{Execute: func(ctx context.Context, s *dms.Service, op *operations.Operation) error {
		return finish(ctx, s, op)
	}})
	f.own()
	f.rt.Add(Definition{Name: "fast", Kinds: []string{"test.fast"}})
	f.rt.ControlPoll = 10 * time.Millisecond
	ctx := context.Background()
	if err := SetControl(ctx, f.s.DAL.SQLDB(), "fast", Paused); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	time.Sleep(50 * time.Millisecond)
	id := f.enqueue(t, "test.fast", `{}`)
	time.Sleep(200 * time.Millisecond)
	if op := f.status(t, id); op.Status != operations.StatusPending {
		t.Fatal("a paused worker ran work", op.Status)
	}
	if err := SetControl(ctx, f.s.DAL.SQLDB(), "fast", Running); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the resumed worker did not run", func() bool { return f.status(t, id).Status == operations.StatusSucceeded })
	statuses, err := Statuses(ctx, f.s.DAL.SQLDB())
	if err != nil || len(statuses) == 0 {
		t.Fatal(statuses, err)
	}
	if err = SetControl(ctx, f.s.DAL.SQLDB(), "fast", State("asleep")); err == nil {
		t.Fatal(errors.New("unknown state accepted"))
	}
}

func recordsOf(s *dms.Service) *activitystore.Store { return activitystore.New(s.DAL.SQLDB()) }
