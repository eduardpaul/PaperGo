package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"papergo/ent/workflowversion"
	"papergo/internal/dms"
)

// RunInput starts one run of a workflow version. The runner stores it with
// the run, so a recovered run executes the same version with the same data.
type RunInput struct {
	RunID       string         `json:"run_id"`
	WorkflowID  string         `json:"workflow_id"`
	Version     int            `json:"version"`
	WorkspaceID string         `json:"workspace_id"`
	ItemID      string         `json:"item_id,omitempty"`
	Event       dms.Event      `json:"event"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	// Items is a selection run's ordered membership; ItemID is its primary.
	Items []string `json:"items,omitempty"`
	// ConditionError fails the run at once: the condition could not be
	// evaluated when the event was dispatched.
	ConditionError string `json:"condition_error,omitempty"`
}

// Exec makes a run durable. Step runs fn once: the writes it makes through t
// and the events it passes to raise commit together, exactly once, and a
// replay after a crash returns the recorded result without running fn.
// Work runs fn outside any transaction, for CPU-heavy work that must not
// hold the database writer, and records its result: fn may run again after
// a crash, so it must be safe to repeat and must not write domain data.
// Sleep waits durably.
type Exec interface {
	Step(name string, fn func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error)) (json.RawMessage, error)
	Work(name string, fn func(ctx context.Context) (json.RawMessage, error)) (json.RawMessage, error)
	// Await waits for approval request id, which node created, to be
	// decided, and returns the node's output: Decided's value.
	Await(node, id string) (json.RawMessage, error)
	Sleep(d time.Duration) error
}

// Result is a finished run's output.
type Result struct {
	Status    string         `json:"status"`
	Node      string         `json:"node,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
}

// ErrRunFailed wraps the reason a run failed.
var ErrRunFailed = errors.New("workflow run failed")

type loaded struct {
	Key        string     `json:"key"`
	Author     string     `json:"author"`
	Definition Definition `json:"definition"`
}

// Execute interprets the run's workflow version: it starts at the flow's
// start node and follows the port each node's outcome selects. Every node is
// one durable step, or three for activities with heavy work, so after a
// crash the run continues at the node it reached. Workflow code here only
// routes between recorded step results.
func (s *Service) Execute(ctx context.Context, x Exec, in RunInput) (Result, error) {
	raw, err := x.Step("load", func(ctx context.Context, t *dms.Service, _ func(dms.Event)) (json.RawMessage, error) {
		w, err := t.Client.Workflow.Get(ctx, in.WorkflowID)
		if err != nil {
			return nil, err
		}
		v, err := t.Client.WorkflowVersion.Query().Where(workflowversion.WorkflowIDEQ(in.WorkflowID), workflowversion.NumberEQ(in.Version)).Only(ctx)
		if err != nil {
			return nil, err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return nil, err
		}
		if in.ConditionError != "" {
			return nil, fmt.Errorf("condition could not be evaluated: %s", in.ConditionError)
		}
		return json.Marshal(loaded{Key: w.Key, Author: v.CreatedBy, Definition: def})
	})
	if err != nil {
		return Result{Status: "failed"}, err
	}
	var l loaded
	if err = decode(raw, &l); err != nil {
		return Result{Status: "failed"}, err
	}
	r := &runState{in: in, x: x, l: l, tools: s.Tools, vars: map[string]any{}, steps: map[string]any{}}
	for k, v := range l.Definition.Variables {
		r.vars[k] = jsonish(v)
	}
	return r.execute(ctx)
}

type runState struct {
	in    RunInput
	x     Exec
	l     loaded
	tools Tools
	vars  map[string]any
	steps map[string]any
	// loops are the forEach nodes the run is inside, innermost last.
	loops []loopFrame
}

// loopFrame is a forEach node's progress: its items, recorded by the node's
// step, and the element the current pass runs with.
type loopFrame struct {
	node  string
	items []any
	index int
	as    string
}

func (r *runState) loopVars(f loopFrame) {
	r.vars[f.as] = f.items[f.index]
	r.vars[f.as+"_index"] = f.index
}

