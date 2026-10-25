package workflow

import (
	"context"
	"fmt"

	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

// MaxCalls bounds the runs one call request starts.
const MaxCalls = 500

// Call asks for one run of a called workflow: on an item, or on none, with
// the input its input_schema checks.
type Call struct {
	ItemID string         `json:"item_id,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
}

// Calls asks for runs of the workflow with key Workflow.
type Calls struct {
	Workflow string `json:"workflow"`
	Calls    []Call `json:"calls"`
}

// StartCalls starts runs of a workflow of the calling run's workspace that
// declares a call trigger, in the calling run's transaction, and returns
// their IDs in order. The calling run, parent, acts as subject: it needs
// write access to each call's item, or manage access to the workspace for a
// call on no item, as a manual start does. Each run is an operation the
// calling run owns, so cancelling the caller cancels it, and the caller can
// wait for it like any operation it started; it acts as its own version's
// author. A called run is one level deeper than its caller, and calls stop
// at MaxDepth. Concurrency applies as to manual starts.
func (s *Service) StartCalls(ctx context.Context, subject, parent, workspaceID string, in Calls) (runs []string, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if parent == "" {
			return issue("run_required", "Workflows are called by workflow runs, with ctx.call.")
		}
		caller, err := t.DAL.Runs().Find(ctx, parent)
		if err != nil {
			return err
		}
		if caller.WorkspaceID != workspaceID {
			return dms.Invalid("a run calls workflows of its own workspace")
		}
		depth := caller.Depth + 1
		if depth >= MaxDepth {
			return dms.Invalid(fmt.Sprintf("calls nest at most %d deep", MaxDepth-1))
		}
		if len(in.Calls) == 0 || len(in.Calls) > MaxCalls {
			return dms.Invalid(fmt.Sprintf("a request makes 1 to %d calls", MaxCalls))
		}
		if err = t.RequireScope(ctx, subject, "item.write"); err != nil {
			return err
		}
		found, err := listWorkflows(ctx, t, workflows.Filter{WorkspaceID: workspaceID, Key: in.Workflow, LiveOnly: true})
		if err != nil {
			return err
		}
		if len(found) == 0 || found[0].State != workflows.StatePublished {
			return dms.Invalid("no published workflow " + in.Workflow + " in this workspace")
		}
		w := found[0]
		v, err := current(ctx, t, w)
		if err != nil {
			return err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return err
		}
		var trigger *Trigger
		for i := range def.Triggers {
			if def.Triggers[i].Type == TriggerCall && !def.Triggers[i].Disabled {
				trigger = &def.Triggers[i]
			}
		}
		if trigger == nil {
			return dms.Invalid("workflow " + in.Workflow + " has no call trigger")
		}
		managed := false
		events := make([]dms.Event, len(in.Calls))
		for i, c := range in.Calls {
			at := fmt.Sprintf("calls[%d]", i)
			inputs, err := forms.Values(def.InputSchema, c.Input, at+".input")
			if err != nil {
				return err
			}
			if def.InputSchema != nil {
				if err = forms.CheckValues(ctx, t, subject, workspaceID, def.InputSchema, inputs, at+".input"); err != nil {
					return err
				}
			}
			e := dms.Event{ID: dms.NewEventID(), Type: TriggerCall, WorkspaceID: workspaceID, Actor: caller.Actor, CauseRunID: parent, Depth: depth,
				Data: map[string]any{"workflow_id": w.ID, "workflow_version": v.Number, "inputs": inputs, "parent_run_id": parent}}
			if c.ItemID == "" {
				if trigger.CollectionID != "" {
					return issue("selection_required", at+": workflow "+in.Workflow+" is called on an item of its library or list")
				}
				if !managed {
					if err = workspace(ctx, t, subject, workspaceID, "manage"); err != nil {
						return err
					}
					managed = true
				}
				events[i] = e
				continue
			}
			r, err := t.Authorize(ctx, subject, c.ItemID, "write")
			if err != nil {
				return err
			}
			if r.Kind != "item" || r.WorkspaceID != workspaceID {
				return issue("wrong_workspace", at+": call workflows on items of the run's workspace")
			}
			if trigger.CollectionID != "" && (r.ContainerID == nil || *r.ContainerID != trigger.CollectionID) {
				return issue("wrong_collection", at+": workflow "+in.Workflow+" is called on items of its library or list")
			}
			e.CollectionID, e.ResourceID = *r.ContainerID, c.ItemID
			if def.Condition != nil {
				holds, err := matches(ctx, t, v.CreatedBy, e.CollectionID, c.ItemID, def.Condition)
				if err != nil {
					return err
				}
				if !holds {
					return dms.Invalid(at + ": item " + c.ItemID + " does not meet the workflow's condition")
				}
			}
			events[i] = e
		}
		_, cancel, err := Match(ctx, t, events)
		if err != nil {
			return err
		}
		if err = t.CancelOperations(ctx, cancel); err != nil {
			return err
		}
		runs = make([]string, len(events))
		for i, e := range events {
			runs[i] = RunID(w.ID, e.ID)
		}
		return nil
	})
	return
}
