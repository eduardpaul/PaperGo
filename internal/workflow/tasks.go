package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"papergo/internal/forms"
	"papergo/internal/model"
	"papergo/internal/tasks"
)

// Task nodes create tasks (see package tasks): action requests whose
// assignees answer with one of the node's outcomes, optionally with a form.
// With wait (the default) the run waits without holding a worker: it parks,
// and the task's completion resumes it at the port named after the outcome.
// An approval node is a task with the outcomes approved and rejected.

// portsFromOutcomes marks an activity whose ports are its node's outcomes.
const portsFromOutcomes = "outcomes"

const (
	taskCommonProps = `"title": {"type": "string", "title": "Title"},
		"description": {"type": "string", "title": "Description"},
		"assignees": {"type": "array", "title": "Assignees", "description": "People, group:<id>, role:owners, role:members, creator, actor or field:<key>; none means the workspace's owners.", "items": {"type": "string"}, "maxItems": 50, "x-papergo": {"kind": "people", "access": "read_draft"}},
		` + itemIDProp + `,
		"key": {"type": "string", "title": "Key", "description": "A lowercase name, such as legal_review, that task triggers and lists can filter on."},
		"completion": {"type": "string", "title": "Completion", "description": "any: the first answer completes the task; all: every assignee answers (a group once).", "enum": ["any", "all"], "default": "any"},
		"input_schema": {"type": "object", "title": "Form", "description": "A form answers fill in, like a launch form; its values are {step:node.input...}."},
		"values": {"type": "object", "title": "Prefilled values", "description": "Initial form values; tokens such as {item:values.total} are resolved when the task is created."},
		"review": {"type": "object", "title": "Review", "description": "A staged file of this run to review: type file.compare (beside the current file) or file.preview, and staged_file_id.", "properties": {"type": {"type": "string", "enum": ["file.compare", "file.preview"]}, "staged_file_id": {"type": "string"}}},
		"priority": {"type": "string", "title": "Priority", "enum": ["low", "normal", "high"], "default": "normal"},
		"due": {"type": "string", "title": "Due", "description": "A duration such as 48h, or a date or RFC 3339 time."}`
	taskOutputSchema = `{"type": "object", "properties": {
		"task_id": {"type": "string"}, "status": {"type": "string"}, "outcome": {"type": "string"}, "completed_by": {"type": "string"}, "comment": {"type": "string"},
		"input": {"type": "object", "description": "The form values of the completing answer."},
		"responses": {"type": "array", "items": {"type": "object", "properties": {"subject": {"type": "string"}, "assignee": {"type": "string"}, "outcome": {"type": "string"}, "comment": {"type": "string"}, "input": {"type": "object"}}}}}}`
)

