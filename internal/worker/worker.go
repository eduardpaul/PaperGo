// Package worker runs PaperGo's operations. Named workers own operation
// kinds, each with its own capacity, admission policy and execution window,
// so long work never holds up short work; maintenance tasks run on their
// schedules beside them. A worker only re-evaluates when it wakes: on new
// work, at the next time something becomes due, or on a control.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"papergo/internal/dms"
)

// Policy decides when queued work of a worker's kinds runs.
type Policy string

const (
	// Immediate runs work as soon as it is ready.
	Immediate Policy = "immediate"
	// Threshold runs a group once it holds Threshold distinct inputs.
	Threshold Policy = "threshold"
	// MaxWait runs a group once its oldest input waited MaxWait.
	MaxWait Policy = "max_wait"
	// ThresholdOrMaxWait runs a group on whichever comes first.
	ThresholdOrMaxWait Policy = "threshold_or_max_wait"
)

func (p Policy) Valid() bool {
	switch p {
	case Immediate, Threshold, MaxWait, ThresholdOrMaxWait:
		return true
	}
	return false
}

// Admission is when work becomes eligible. Group policies run operations
// in groups that their kinds' handlers execute together; Batch bounds the
// distinct inputs of one execution, and a group larger than that drains in
// several. Flush is an optional schedule that makes every group eligible.
type Admission struct {
	Policy    Policy
	Threshold int
	MaxWait   time.Duration
	Batch     int
	Flush     *Schedule
}

// Definition is a named worker.
type Definition struct {
	Name  string
	Kinds []string
	// Concurrency is how many executions run at once.
	Concurrency int
	Admission   Admission
	// Window, when set, admits new executions only inside it.
	Window *Window
}

// Maintenance is a task that runs every Every, such as retention.
type Maintenance struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context, now time.Time) error
}

// Runtime runs the workers and maintenance tasks of one process.
type Runtime struct {
	svc   *dms.Service
	log   *slog.Logger
	defs  []*Definition
	tasks []Maintenance
	// DrainTimeout is how long executions may finish after Run's context
	// ends before they are cancelled and give their operations back.
	DrainTimeout time.Duration
	// ControlPoll is how often workers read their controls.
	ControlPoll time.Duration
	now         func() time.Time

	mu      sync.Mutex
	wakeups chan struct{}
	running map[string]int
}

// New returns a runtime for the operations svc runs. It wakes its workers
// when svc accepts operations.
func New(svc *dms.Service, log *slog.Logger) *Runtime {
	r := &Runtime{svc: svc, log: log, DrainTimeout: 10 * time.Second, ControlPoll: 15 * time.Second, now: time.Now, wakeups: make(chan struct{}), running: map[string]int{}}
	svc.OnOperationsAccepted(r.Wake)
	return r
}

// Add registers a worker. Validate checks the set before Run.
func (r *Runtime) Add(def Definition) {
	d := def
	if d.Concurrency == 0 {
		d.Concurrency = 1
	}
	if d.Admission.Policy == "" {
		d.Admission.Policy = Immediate
	}
	r.defs = append(r.defs, &d)
}