func (r *runState) execute(ctx context.Context) (Result, error) {
	flow := r.l.Definition.Flow
	name := flow.Start
	for visits := 1; ; visits++ {
		if visits > MaxNodeVisits {
			return r.finish(name, fmt.Errorf("the run visited more than %d nodes", MaxNodeVisits))
		}
		node := flow.Nodes[name]
		if node.Activity == forEachKey {
			// Reaching a loop through a port starts it again, also when an
			// outer loop's next pass reaches an inner one.
			for i, f := range r.loops {
				if f.node == name {
					r.loops = r.loops[:i]
					break
				}
			}
		}
		o, err := r.attempt(ctx, name, node)
		if err != nil {
			if ctx.Err() != nil {
				return Result{Status: "cancelled", Node: name}, ctx.Err()
			}
			next, ok := node.Next[PortError]
			if !ok {
				return r.finish(name, err)
			}
			r.steps[name] = map[string]any{"error": err.Error()}
			name = next
			continue
		}
		if o.Wait != "" {
			raw, err := r.x.Await(name, o.Wait)
			if err != nil {
				if ctx.Err() != nil {
					return Result{Status: "cancelled", Node: name}, ctx.Err()
				}
				next, ok := node.Next[PortError]
				if !ok {
					return r.finish(name, err)
				}
				r.steps[name] = map[string]any{"error": err.Error()}
				name = next
				continue
			}
			var decided map[string]any
			if err = decode(raw, &decided); err != nil {
				return r.finish(name, err)
			}
			o.Output, o.Port = decided, fmt.Sprint(decided["decision"])
		}
		r.steps[name] = o.Output
		for k, v := range o.Vars {
			r.vars[k] = v
		}
		if o.Fail != "" {
			return r.finish(name, errors.New(o.Fail))
		}
		if o.End {
			return r.finish(name, nil)
		}
		if o.Sleep > 0 {
			if err = r.x.Sleep(time.Duration(o.Sleep * float64(time.Second))); err != nil {
				return Result{Status: "cancelled", Node: name}, err
			}
		}
		port := o.Port
		if port == "" {
			port = PortDone
		}
		if node.Activity == forEachKey && port == portBody {
			out, _ := o.Output.(map[string]any)
			items, _ := out["items"].([]any)
			as, _ := out["as"].(string)
			f := loopFrame{node: name, items: items, as: as}
			r.loops = append(r.loops, f)
			r.loopVars(f)
		}
		next, ok := node.Next[port]
		if !ok && port != PortError {
			next, ok = node.Next[PortDone]
		}
		// A pass of a loop ends where its nodes have nothing next: the next
		// element starts, or after the last one the loop follows done.
		for !ok && len(r.loops) > 0 {
			f := &r.loops[len(r.loops)-1]
			if f.index++; f.index < len(f.items) {
				r.loopVars(*f)
				next, ok = flow.Nodes[f.node].Next[portBody], true
				break
			}
			loop := flow.Nodes[f.node]
			r.loops = r.loops[:len(r.loops)-1]
			next, ok = loop.Next[PortDone]
		}
		if !ok {
			return r.finish(name, nil)
		}
		name = next
	}
}

// attempt runs a node, retrying it as its retry policy says.
func (r *runState) attempt(ctx context.Context, name string, node Node) (outcome, error) {
	attempts, delay := 1, time.Duration(0)
	if node.Retry != nil {
		attempts = node.Retry.Attempts
		delay, _ = time.ParseDuration(node.Retry.Delay)
	}
	for i := 1; ; i++ {
		o, err := r.once(name, node)
		if err == nil {
			return o, nil
		}
		if i >= attempts || ctx.Err() != nil {
			return outcome{}, err
		}
		if delay > 0 {
			if e := r.x.Sleep(delay); e != nil {
				return outcome{}, e
			}
		}
	}
}