func init() {
	register(&Activity{Key: "task", Kind: "flow", PortsFrom: portsFromOutcomes,
		Description: "Asks the assignees to do something and answer with one of the outcomes, optionally filling in a form. With wait (the default), the run waits without holding a worker and follows the port named after the outcome; without, it creates the task and continues at done.",
		InputSchema: inputs(taskCommonProps+`,
			"outcomes": {"type": "array", "title": "Outcomes", "description": "The answers, each a port: key, label, style (primary, danger, default), comment (none, optional, required), skip_form, allow_stale, decisive. Default: done.", "maxItems": 10,
				"items": {"type": "object", "properties": {"key": {"type": "string"}, "label": {"type": "string"}, "style": {"type": "string", "enum": ["primary", "danger", "default"]}, "comment": {"type": "string", "enum": ["none", "optional", "required"]}, "skip_form": {"type": "boolean"}, "allow_stale": {"type": "boolean"}, "decisive": {"type": "boolean"}}, "required": ["key", "label"]}},
			"wait": {"type": "boolean", "title": "Wait for the answer", "default": true}`, "title"),
		OutputSchema: forms.MustParse(taskOutputSchema), run: runTask})
	register(&Activity{Key: "approval", Kind: "flow", Ports: []string{tasks.Approved, tasks.Rejected},
		Description:  "Asks the assignees to approve or reject, and waits for the answer: a task of kind approval. Rejecting needs a comment. An optional form collects input with the answer, and review shows a staged file of this run.",
		InputSchema:  inputs(taskCommonProps, "title"),
		OutputSchema: forms.MustParse(taskOutputSchema), run: runApproval})
	register(&Activity{Key: "task.cancel", Kind: "action", Description: "Cancels an open task of this workspace that no run waits for, such as a follow-up created without waiting that is no longer needed.",
		InputSchema:  inputs(`"task_id": {"type": "string", "title": "Task", "description": "Such as {step:ask.task_id}."}`, "task_id"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"task_id": {"type": "string"}, "status": {"type": "string"}}}`), run: runTaskCancel})
}

// nodeOutcomes reads a task node's literal outcomes; nil is the default.
func nodeOutcomes(in map[string]any) ([]model.TaskOutcome, error) {
	raw, ok := in["outcomes"]
	if !ok {
		return nil, nil
	}
	if _, isList := raw.([]any); !isList {
		return nil, errors.New("outcomes must be a literal list: they are the node's ports")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var out []model.TaskOutcome
	if err = dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("outcomes: %v", err)
	}
	if err = tasks.CheckOutcomes(out); err != nil {
		return nil, err
	}
	return out, nil
}

// NodePorts returns the ports a node may connect besides done and error.
func (a *Activity) NodePorts(in map[string]any) []string {
	if a.PortsFrom != portsFromOutcomes {
		return a.Ports
	}
	if wait, ok := in["wait"].(bool); ok && !wait {
		return nil
	}
	outcomes, _ := nodeOutcomes(in)
	if outcomes == nil {
		outcomes = tasks.DefaultOutcomes()
	}
	ports := make([]string, len(outcomes))
	for i, o := range outcomes {
		ports[i] = o.Key
	}
	return ports
}

// validateTask checks a task or approval node's literal inputs at save time.
func validateTask(a *Activity, in map[string]any) error {
	if a.PortsFrom == portsFromOutcomes {
		if _, err := nodeOutcomes(in); err != nil {
			return err
		}
		if w, ok := in["wait"]; ok {
			if _, isBool := w.(bool); !isBool {
				return errors.New("wait must be true or false")
			}
		}
	}
	if c, ok := in["completion"]; ok && c != tasks.CompletionAny && c != tasks.CompletionAll {
		return errors.New("completion is any or all")
	}
	var schema map[string]any
	if s, ok := in["input_schema"]; ok {
		if schema, ok = s.(map[string]any); !ok {
			return errors.New("input_schema must be a literal form")
		}
		if err := forms.Validate(schema, "input_schema"); err != nil {
			return err
		}
	}
	if v, ok := in["values"]; ok {
		if _, isMap := v.(map[string]any); !isMap || schema == nil {
			return errors.New("values must be an object and needs input_schema")
		}
	}
	if r, ok := in["review"].(map[string]any); ok {
		if err := tasks.CheckReviewShape(r); err != nil {
			return err
		}
	}
	if k, ok := in["key"].(string); ok && k != "" && !strings.Contains(k, "{") && !tasks.ValidKey(k) {
		return errors.New("key is a lowercase name such as legal_review")
	}
	return nil
}

func runTask(e *env, in map[string]any) (outcome, error) {
	outcomes, err := nodeOutcomes(in)
	if err != nil {
		return outcome{}, err
	}
	wait := true
	if w, ok := in["wait"].(bool); ok {
		wait = w
	}
	return e.createTask(in, tasks.KindTask, outcomes, wait)
}

func runApproval(e *env, in map[string]any) (outcome, error) {
	return e.createTask(in, tasks.KindApproval, tasks.ApprovalOutcomes(), true)
}

// createTask creates a task node's task as the workflow's author.
func (e *env) createTask(in map[string]any, kind string, outcomes []model.TaskOutcome, wait bool) (outcome, error) {
	ti := tasks.Input{WorkspaceID: e.workspaceID, Kind: kind, Outcomes: outcomes, Wait: wait,
		Workflow: &tasks.Source{WorkflowID: e.workflowID, RunID: e.runID, Node: e.node}}
	var err error
	if ti.Title, err = e.scope.text(in["title"]); err != nil {
		return outcome{}, err
	}
	for name, field := range map[string]*string{"description": &ti.Description, "key": &ti.Key, "completion": &ti.Completion, "priority": &ti.Priority} {
		if v, ok := in[name]; ok {
			if *field, err = e.scope.text(v); err != nil {
				return outcome{}, err
			}
		}
	}
	if v, ok := in["assignees"]; ok {
		if ti.Assignees, err = e.strings(v); err != nil {
			return outcome{}, err
		}
	}
	if _, ok := in["item_id"]; ok || e.itemID != "" {
		if ti.ItemID, err = e.target(in); err != nil {
			return outcome{}, err
		}
	}
	if s, ok := in["input_schema"].(map[string]any); ok {
		ti.InputSchema = s
	}
	if v, ok := in["values"]; ok {
		if ti.Values, err = e.object(v); err != nil {
			return outcome{}, err
		}
	}
	if r, ok := in["review"].(map[string]any); ok {
		id, err := e.scope.text(r["staged_file_id"])
		if err != nil {
			return outcome{}, err
		}
		ti.Review = map[string]any{"type": r["type"], "staged_file_id": id}
	}
	if v, ok := in["due"]; ok {
		s, err := e.scope.text(v)
		if err != nil {
			return outcome{}, err
		}
		if s != "" {
			due, err := dueAt(s, e.scope.now)
			if err != nil {
				return outcome{}, err
			}
			ti.DueAt = &due
		}
	}
	if actor, _ := e.scope.run["actor"].(string); actor != "" {
		ti.Actor = actor
	}
	tk, err := tasks.Create(e.ctx, e.t, e.author, ti)
	if err != nil {
		return outcome{}, err
	}
	out := map[string]any{"task_id": tk.ID}
	if !wait {
		return outcome{Output: out}, nil
	}
	return outcome{Wait: tk.ID, Output: out}, nil
}

// dueAt reads a due time: a duration from now, a date or an RFC 3339 time.
func dueAt(s string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("due %q is not in the future", s)
		}
		return now.Add(d).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("due %q is not a duration, date or time", s)
}

func runTaskCancel(e *env, in map[string]any) (outcome, error) {
	id, err := e.scope.text(in["task_id"])
	if err != nil {
		return outcome{}, err
	}
	if id == "" {
		return outcome{}, errors.New("task_id is empty")
	}
	tk, err := tasks.CancelByWorkflow(e.ctx, e.t, e.author, e.workspaceID, id)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"task_id": tk.ID, "status": string(tk.Status)}}, nil
}
