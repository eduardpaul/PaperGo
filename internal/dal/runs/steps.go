package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// StepKind is what a recorded script step did.
type StepKind string

const (
	// StepRequest is an API request the run made as its author.
	StepRequest StepKind = "request"
	// StepWait waits until operations the run started have finished.
	StepWait StepKind = "wait"
	// StepValue is a clock reading or random value the script observed.
	StepValue StepKind = "value"
)

func (k StepKind) Valid() bool { return k == StepRequest || k == StepWait || k == StepValue }

// StepStatus is where a recorded step is: only waits are ever pending.
type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
)

func (s StepStatus) Valid() bool { return s == StepPending || s == StepCompleted || s == StepFailed }

// Step is one recorded step of a script run, numbered from zero in the
// order the script made it. A replay returns its output instead of
// repeating it. InputHash identifies what was asked, so a replay that asks
// for something else at the same position is detected.
type Step struct {
	RunID     string
	Seq       int
	Kind      StepKind
	Name      string
	InputHash string
	Status    StepStatus
	Input     json.RawMessage
	// Output is nil when a finished run dropped it; OutputSize is its size
	// in bytes either way.
	Output      json.RawMessage
	OutputSize  int
	Error       string
	StartedAt   time.Time
	CompletedAt *time.Time
	// Operations are the operations a wait step waits for, released once
	// none of them is pending, or those a request step started.
	Operations []string
}

// StepQueries are the statements a driver runs for script steps. Outputs
// cross them as stored, compressed by pack.
type StepQueries interface {
	// RunSteps returns a run's steps ordered by Seq.
	RunSteps(ctx context.Context, runID string) ([]Step, error)
	InsertRunStep(ctx context.Context, s Step) error
	InsertStepOperations(ctx context.Context, runID string, seq int, operationIDs []string) error
	// FinishRunStep completes or fails a pending step and reports whether it
	// was pending.
	FinishRunStep(ctx context.Context, runID string, seq int, status StepStatus, output []byte, size int, message string, at time.Time) (bool, error)
	// ReleasableWaits returns the pending waits for any of operationIDs, or
	// every pending wait when there are none, whose operations have all
	// ended, with those operations.
	ReleasableWaits(ctx context.Context, operationIDs []string) ([]Wait, error)
	// RunWaiting reports whether a run has a pending step.
	RunWaiting(ctx context.Context, runID string) (bool, error)
	// DropRunOutputs drops the outputs of a run's steps that are stored
	// larger than limit bytes.
	DropRunOutputs(ctx context.Context, runID string, limit int) error
	// CopyRunSteps copies a run's steps before a position, with the
	// operations they name, to another run.
	CopyRunSteps(ctx context.Context, target, source string, before int) error
}

// Wait is a pending wait step and the operations it waits for.
type Wait struct {
	RunID      string
	Seq        int
	Operations []string
}

// Steps returns a run's recorded steps in order.
func (r *Repo) Steps(ctx context.Context, runID string) ([]Step, error) {
	steps, err := r.read.RunSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	for i := range steps {
		if steps[i].Output, err = unpack(steps[i].Output); err != nil {
			return nil, fmt.Errorf("step %d of %s: %w", steps[i].Seq, runID, err)
		}
	}
	return steps, nil
}

// RecordSteps appends steps to their runs. Only a wait can be pending, and
// only waits and requests name operations; any other step is recorded
// complete.
func (r *Repo) RecordSteps(ctx context.Context, steps []Step) error {
	at := now()
	for _, s := range steps {
		if !s.Kind.Valid() || !s.Status.Valid() || s.Seq < 0 || s.Name == "" || s.InputHash == "" {
			return fmt.Errorf("invalid step %d of %s", s.Seq, s.RunID)
		}
		if s.Status == StepPending && s.Kind != StepWait || len(s.Operations) > 0 && s.Kind == StepValue {
			return fmt.Errorf("step %d of %s: only waits can be pending, and values name no operations", s.Seq, s.RunID)
		}
	}
	return r.write(ctx, func(q Queries) error {
		for _, s := range steps {
			// A step the engine kept until a durable step keeps its time.
			if s.StartedAt.IsZero() {
				s.StartedAt = at
			}
			if s.Status != StepPending && s.CompletedAt == nil {
				s.CompletedAt = &at
			}
			s.OutputSize, s.Output = len(s.Output), pack(s.Output)
			if err := q.InsertRunStep(ctx, s); err != nil {
				return err
			}
			if len(s.Operations) > 0 {
				if err := q.InsertStepOperations(ctx, s.RunID, s.Seq, s.Operations); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// FinishStep records the outcome of a pending wait, such as its timeout. It
// reports false when the step was not pending, so a repeated completion
// changes nothing.
func (r *Repo) FinishStep(ctx context.Context, runID string, seq int, output json.RawMessage, failure string) (bool, error) {
	status := StepCompleted
	if failure != "" {
		status, output = StepFailed, nil
	}
	var done bool
	err := r.write(ctx, func(q Queries) error {
		var err error
		done, err = q.FinishRunStep(ctx, runID, seq, status, pack(output), len(output), failure, now())
		return err
	})
	return done, err
}

// ReleasableWaits returns the pending waits for any of operationIDs whose
// operations have all ended, which FinishStep completes with what they
// ended as. Without IDs it returns every such wait, for waits whose last
// operation ended while nobody released them, such as before a crash.
func (r *Repo) ReleasableWaits(ctx context.Context, operationIDs []string) ([]Wait, error) {
	return r.read.ReleasableWaits(ctx, operationIDs)
}

// Waiting reports whether a run has a pending wait.
func (r *Repo) Waiting(ctx context.Context, runID string) (bool, error) {
	return r.read.RunWaiting(ctx, runID)
}

// KeptOutput is the stored size up to which a finished run keeps a step's
// output, so its details show what small responses said.
const KeptOutput = 4 << 10

// DropOutputs drops the outputs a run that completed or was cancelled no
// longer replays, except those stored in at most KeptOutput bytes. Their
// sizes stay. A failed run keeps them for its retry.
func (r *Repo) DropOutputs(ctx context.Context, runID string) error {
	return r.write(ctx, func(q Queries) error { return q.DropRunOutputs(ctx, runID, KeptOutput) })
}

// ForkSteps gives a run the steps another recorded before a position, so
// its script replays them instead of repeating them.
func (r *Repo) ForkSteps(ctx context.Context, target, source string, before int) error {
	return r.write(ctx, func(q Queries) error { return q.CopyRunSteps(ctx, target, source, before) })
}
