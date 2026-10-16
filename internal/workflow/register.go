package workflow

import (
	"context"
	"encoding/json"
	"time"

	"papergo/internal/dms"
)

// ActivitySpec registers a compiled activity. Prepare and Commit run inside
// the step transaction as the workflow version's author. Work runs outside
// any transaction, may run again after a crash, and must not write domain
// data. The execution id is stable for one visit of the node, including the
// loop iteration, and stable again when that step is replayed.
type ActivitySpec struct {
	Key          string
	Description  string
	Kind         string
	Ports        []string
	InputSchema  map[string]any
	OutputSchema map[string]any
	PortsFrom    string
	Terminal     bool
	Prepare      func(Step, map[string]any) (Prepared, error)
	Work         func(context.Context, Tools, json.RawMessage) (json.RawMessage, error)
	Commit       func(Step, json.RawMessage, json.RawMessage) (Prepared, error)
}

// Prepared is the outcome of a prepare or commit handler.
type Prepared struct {
	Port   string
	Output any
	Vars   map[string]any
	Sleep  time.Duration
	End    bool
	Fail   string
	Job    json.RawMessage
	Park   *Park
}

func (p Prepared) outcome() outcome {
	return outcome{Port: p.Port, Output: p.Output, Vars: p.Vars, Sleep: p.Sleep.Seconds(), End: p.End, Fail: p.Fail, Job: p.Job, Park: p.Park}
}

// Step is the transactional view of the node: the DMS service, the author,
// and token resolution.
type Step struct{ e *env }

// DMS is the service bound to the node's transaction.
func (s Step) DMS() *dms.Service { return s.e.t }

// Context is the node's transaction context.
func (s Step) Context() context.Context { return s.e.ctx }

// Author is the workflow version's author.
func (s Step) Author() string { return s.e.author }

// ItemID is the run's item, empty for a run with none.
func (s Step) ItemID() string { return s.e.itemID }

// Items is a selection run's ordered membership.
func (s Step) Items() []string { return s.e.items }

// RunID is the durable run id.
func (s Step) RunID() string { return s.e.runID }

// WorkspaceID is the workflow's workspace.
func (s Step) WorkspaceID() string { return s.e.workspaceID }

// WorkflowID is the workflow definition's id.
func (s Step) WorkflowID() string { return s.e.workflowID }

// Node is the node name.
func (s Step) Node() string { return s.e.node }

// ExecutionID identifies this visit of the node, including loop iterations.
func (s Step) ExecutionID() string { return s.e.executionID }

// Tools are the deployment's file, OCR and model tools.
func (s Step) Tools() Tools { return s.e.tools }

// Resolve evaluates tokens in v.
func (s Step) Resolve(v any) (any, error) { return s.e.scope.resolve(v) }

// Text resolves v to text.
func (s Step) Text(v any) (string, error) { return s.e.scope.text(v) }

// Target resolves the item the action works on.
func (s Step) Target(inputs map[string]any) (string, error) { return s.e.target(inputs) }

// OptionalText reads an optional text input.
func (s Step) OptionalText(inputs map[string]any, name string) (string, error) {
	return s.e.optionalText(inputs, name)
}

// Whole reads an optional whole-number input within bounds.
func (s Step) Whole(inputs map[string]any, name string, v *int, low, high int) error {
	return s.e.whole(inputs, name, v, low, high)
}

// Choice reads an optional text input that must be one of values.
func (s Step) Choice(inputs map[string]any, name string, v *string, values []string) error {
	return s.e.choice(inputs, name, v, values)
}

// Object resolves v to an object.
func (s Step) Object(v any) (map[string]any, error) { return s.e.object(v) }

// Strings resolves v to a list of text.
func (s Step) Strings(name string, v any) ([]string, error) { return s.e.strings(name, v) }

// RegisterActivity adds a compiled activity to the catalog.
func RegisterActivity(spec ActivitySpec) {
	if spec.Key == "" || spec.Prepare == nil {
		panic("activity " + spec.Key + " needs a key and a prepare handler")
	}
	if spec.Work != nil && spec.Commit == nil {
		panic("activity " + spec.Key + " has work without a commit handler")
	}
	a := &Activity{
		Key: spec.Key, Description: spec.Description, Kind: spec.Kind, Ports: spec.Ports,
		InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema, PortsFrom: spec.PortsFrom, Terminal: spec.Terminal,
		run: func(e *env, in map[string]any) (outcome, error) {
			prepared, err := spec.Prepare(Step{e: e}, in)
			if err != nil {
				return outcome{}, err
			}
			return prepared.outcome(), nil
		},
	}
	if spec.Work != nil {
		a.work = func(w *workEnv, job json.RawMessage) (json.RawMessage, error) {
			return spec.Work(w.ctx, w.tools, job)
		}
		a.commit = func(e *env, job, result json.RawMessage) (outcome, error) {
			prepared, err := spec.Commit(Step{e: e}, job, result)
			if err != nil {
				return outcome{}, err
			}
			return prepared.outcome(), nil
		}
	}
	register(a)
}
