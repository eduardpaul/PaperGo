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
	RunID       string
	Seq         int
	Kind        StepKind
	Name        string
	InputHash   string
	Status      StepStatus
	Input       json.RawMessage
	Output      json.RawMessage
	Error       string
	StartedAt   time.Time
	CompletedAt *time.Time
	// Operations are the operations a wait step waits for. Finishing the
	// last of them completes the step in the same transaction.
	Operations []string
}

// StepQueries are the statements a driver runs for script steps.
type StepQueries interface {
	// RunSteps returns a run's steps ordered by Seq.
	RunSteps(ctx context.Context, runID string) ([]Step, error)
	InsertRunStep(ctx context.Context, s Step) error
	InsertStepOperations(ctx context.Context, runID string, seq int, operationIDs []string) error
	// FinishRunStep completes or fails a pending step and reports whether it
	// was pending.
	FinishRunStep(ctx context.Context, runID string, seq int, status StepStatus, output json.RawMessage, message string, at time.Time) (bool, error)
}

// Steps returns a run's recorded steps in order.
func (r *Repo) Steps(ctx context.Context, runID string) ([]Step, error) {
	return r.read.RunSteps(ctx, runID)
}

// RecordSteps appends steps to their runs. Only a wait can be pending, and
// only a wait names operations; any other step is recorded complete.
func (r *Repo) RecordSteps(ctx context.Context, steps []Step) error {
	at := now()
	for _, s := range steps {
		if !s.Kind.Valid() || !s.Status.Valid() || s.Seq < 0 || s.Name == "" || s.InputHash == "" {
			return fmt.Errorf("invalid step %d of %s", s.Seq, s.RunID)
		}
		if (s.Status == StepPending || len(s.Operations) > 0) && s.Kind != StepWait {
			return fmt.Errorf("step %d of %s: only waits can be pending or name operations", s.Seq, s.RunID)
		}
	}
	return r.write(ctx, func(q Queries) error {
		for _, s := range steps {
			s.StartedAt = at
			if s.Status != StepPending {
				s.CompletedAt = &at
			}
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
		done, err = q.FinishRunStep(ctx, runID, seq, status, output, failure, now())
		return err
	})
	return done, err
}