// once runs one attempt of a node. An activity with heavy work runs in
// three steps: its run in a step transaction returns either the outcome or a
// job; the job's work runs outside any transaction; and commit turns the
// work's result into the outcome in a second step transaction.
func (r *runState) once(name string, node Node) (outcome, error) {
	a := activities[node.Activity]
	raw, err := r.x.Step(name, func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
		return r.node(ctx, t, raise, name, func(e *env) (outcome, error) { return a.run(e, node.Inputs) })
	})
	if err != nil {
		return outcome{}, err
	}
	var o outcome
	if err = decode(raw, &o); err != nil || o.Job == nil {
		return o, err
	}
	job := o.Job
	result, err := r.x.Work(name+" work", func(ctx context.Context) (json.RawMessage, error) {
		out, err := a.work(&workEnv{ctx: ctx, tools: r.tools, runID: r.in.RunID}, job)
		if err == nil && len(out) > MaxOutputBytes {
			err = fmt.Errorf("node %s work result is larger than %d bytes", name, MaxOutputBytes)
		}
		return out, err
	})
	if err != nil {
		return outcome{}, err
	}
	raw, err = r.x.Step(name+" commit", func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
		return r.node(ctx, t, raise, name, func(e *env) (outcome, error) { return a.commit(e, job, result) })
	})
	if err != nil {
		return outcome{}, err
	}
	o = outcome{}
	return o, decode(raw, &o)
}

// node runs inside a step: tokens and the item are read in the node's
// transaction, and the outcome is recorded.
func (r *runState) node(ctx context.Context, t *dms.Service, raise func(dms.Event), name string, run func(*env) (outcome, error)) (json.RawMessage, error) {
	var item map[string]any
	sc := &scope{ctx: ctx, trigger: jsonish(r.in.Event).(map[string]any), inputs: r.in.Inputs, vars: r.vars, steps: r.steps, now: time.Now().UTC(),
		run: map[string]any{"id": r.in.RunID, "workflow_id": r.in.WorkflowID, "workflow_key": r.l.Key, "version": r.in.Version, "actor": r.in.Event.Actor, "items": r.items()}}
	if r.in.ItemID != "" {
		sc.item = func(ctx context.Context) (map[string]any, error) {
			if item == nil {
				res, err := t.GetSurface(ctx, r.l.Author, r.in.ItemID, "auto")
				if err != nil {
					return nil, fmt.Errorf("read the run's item: %w", err)
				}
				item = jsonish(snapshot(res)).(map[string]any)
			}
			return item, nil
		}
	}
	e := &env{ctx: dms.WithCause(ctx, r.in.Event.Depth, r.in.RunID), t: t, author: r.l.Author, itemID: r.in.ItemID, items: r.in.Items, runID: r.in.RunID, workspaceID: r.in.WorkspaceID, workflowID: r.in.WorkflowID, node: name, scope: sc,
		raise: func(event string, data map[string]any) { raise(r.event(WorkflowEvent(r.l.Key, event), data)) }}
	o, err := run(e)
	if err != nil {
		return nil, err
	}
	o.Output = jsonish(o.Output)
	out, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxOutputBytes {
		return nil, fmt.Errorf("node %s output is larger than %d bytes", name, MaxOutputBytes)
	}
	return out, nil
}

// items lists the run's items in order: a selection's members, else the
// run's item, else none.
func (r *runState) items() []any {
	ids := r.in.Items
	if len(ids) == 0 && r.in.ItemID != "" {
		ids = []string{r.in.ItemID}
	}
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// event is a workflow event of this run: it carries the run's item and is
// one level deeper than the event that started the run.
func (r *runState) event(typ string, data map[string]any) dms.Event {
	data["run_id"] = r.in.RunID
	return dms.Event{ID: dms.NewEventID(), Type: typ, WorkspaceID: r.in.WorkspaceID, CollectionID: r.in.Event.CollectionID, ResourceID: r.in.ItemID, Actor: r.in.Event.Actor, Data: data, Depth: r.in.Event.Depth + 1, CauseRunID: r.in.RunID}
}

// finish raises wf.{key}.completed or wf.{key}.failed in one last step.
func (r *runState) finish(node string, failure error) (Result, error) {
	res := Result{Status: "completed", Node: node, Variables: r.vars}
	data := map[string]any{"status": "completed", "node": node}
	if failure != nil {
		res.Status = "failed"
		data["status"] = "failed"
		data["error"] = failure.Error()
	}
	_, err := r.x.Step("finish", func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
		raise(r.event(WorkflowEvent(r.l.Key, data["status"].(string)), data))
		return json.RawMessage(`{}`), nil
	})
	if err != nil {
		return res, err
	}
	if failure != nil {
		return res, fmt.Errorf("%w at node %s: %v", ErrRunFailed, node, failure)
	}
	return res, nil
}

func decode(raw json.RawMessage, v any) error {
	return json.Unmarshal(raw, v)
}
