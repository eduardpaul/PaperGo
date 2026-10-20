package workflow

import (
	"context"
	"encoding/json"

	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
)

// RunPayload is a run operation's payload: what its script sees as ctx.run
// and its input. It is fixed when the run starts, so every slice of the run
// replays the same.
type RunPayload struct {
	WorkflowID  string         `json:"workflow_id"`
	Key         string         `json:"key"`
	Version     int            `json:"version"`
	WorkspaceID string         `json:"workspace_id"`
	ItemID      string         `json:"item_id,omitempty"`
	Items       []string       `json:"items,omitempty"`
	Event       RunEvent       `json:"event"`
	Input       map[string]any `json:"input,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	// Role is the role the workflow fills, whose events its run raises.
	Role string `json:"role,omitempty"`
	// System marks a run of a system built-in.
	System bool `json:"system,omitempty"`
	// StartError fails the run before its script runs, with the reason.
	StartError string `json:"start_error,omitempty"`
	// Serial names the runs of a coalescing workflow on one item, which run
	// one at a time: a run that starts while another is active waits for
	// it under SerialExecution(Serial).
	Serial string `json:"serial,omitempty"`
}

// RunEvent is the event that started a run, as its script sees it.
type RunEvent struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	WorkspaceID  string         `json:"workspace_id"`
	CollectionID string         `json:"collection_id,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Actor        string         `json:"actor"`
	Depth        int            `json:"depth"`
	Data         map[string]any `json:"data"`
}

// Context is the run as ctx.run sees it, with its input.
func (p RunPayload) Context() map[string]any {
	items := p.Items
	if items == nil {
		items = []string{}
	}
	params := p.Params
	if params == nil {
		params = map[string]any{}
	}
	out := map[string]any{
		"workflow":     map[string]any{"id": p.WorkflowID, "key": p.Key, "version": p.Version},
		"workspace_id": p.WorkspaceID, "items": items, "event": p.Event, "actor": p.Event.Actor, "params": params,
		"input": p.Input,
	}
	if p.ItemID != "" {
		out["item_id"] = p.ItemID
	}
	return out
}

// SerialExecution is the execution a coalescing run waits under for the
// active run of its workflow on the same item.
func SerialExecution(serial string) string { return "serial:" + serial }

// newRun records a run operation and its provenance: the operation, which
// the run's worker executes, and the run row with its selection.
func newRun(ctx context.Context, t *dms.Service, author string, run runstore.CreateInput, payload RunPayload, waiting bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	op := operations.Operation{ID: run.ID, Kind: dms.OperationKindRun, CreatedBy: author, ContainerID: run.WorkspaceID, Payload: raw}
	if waiting {
		op.ExecutionID = SerialExecution(payload.Serial)
	}
	if _, err = t.AcceptOperation(ctx, op); err != nil {
		return err
	}
	if _, err = t.DAL.Runs().Create(ctx, run); err != nil {
		return err
	}
	for position, itemID := range payload.Items {
		if err = t.DAL.Runs().AddItem(ctx, run.ID, itemID, position); err != nil {
			return err
		}
	}
	return nil
}
