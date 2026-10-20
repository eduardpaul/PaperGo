package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"papergo/internal/dal/runs"
	"papergo/sdk"
)

// Request is an API request a script makes. URL is the path and query.
type Request struct {
	Method string
	URL    string
	Header map[string]string
	Body   []byte
}

// Response answers a Request.
type Response struct {
	Status int
	Header map[string]string
	Body   []byte
}

// Run identifies the run a slice executes and what it runs.
type Run struct {
	ID       string
	Artifact Artifact
	Input    json.RawMessage
}

// Effects performs a run's side effects durably. Steps passed as unsaved are
// value steps the script observed since the last durable step; an effect
// records them with its own step, in the same transaction, so a replay
// always sees the values earlier effects depended on.
type Effects interface {
	// Steps returns the run's recorded steps in order.
	Steps(ctx context.Context, runID string) ([]runs.Step, error)
	// Request performs req as the run's author and records step, with
	// ResponseOutput(response) as its output, in the transaction of the
	// request's writes. A failed request records its response but none of
	// its writes.
	Request(ctx context.Context, unsaved []runs.Step, step runs.Step, req Request) (Response, error)
	// Start records the pending action step and schedules the action. The
	// run resumes when the action's step is finished.
	Start(ctx context.Context, unsaved []runs.Step, step runs.Step) error
}

// OutcomeStatus is how an execution slice ended.
type OutcomeStatus string

const (
	// Completed: the workflow returned Output.
	Completed OutcomeStatus = "completed"
	// Suspended: the run waits for a pending action and holds no runtime.
	Suspended OutcomeStatus = "suspended"
	// Failed: the script threw, broke a limit or replayed differently.
	Failed OutcomeStatus = "failed"
)

// Outcome is the result of one execution slice.
type Outcome struct {
	Status OutcomeStatus
	Output json.RawMessage
	Error  string
	// Logs are the entries this slice wrote after replaying earlier ones.
	Logs []Log
}

// Log is one console or ctx.log entry.
type Log struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ErrNondeterministic reports a replay that asked for a different step than
// the one recorded at its position.
var ErrNondeterministic = errors.New("the workflow replayed differently")

var (
	errSuspended = errors.New("workflow suspended")
	errSlice     = errors.New("the workflow ran longer than its time slice without waiting")
)

const (
	defaultSlice = 5 * time.Second
	// Output and request limits keep a run's journal small.
	maxOutput       = 256 << 10
	maxActionInput  = 64 << 10
	maxResponseBody = 1 << 20
	maxCallStack    = 4096
)

// Engine executes script runs. It is safe for concurrent use; each slice
// runs in its own runtime.
type Engine struct {
	effects Effects
	slice   time.Duration
	cache   *programCache
}

// New returns an engine whose slices may run JavaScript for slice before
// waiting; zero means five seconds.
func New(effects Effects, slice time.Duration) *Engine {
	if slice <= 0 {
		slice = defaultSlice
	}
	return &Engine{effects: effects, slice: slice, cache: newProgramCache(64)}
}

var sdkProgram = sync.OnceValues(func() (*goja.Program, error) {
	return goja.Compile("papergo-sdk.js", sdk.Bundle, true)
})

// Execute replays run from the start and continues it until it completes,
// fails or suspends. An error means the slice could not run, for example
// because the database failed; the run's recorded state is unchanged and it
// can execute again.
func (e *Engine) Execute(ctx context.Context, run Run) (Outcome, error) {
	sdkCode, err := sdkProgram()
	if err != nil {
		return Outcome{}, fmt.Errorf("compile the workflow SDK: %w", err)
	}
	program, err := e.cache.get(run.Artifact)
	if err != nil {
		return Outcome{Status: Failed, Error: err.Error()}, nil
	}
	recorded, err := e.effects.Steps(ctx, run.ID)
	if err != nil {
		return Outcome{}, err
	}
	input := run.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	h := &host{ctx: ctx, run: run, effects: e.effects, recorded: recorded}
	rt := h.runtime()
	h.watch = newWatchdog(e.slice, func() { rt.Interrupt(errSlice) })
	defer h.watch.stop()
	stopCancel := context.AfterFunc(ctx, func() { rt.Interrupt(ctx.Err()) })
	defer stopCancel()

	value, err := func() (goja.Value, error) {
		if _, err := rt.RunProgram(sdkCode); err != nil {
			return nil, err
		}
		if _, err := rt.RunProgram(program); err != nil {
			return nil, err
		}
		main, ok := goja.AssertFunction(rt.Get("__papergoMain"))
		if !ok {
			return nil, errors.New("the script has no workflow entry point")
		}
		return main(goja.Undefined(), rt.ToValue(run.ID), rt.ToValue(string(input)))
	}()
	return h.outcome(value, err)
}

func (h *host) outcome(value goja.Value, err error) (Outcome, error) {
	logs := h.logs
	switch {
	case h.abort != nil && errors.Is(h.abort, ErrNondeterministic):
		return Outcome{Status: Failed, Error: h.abort.Error(), Logs: logs}, nil
	case h.abort != nil:
		return Outcome{}, h.abort
	case h.suspended:
		return Outcome{Status: Suspended, Logs: logs}, nil
	}
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			if cause := interrupted.Unwrap(); cause != nil && !errors.Is(cause, errSlice) {
				return Outcome{}, cause
			}
		}
		return Outcome{Status: Failed, Error: failure(err), Logs: logs}, nil
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return Outcome{Status: Failed, Error: "the workflow entry point did not return a promise", Logs: logs}, nil
	}
	switch promise.State() {
	case goja.PromiseStateFulfilled:
		output := json.RawMessage(promise.Result().String())
		if len(output) > maxOutput {
			return Outcome{Status: Failed, Error: fmt.Sprintf("the workflow output is larger than %d bytes", maxOutput), Logs: logs}, nil
		}
		return Outcome{Status: Completed, Output: output, Logs: logs}, nil
	case goja.PromiseStateRejected:
		return Outcome{Status: Failed, Error: thrown(promise.Result()), Logs: logs}, nil
	default:
		return Outcome{Status: Failed, Error: "the workflow awaited a promise that never settles", Logs: logs}, nil
	}
}

// failure describes an error a slice ended with, preferring the script's
// own stack trace.
func failure(err error) string {
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return thrown(exception.Value())
	}
	return err.Error()
}

func thrown(v goja.Value) string {
	if obj, ok := v.(*goja.Object); ok {
		if stack := obj.Get("stack"); stack != nil && !goja.IsUndefined(stack) && stack.String() != "" {
			return stack.String()
		}
	}
	if v == nil {
		return "the workflow failed"
	}
	return v.String()
}

// watchdog interrupts a runtime that has run JavaScript for longer than its
// budget. Time spent in host effects does not count.
type watchdog struct {
	mu        sync.Mutex
	remaining time.Duration
	started   time.Time
	timer     *time.Timer
	fire      func()
}

func newWatchdog(budget time.Duration, fire func()) *watchdog {
	w := &watchdog{remaining: budget, fire: fire}
	w.resume()
	return w
}

func (w *watchdog) pause() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil && w.timer.Stop() {
		w.remaining -= time.Since(w.started)
	}
	w.timer = nil
}

func (w *watchdog) resume() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.remaining <= 0 {
		w.remaining = time.Nanosecond
	}
	w.started = time.Now()
	w.timer = time.AfterFunc(w.remaining, w.fire)
}

func (w *watchdog) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = nil
}
