package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
)

// TriggerType is a kind of trigger a module serves, such as
// connection.message: runs start on the module's events of its Name, and
// its triggers carry the module's own options in Trigger.Options. The
// module raises the events (dms.Service.Raise) and decides which triggers
// they start.
type TriggerType struct {
	// Name is the trigger's type and its events' type.
	Name string
	// Validate checks a trigger's options, without the database. The
	// trigger's other fields are checked as for every type; a module type
	// takes collection_id only.
	Validate func(Trigger) error
	// Check checks a trigger for the workflow's author in its workspace,
	// when the workflow is saved: what the options name exists, and the
	// author may use it.
	Check func(ctx context.Context, t *dms.Service, author, workspaceID string, tr Trigger) error
	// Accepts reports whether an event of the type starts a run of a
	// trigger, beyond its type, workspace and collection: m names the
	// workflow and its author, who the run acts as.
	Accepts func(ctx context.Context, t *dms.Service, m TriggerMatch, e dms.Event) (bool, error)
}

// TriggerMatch is a trigger of a workflow an event may start.
type TriggerMatch struct {
	WorkflowID  string
	WorkspaceID string
	// Position is the trigger's place in its definition's triggers.
	Position int
	Author   string
	Trigger  Trigger
}

var triggerTypes = map[string]TriggerType{}

// RegisterTriggerType adds a module's trigger type, replacing one of the
// same name as roles are. A name the engine serves is an error.
func RegisterTriggerType(tt TriggerType) error {
	if tt.Name == "" || tt.Validate == nil || tt.Accepts == nil {
		return fmt.Errorf("a trigger type needs a name, a validation and a match")
	}
	if isItemTrigger(tt.Name) || tt.Name == TriggerSchedule || tt.Name == TriggerManual || tt.Name == TriggerCall || eventPattern.MatchString(tt.Name) || roleEventPattern.MatchString(tt.Name) {
		return fmt.Errorf("trigger type %s is the engine's", tt.Name)
	}
	triggerTypes[tt.Name] = tt
	return nil
}

// moduleTrigger returns the module type serving a trigger, if one does.
func moduleTrigger(name string) (TriggerType, bool) {
	tt, ok := triggerTypes[name]
	return tt, ok
}

// validateModuleTrigger checks a module trigger's shape: options and
// collection_id, nothing the engine's own types take.
func validateModuleTrigger(at string, tt TriggerType, t Trigger) error {
	if t.ContentTypeID != "" || t.Cron != "" || t.TimeZone != "" || t.Surface != "" || t.Selection != "" || t.Access != "" || len(t.Terms) > 0 || t.TermChange != "" || len(t.Sources) > 0 || len(t.MediaTypes) > 0 || t.TaskKey != "" || len(t.Actions) > 0 {
		return dms.Invalid(at + tt.Name + " triggers take options and collection_id")
	}
	if err := tt.Validate(t); err != nil {
		return dms.Invalid(at + err.Error())
	}
	return nil
}

// ModuleTriggers returns the triggers of a module type in the published,
// automatic workflows that serve them, with their authors, for the module
// that raises the type's events.
func (s *Service) ModuleTriggers(ctx context.Context, name string) ([]TriggerMatch, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]TriggerMatch, error) {
		rows, err := t.DAL.Workflows().Triggers(ctx, workflows.TriggerFilter{Type: name})
		if err != nil {
			return nil, err
		}
		ids := map[string]bool{}
		for _, r := range rows {
			ids[r.WorkflowID] = true
		}
		if len(ids) == 0 {
			return nil, nil
		}
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		found, err := listWorkflows(ctx, t, workflows.Filter{IDs: list, AutomaticEnabled: boolPtr(true), State: workflows.StatePublished, LiveOnly: true})
		if err != nil {
			return nil, err
		}
		var out []TriggerMatch
		for _, w := range found {
			v, err := current(ctx, t, w)
			if err != nil {
				return nil, err
			}
			def, err := ParseDefinition(v.Definition)
			if err != nil {
				return nil, err
			}
			for i, tr := range def.Triggers {
				if tr.Type == name && !tr.Disabled {
					out = append(out, TriggerMatch{WorkflowID: w.ID, WorkspaceID: w.WorkspaceID, Position: i, Author: v.CreatedBy, Trigger: tr})
				}
			}
		}
		return out, nil
	})
}

// TriggerOptions decodes a module trigger's options strictly into dst.
func TriggerOptions(t Trigger, dst any) error {
	if len(t.Options) == 0 {
		return fmt.Errorf("%s triggers need options", t.Type)
	}
	dec := json.NewDecoder(bytes.NewReader(t.Options))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("options: %v", err)
	}
	return nil
}