// Worker returns the definition named name, for configuration to adjust
// before Run, or nil.
func (r *Runtime) Worker(name string) *Definition {
	for _, d := range r.defs {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// Maintain registers a maintenance task.
func (r *Runtime) Maintain(m Maintenance) { r.tasks = append(r.tasks, m) }

// Validate checks that every operation kind the service runs has exactly
// one worker, that no worker names an unknown kind, and that each
// definition is complete.
func (r *Runtime) Validate() error {
	kinds := r.svc.OperationKinds()
	owner := map[string]string{}
	names := map[string]bool{}
	for _, d := range r.defs {
		if d.Name == "" || names[d.Name] {
			return fmt.Errorf("workers need distinct names: %q", d.Name)
		}
		names[d.Name] = true
		if d.Concurrency < 1 {
			return fmt.Errorf("worker %s: concurrency must be at least 1", d.Name)
		}
		if err := d.Admission.validate(); err != nil {
			return fmt.Errorf("worker %s: %w", d.Name, err)
		}
		if d.Window != nil {
			if err := d.Window.validate(); err != nil {
				return fmt.Errorf("worker %s: %w", d.Name, err)
			}
		}
		if len(d.Kinds) == 0 {
			return fmt.Errorf("worker %s runs no operation kind", d.Name)
		}
		for _, kind := range d.Kinds {
			if !slices.Contains(kinds, kind) {
				return fmt.Errorf("worker %s: no handler runs operation kind %s", d.Name, kind)
			}
			if grouped := r.svc.OperationRunsInGroups(kind); grouped != (d.Admission.Policy != Immediate) {
				if grouped {
					return fmt.Errorf("worker %s: operation kind %s runs in groups and needs a group admission policy", d.Name, kind)
				}
				return fmt.Errorf("worker %s: operation kind %s does not run in groups and needs immediate admission", d.Name, kind)
			}
			if other, taken := owner[kind]; taken {
				return fmt.Errorf("operation kind %s belongs to workers %s and %s", kind, other, d.Name)
			}
			owner[kind] = d.Name
		}
	}
	for _, kind := range kinds {
		if _, ok := owner[kind]; !ok {
			return fmt.Errorf("no worker runs operation kind %s", kind)
		}
	}
	for _, m := range r.tasks {
		if m.Name == "" || m.Every <= 0 || m.Run == nil {
			return fmt.Errorf("maintenance %q needs a name, a period and a task", m.Name)
		}
	}
	return nil
}

func (a Admission) validate() error {
	if !a.Policy.Valid() {
		return fmt.Errorf("unknown admission policy %q", a.Policy)
	}
	group := a.Policy != Immediate
	if a.Policy == Threshold || a.Policy == ThresholdOrMaxWait {
		if a.Threshold < 1 {
			return errors.New("a threshold policy needs a threshold of at least 1")
		}
	}
	if a.Policy == MaxWait || a.Policy == ThresholdOrMaxWait {
		if a.MaxWait < time.Minute || a.MaxWait > 720*time.Hour {
			return errors.New("max_wait must be from 1m to 720h")
		}
	}
	if group && a.Batch < 1 {
		return errors.New("a group policy needs a batch of at least 1 input")
	}
	if !group && (a.Flush != nil || a.Threshold != 0 || a.MaxWait != 0) {
		return errors.New("immediate admission takes no threshold, max_wait or flush")
	}
	return nil
}

// Wake makes every worker re-evaluate now.
func (r *Runtime) Wake() {
	r.mu.Lock()
	close(r.wakeups)
	r.wakeups = make(chan struct{})
	r.mu.Unlock()
}

// woken returns the channel the next Wake closes.
func (r *Runtime) woken() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wakeups
}

// Run runs the workers and maintenance tasks until ctx ends. Then workers
// stop claiming, executions get DrainTimeout to finish, and the rest are
// cancelled and give their operations back. It returns once everything
// stopped.
func (r *Runtime) Run(ctx context.Context) error {
	if err := r.Validate(); err != nil {
		return err
	}
	// Executions run on work, which outlives ctx by the drain timeout.
	work, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	go func() {
		<-ctx.Done()
		timer := time.NewTimer(r.DrainTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel()
		case <-work.Done():
		}
	}()
	var wg sync.WaitGroup
	for _, d := range r.defs {
		w := &worker{rt: r, def: d}
		if d.Admission.Policy == Immediate {
			for range d.Concurrency {
				wg.Go(func() { w.immediate(ctx, work) })
			}
		} else {
			wg.Go(func() { w.groups(ctx, work) })
		}
		wg.Go(func() { w.control(ctx) })
	}
	for _, m := range r.tasks {
		wg.Go(func() { r.maintain(ctx, m) })
	}
	wg.Wait()
	return nil
}

// maintain runs m at once and then every m.Every until ctx ends.
func (r *Runtime) maintain(ctx context.Context, m Maintenance) {
	for {
		if err := m.Run(ctx, r.now()); err != nil && ctx.Err() == nil {
			r.log.Error("maintenance failed", "task", m.Name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.Every):
		}
	}
}

// sleep waits until d passed, wake closed or ctx ended. Callers take wake
// from woken before they look for work, so a wake in between is not lost.
func sleep(ctx context.Context, wake <-chan struct{}, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-timer.C:
	}
}

// Drain runs every operation that is ready now, of every kind, one after
// another, and returns when none is left: for tools and tests that need
// queued work done without running workers.
func Drain(ctx context.Context, svc *dms.Service) error {
	for {
		worked, err := svc.RunOperation(ctx, svc.OperationKinds())
		if err != nil || !worked {
			return err
		}
	}
}

// Single returns a runtime whose one worker runs every operation kind svc
// knows, one at a time, without maintenance: for tools and tests.
func Single(svc *dms.Service, log *slog.Logger) *Runtime {
	rt := New(svc, log)
	rt.Add(Definition{Name: "all", Kinds: svc.OperationKinds()})
	return rt
}
