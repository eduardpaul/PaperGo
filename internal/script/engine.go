package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"papergo/internal/dal/operations"
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

// Run identifies the run a slice executes, who it acts as and what it runs.
// Context is the run as the script's ctx.run sees it, with its input; the
// engine adds the ID.
type Run struct {
	ID       string
	Author   string
	Claim    operations.Claim
	Artifact Artifact
	Context  map[string]any
}

// Effects performs a run's side effects durably. Steps passed as unsaved are
// value steps the script observed since the last durable step; an effect
// records them with its own step, in the same transaction, so a replay
// always sees the values earlier effects depended on.
type Effects interface {
	// Steps returns the run's recorded steps in order.
	Steps(ctx context.Context, run Run) ([]runs.Step, error)
	// Request performs req as the run's author and records step, with
	// ResponseOutput(response) as its output, in the transaction of the
	// request's writes. A failed request records its response but none of
	// its writes.
	Request(ctx context.Context, run Run, unsaved []runs.Step, step runs.Step, req Request) (Response, error)
	// Read performs a request that changes nothing, a GET or HEAD, as the
	// run's author, outside any transaction of the run. Its step is recorded
	// later, with the next durable step: a crash before then only repeats
	// the read.
	Read(ctx context.Context, run Run, req Request) (Response, error)
	// Record records value and read steps the script observed since the
	// last durable step, so a long run of reads keeps its journal.
	Record(ctx context.Context, run Run, unsaved []runs.Step) error
	// Wait records the wait step for step.Operations, which the author must
	// be able to read: completed when none of them is pending, else pending
	// until the last one finishes. It reports whether the wait is over.
	// An operation the author cannot read is ErrUnknownOperation.
	Wait(ctx context.Context, run Run, unsaved []runs.Step, step runs.Step) (bool, error)
	// Sleep records a sleep step, pending unless step is completed.
	Sleep(ctx context.Context, run Run, unsaved []runs.Step, step runs.Step) error
	// Wake completes a pending sleep step whose time came.
	Wake(ctx context.Context, run Run, seq int) error
}

// ErrUnknownOperation reports a wait for an operation that does not exist or
// that the run's author cannot read.
var ErrUnknownOperation = errors.New("no such operation")

// OutcomeStatus is how an execution slice ended.
type OutcomeStatus string

const (
	// Completed: the workflow returned Output.
	Completed OutcomeStatus = "completed"
	// Suspended: the run waits for operations, or until Until, and holds
	// no runtime.
	Suspended OutcomeStatus = "suspended"
	// Failed: the script threw, broke a limit or replayed differently.
	Failed OutcomeStatus = "failed"
)

// Outcome is the result of one execution slice.
type Outcome struct {
	Status OutcomeStatus
	Output json.RawMessage
	Error  string
	// Until is when a run that sleeps continues.
	Until *time.Time
	// Logs are the entries this slice wrote after replaying earlier ones.
	Logs []Log
	// Unsaved are the value and read steps a completed or failed run
	// observed after its last durable step, for the caller to record with
	// the outcome.
	Unsaved []runs.Step
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
	maxOutput    = 256 << 10
	maxWaits     = 64
	maxCallStack = 4096
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
	recorded, err := e.effects.Steps(ctx, run)
	if err != nil {
		return Outcome{}, err
	}
	info := map[string]any{}
	for k, v := range run.Context {
		info[k] = v
	}
	info["id"] = run.ID
	raw, err := json.Marshal(info)
	if err != nil {
		return Outcome{}, err
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
		value, err := main(goja.Undefined(), rt.ToValue(string(raw)))
		if err != nil {
			return nil, err
		}
		return value, h.settle(value)
	}()
	out, err := h.outcome(value, err)
	if out.Status == Completed || out.Status == Failed {
		out.Unsaved = h.unsaved
	}
	return out, err
}

// settle runs the SDK's __papergoIdle each time the script has run out of
// promise jobs while its run is pending. The SDK then starts, as one wait
// step, every wait the script began since, so operations waited for
// together, such as tasks in a Promise.all, suspend the run once. It ends
// when the run settles or suspends, or when nothing was waiting.
func (h *host) settle(value goja.Value) error {
	idle, ok := goja.AssertFunction(h.rt.Get("__papergoIdle"))
	promise, pending := value.Export().(*goja.Promise)
	for ok && pending && promise.State() == goja.PromiseStatePending && !h.suspended && h.abort == nil {
		waited, err := idle(goja.Undefined())
		if err != nil {
			return err
		}
		if !waited.ToBoolean() {
			return nil
		}
	}
	return nil
}

// manifestSlice bounds the JavaScript a manifest evaluation runs.
const manifestSlice = time.Second

// Manifest evaluates a script's export with params and returns what its
// workflow() declares: triggers, condition, input_schema, output_schema and
// concurrency, as JSON. The run function is not called, and the script may
// make no requests, waits or sleeps.
func (e *Engine) Manifest(ctx context.Context, artifact Artifact, params map[string]any) (json.RawMessage, error) {
	sdkCode, err := sdkProgram()
	if err != nil {
		return nil, fmt.Errorf("compile the workflow SDK: %w", err)
	}
	program, err := e.cache.get(artifact)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	h := &host{ctx: ctx, manifest: true}
	rt := h.runtime()
	h.watch = newWatchdog(manifestSlice, func() { rt.Interrupt(errSlice) })
	defer h.watch.stop()
	if _, err = rt.RunProgram(sdkCode); err != nil {
		return nil, err
	}
	if _, err = rt.RunProgram(program); err != nil {
		return nil, errors.New(failure(err))
	}
	manifest, ok := goja.AssertFunction(rt.Get("__papergoManifest"))
	if !ok {
		return nil, errors.New("the script has no workflow entry point")
	}
	value, err := manifest(goja.Undefined(), rt.ToValue(string(raw)))
	if err != nil {
		return nil, errors.New(failure(err))
	}
	return json.RawMessage(value.String()), nil
}

func (h *host) outcome(value goja.Value, err error) (Outcome, error) {
	logs := h.logs
	switch {
	case h.abort != nil && errors.Is(h.abort, ErrNondeterministic):
		return Outcome{Status: Failed, Error: h.abort.Error(), Logs: logs}, nil
	case h.abort != nil:
		return Outcome{}, h.abort
	case h.suspended:
		return Outcome{Status: Suspended, Until: h.until, Logs: logs}, nil
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
