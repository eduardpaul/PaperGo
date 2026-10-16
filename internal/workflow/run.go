package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"papergo/ent/workflowversion"
	"papergo/internal/dms"
	"papergo/internal/forms"
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
	// Partition, for coalesce workflows, names the workflow and item: runs
	// of one partition execute one at a time.
	Partition string `json:"partition,omitempty"`
	// System runs belong to a system built-in: they use the runner's system
	// queue and are kept a day once they succeed.
	System bool `json:"system,omitempty"`
}

// UnmarshalJSON preserves arbitrary JSON numbers when durable execution reloads a run.
func (in *RunInput) UnmarshalJSON(raw []byte) error {
	type plain RunInput
	return decisionDecode(raw, (*plain)(in))
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
	// Await waits for park, which node created, and returns its completion.
	// DBOS durably records the received completion or timeout.
	Await(node string, park Park) (json.RawMessage, error)
	Sleep(d time.Duration) error
}

// Result is a finished run's output.
type Result struct {
	Status    string         `json:"status"`
	Node      string         `json:"node,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
	// Outputs are a completed run's declared outputs (Definition.Outputs).
	Outputs map[string]any `json:"outputs,omitempty"`
}

// ErrRunFailed wraps the reason a run failed.
var ErrRunFailed = errors.New("workflow run failed")

type loaded struct {
	Key    string `json:"key"`
	Author string `json:"author"`
	System bool   `json:"system,omitempty"`
	// Role is the role the workflow fills: its runs also raise the role's
	// events.
	Role       string     `json:"role,omitempty"`
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
		l := loaded{Key: w.Key, Author: v.CreatedBy, System: systemWorkflow(w), Definition: def}
		if w.Role != nil {
			l.Role = *w.Role
		}
		return json.Marshal(l)
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
		r.vars[k] = forms.Plain(v)
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
	loops             []loopFrame
	invocation        uint64
	currentInvocation uint64
}

// loopFrame is a forEach node's progress: its items, recorded by the node's
// step, and the element the current pass runs with.
type loopFrame struct {
	node  string
	items []any
	index int
	as    string
	// visits are the nodes visited before the loop, in the enclosing pass.
	visits int
}

func (r *runState) loopVars(f loopFrame) {
	r.vars[f.as] = f.items[f.index]
	r.vars[f.as+"_index"] = f.index
}

func (r *runState) execute(ctx context.Context) (Result, error) {
	flow := r.l.Definition.Flow
	name := flow.Start
	// visits counts the nodes of the current loop pass, or outside loops.
	visits := 0
	for total := 1; ; total++ {
		node := flow.Nodes[name]
		if node.Activity == forEachKey {
			// Reaching a loop through a port starts it again, also when an
			// outer loop's next pass reaches an inner one.
			for i, f := range r.loops {
				if f.node == name {
					r.loops, visits = r.loops[:i], f.visits
					break
				}
			}
		}
		if visits++; visits > MaxNodeVisits {
			return r.finish(name, fmt.Errorf("the run visited more than %d nodes without starting a loop pass", MaxNodeVisits))
		}
		if total > MaxRunVisits {
			return r.finish(name, fmt.Errorf("the run visited more than %d nodes", MaxRunVisits))
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
			f := loopFrame{node: name, items: items, as: as, visits: visits}
			r.loops = append(r.loops, f)
			r.loopVars(f)
			visits = 0
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
				next, ok, visits = flow.Nodes[f.node].Next[portBody], true, 0
				break
			}
			loop := flow.Nodes[f.node]
			r.loops, visits = r.loops[:len(r.loops)-1], f.visits
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
		r.invocation++
		r.currentInvocation = r.invocation
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
	if err = decode(raw, &o); err != nil {
		return outcome{}, err
	}
	return r.continueActivity(name, a, o)
}

// continueActivity applies the same lifecycle to immediate work and work
// requested by Resume. Each transition has a durable DBOS boundary.
func (r *runState) continueActivity(name string, a *Activity, o outcome) (outcome, error) {
	for transition := 0; transition < MaxNodeVisits; transition++ {
		label := fmt.Sprintf("%s transition %d", name, transition)
		switch {
		case o.Job != nil:
			if a.work == nil {
				return outcome{}, fmt.Errorf("activity %s returned work without a Work handler", a.Key)
			}
			job := o.Job
			result, err := r.x.Work(label+" work", func(ctx context.Context) (json.RawMessage, error) {
				out, err := a.work(&workEnv{ctx: ctx, tools: r.tools, runID: r.in.RunID}, job)
				if err == nil && len(out) > MaxOutputBytes {
					err = fmt.Errorf("node %s work result is larger than %d bytes", name, MaxOutputBytes)
				}
				return out, err
			})
			if err != nil {
				return outcome{}, err
			}
			raw, err := r.x.Step(label+" commit", func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
				if err := t.ReleaseBlobLeases(ctx, r.executionID(name)); err != nil {
					return nil, err
				}
				return r.node(ctx, t, raise, name, func(e *env) (outcome, error) { return a.commit(e, job, result) })
			})
			if err != nil {
				return outcome{}, err
			}
			o = outcome{}
			if err = decode(raw, &o); err != nil {
				return outcome{}, err
			}
		case o.Park != nil:
			park := *o.Park
			raw, err := r.x.Await(label, park)
			if err != nil {
				return outcome{}, err
			}
			var completion Completion
			if err = decode(raw, &completion); err != nil {
				return outcome{}, err
			}
			raw, err = r.x.Step(label+" resume", func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
				result, err := r.node(ctx, t, raise, name, func(e *env) (outcome, error) {
					if a.resume != nil {
						return a.resume(e, completion)
					}
					if completion.Error != "" {
						return outcome{}, errors.New(completion.Error)
					}
					return outcome{Output: completion.Output, Port: completion.Port}, nil
				})
				if err != nil {
					return nil, err
				}
				if err = ConsumeWait(ctx, t, park.Kind, park.Key, r.in.RunID); err != nil {
					return nil, err
				}
				return result, nil
			})
			if err != nil {
				// A failed Resume is a durably consumed completion too. Drop
				// its parking marker before a retry, delay or error branch.
				_, releaseErr := r.x.Step(label+" release failed wait", func(ctx context.Context, t *dms.Service, _ func(dms.Event)) (json.RawMessage, error) {
					if err := ConsumeWait(ctx, t, park.Kind, park.Key, r.in.RunID); err != nil {
						return nil, err
					}
					return json.RawMessage(`{}`), nil
				})
				return outcome{}, errors.Join(err, releaseErr)
			}
			o = outcome{}
			if err = decode(raw, &o); err != nil {
				return outcome{}, err
			}
		default:
			return o, nil
		}
	}
	return outcome{}, fmt.Errorf("activity %s exceeded its lifecycle transition limit", a.Key)
}

// node runs inside a step: tokens and the item are read in the node's
// transaction, and the outcome is recorded.
func (r *runState) node(ctx context.Context, t *dms.Service, raise func(dms.Event), name string, run func(*env) (outcome, error)) (json.RawMessage, error) {
	// Each step rechecks live scopes, including after a durable wait or replay.
	// System built-ins run release definitions, which never expose the payload.
	if r.in.Event.Type == dms.EventTermChanged && !r.l.System {
		if err := t.RequireScope(ctx, r.l.Author, "taxonomy.read"); err != nil {
			return nil, fmt.Errorf("read term.changed trigger requires taxonomy.read: %w", err)
		}
	}
	sc := r.scope(ctx, t)
	e := &env{ctx: dms.WithCause(ctx, r.in.Event.Depth, r.in.RunID), t: t, author: r.l.Author, itemID: r.in.ItemID, items: r.in.Items, runID: r.in.RunID, workspaceID: r.in.WorkspaceID, workflowID: r.in.WorkflowID, node: name, executionID: r.executionID(name), scope: sc, tools: r.tools,
		raise: func(event string, data map[string]any) { raise(r.event(WorkflowEvent(r.l.Key, event), data)) }}
	o, err := run(e)
	if err != nil {
		return nil, err
	}
	if o.Job != nil {
		if err = t.LeaseBlobs(ctx, e.executionID, e.runID, o.BlobIDs); err != nil {
			return nil, err
		}
	}
	if o.Park != nil {
		deadline := o.Park.Deadline
		var at *time.Time
		if !deadline.IsZero() {
			at = &deadline
		}
		if err = PutWait(ctx, t, Wait{Kind: o.Park.Kind, Key: o.Park.Key, WorkspaceID: e.workspaceID, RunID: e.runID, Node: e.node, ExecutionID: e.executionID, Data: o.Park.Data, Deadline: at, Parking: true, Queue: o.Park.Queue, PayloadID: o.Park.PayloadID, BlobIDs: o.Park.BlobIDs}); err != nil {
			return nil, err
		}
	}
	o.Output = forms.Plain(o.Output)
	out, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxOutputBytes {
		return nil, fmt.Errorf("node %s output is larger than %d bytes", name, MaxOutputBytes)
	}
	return out, nil
}

// scope is what tokens read in a step: the run's data, and its item read
// once through t when a token or activity needs it.
func (r *runState) scope(ctx context.Context, t *dms.Service) *scope {
	var item map[string]any
	sc := &scope{ctx: ctx, trigger: forms.Plain(r.in.Event).(map[string]any), inputs: r.in.Inputs, vars: r.vars, steps: r.steps, now: time.Now().UTC(),
		run: map[string]any{"id": r.in.RunID, "workflow_id": r.in.WorkflowID, "workflow_key": r.l.Key, "version": r.in.Version, "actor": r.in.Event.Actor, "items": r.items()}}
	if r.in.ItemID != "" {
		sc.item = func(ctx context.Context) (map[string]any, error) {
			if item == nil {
				res, err := t.GetSurface(ctx, r.l.Author, r.in.ItemID, "auto")
				if err != nil {
					return nil, fmt.Errorf("read the run's item: %w", err)
				}
				item = forms.Plain(snapshot(res)).(map[string]any)
			}
			return item, nil
		}
	}
	return sc
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

// finished is what the finish step records: a completed run's outputs, or
// the reason they failed it.
type finished struct {
	Outputs map[string]any `json:"outputs,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// finish evaluates a completed run's outputs and raises wf.{key}.completed
// or wf.{key}.failed in one last step. Outputs that fail their schema fail
// the run, so a broken chain shows where it broke.
func (r *runState) finish(node string, failure error) (Result, error) {
	raw, err := r.x.Step("finish", func(ctx context.Context, t *dms.Service, raise func(dms.Event)) (json.RawMessage, error) {
		var f finished
		if failure == nil && r.l.Definition.OutputSchema != nil {
			outputs, err := r.outputs(r.scope(ctx, t))
			if err != nil {
				f.Error = err.Error()
			}
			f.Outputs = outputs
		}
		data := map[string]any{"status": "completed", "node": node}
		switch {
		case failure != nil:
			data["status"], data["error"] = "failed", failure.Error()
		case f.Error != "":
			data["status"], data["error"] = "failed", f.Error
		case f.Outputs != nil:
			data["outputs"] = f.Outputs
		}
		raise(r.event(WorkflowEvent(r.l.Key, data["status"].(string)), data))
		// Followers of a role hear every filler, whichever fills it for the
		// item's collection.
		if r.l.Role != "" {
			raise(r.event(RoleEvent(r.l.Role, data["status"].(string)), data))
		}
		return json.Marshal(f)
	})
	res := Result{Status: "completed", Node: node, Variables: r.vars}
	if err != nil {
		res.Status = "failed"
		return res, err
	}
	var f finished
	if err = decode(raw, &f); err != nil {
		res.Status = "failed"
		return res, err
	}
	if failure == nil && f.Error != "" {
		failure = errors.New(f.Error)
	}
	if failure != nil {
		res.Status = "failed"
		return res, fmt.Errorf("%w at node %s: %v", ErrRunFailed, node, failure)
	}
	res.Outputs = f.Outputs
	return res, nil
}

// outputs resolves the definition's outputs and checks them against its
// output schema, defaults applied.
func (r *runState) outputs(sc *scope) (map[string]any, error) {
	resolved, err := sc.resolve(forms.Plain(r.l.Definition.Outputs))
	if err != nil {
		return nil, fmt.Errorf("outputs: %w", err)
	}
	values, _ := resolved.(map[string]any)
	// A token that finds nothing leaves the output unset, so an optional
	// output can be absent.
	for name, v := range values {
		if v == nil {
			delete(values, name)
		}
	}
	out, err := forms.Values(r.l.Definition.OutputSchema, values, "outputs")
	if err != nil {
		return nil, err
	}
	if raw, err := json.Marshal(out); err != nil || len(raw) > MaxRunOutputBytes {
		return nil, fmt.Errorf("outputs are larger than %d bytes; pass references such as IDs instead of content", MaxRunOutputBytes)
	}
	return forms.Plain(out).(map[string]any), nil
}

func decode(raw json.RawMessage, v any) error {
	return decisionDecode(raw, v)
}

// executionID is stable for one visit of a node, including the loop iteration
// it runs in, and stable again when that step is replayed.
func (r *runState) executionID(node string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(r.in.RunID))
	var visit [8]byte
	binary.BigEndian.PutUint64(visit[:], r.currentInvocation)
	_, _ = h.Write(visit[:])
	for _, f := range r.loops {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(f.node))
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(f.index))
		_, _ = h.Write(n[:])
	}
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(node))
	return hex.EncodeToString(h.Sum(nil)[:16])
}
