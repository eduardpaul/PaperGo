// Package workflow is PaperGo's automation model: workflows are scripts
// whose export declares their triggers, condition and forms, with immutable
// versions, built-in workflows and roles, and the matching of events and
// schedules to runs. internal/scriptrun executes runs. docs/workflows.md is
// the user documentation.
package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/model"
	"papergo/internal/tasks"

	"github.com/robfig/cron/v3"
)

// Trigger types. Item triggers react to committed item writes, schedule
// triggers to cron occurrences, manual triggers to starts through the API,
// call triggers to other runs' ctx.call, and workflow events
// (wf.{key}.{event}) to other runs ending.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	TriggerCall     = "call"
)

// ItemTriggers are the trigger types raised by item writes.
var ItemTriggers = []string{dms.EventItemArchived, dms.EventItemUnarchived, dms.EventItemCreated, dms.EventItemUpdated, dms.EventItemPublished, dms.EventItemUnpublished, dms.EventItemDeleted, dms.EventItemRestored, dms.EventItemPurged, dms.EventFileAdded, dms.EventFileUpdated}

// FolderTriggers are the trigger types raised by folder writes.
var FolderTriggers = []string{dms.EventFolderCreated, dms.EventFolderUpdated, dms.EventFolderDeleted, dms.EventFolderRestored, dms.EventFolderPurged}

// Limits keep definitions and runs bounded.
const (
	// MaxRunOutputBytes bounds what a run returns: large data travels as
	// references, such as IDs, never as content.
	MaxRunOutputBytes = 16 * 1024
	MaxTriggers       = 20
	MaxDepth          = 5
	MaxScheduled      = 500
	// MaxSourceBytes bounds a workflow script.
	MaxSourceBytes = 1 << 20
)

// Definition is what a workflow version's script declares in its export.
type Definition struct {
	Triggers  []Trigger       `json:"triggers"`
	Condition *dms.FilterExpr `json:"condition,omitempty"`
	// InputSchema is the launch form of manual starts (see forms.go); its
	// values are the run's input.
	InputSchema map[string]any `json:"input_schema,omitempty"`
	// Concurrency decides what a start does while a run of the workflow on
	// the same item is still active: parallel (the default) starts anyway,
	// skip starts nothing, replace cancels the active run, and coalesce starts
	// nothing while a run is queued and otherwise queues one run that starts
	// when the active one ends. Selection runs compare every member.
	Concurrency string `json:"concurrency,omitempty"`
	// OutputSchema is the form of what a completed run returns. The output
	// is checked against it and carried in wf.{key}.completed as
	// data.outputs, so other workflows can follow.
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

// Concurrency policies.
const (
	ConcurrencyParallel = "parallel"
	ConcurrencySkip     = "skip"
	ConcurrencyReplace  = "replace"
	ConcurrencyCoalesce = "coalesce"
)

// Trigger starts runs. CollectionID limits item and workflow-event triggers
// to one list or library, and makes a schedule start one run per item of
// that collection that meets the condition on Surface (head by default).
type Trigger struct {
	Disabled      bool   `json:"disabled,omitempty"`
	Type          string `json:"type"`
	CollectionID  string `json:"collection_id,omitempty"`
	ContentTypeID string `json:"content_type_id,omitempty"`
	Cron          string `json:"cron,omitempty"`
	TimeZone      string `json:"time_zone,omitempty"`
	Surface       string `json:"surface,omitempty"`
	// Selection is per_item (exactly one item) or selection (one ordered
	// selection of items). A collection implies per_item by default; without
	// either option a manual trigger is standalone and accepts no items.
	Selection string `json:"selection,omitempty"`
	// Terms, on item.created, item.updated and item.published triggers,
	// limits them to revisions whose term and keywords fields hold one of
	// these terms (or a descendant, or a term merged into one). TermChange
	// is present (the default), added (held now, not by the revision before)
	// or removed (held before, not now).
	Terms      []string `json:"terms,omitempty"`
	TermChange string   `json:"term_change,omitempty"`
	// Sources, on file.added and file.updated triggers, are the file sources that start runs
	// (dms.FileSources). The default is every source except workflow, so a
	// workflow that replaces a file does not start itself again.
	Sources []string `json:"sources,omitempty"`
	// MediaTypes, on file.added and file.updated triggers, limits them to files of these
	// media types: exact (application/pdf) or a wildcard (image/*).
	MediaTypes []string `json:"media_types,omitempty"`
	// TaskKey, on task.created, task.completed and task.cancelled triggers,
	// limits them to tasks with this key.
	TaskKey string `json:"task_key,omitempty"`
	// Actions, on permissions.changed triggers, limits them to these changes
	// (PermissionActions); the default is all.
	Actions []string `json:"actions,omitempty"`
}

// PermissionActions are the changes a permissions.changed event reports.
var PermissionActions = []string{"replace", "move", "member.set", "member.remove"}

// acceptsAction reports whether a permissions.changed trigger accepts action.
func (t Trigger) acceptsAction(action string) bool {
	return len(t.Actions) == 0 || slices.Contains(t.Actions, action)
}

// sources returns the file sources a file trigger accepts.
func (t Trigger) sources() []string {
	if len(t.Sources) > 0 {
		return t.Sources
	}
	return []string{dms.FileUpload, dms.FileWebDAV, dms.FileRestore}
}

// acceptsMediaType reports whether a file trigger accepts a file's
// media type.
func (t Trigger) acceptsMediaType(media string) bool {
	if len(t.MediaTypes) == 0 {
		return true
	}
	for _, m := range t.MediaTypes {
		if m == media || strings.HasSuffix(m, "/*") && strings.HasPrefix(media, strings.TrimSuffix(m, "*")) {
			return true
		}
	}
	return false
}

// mediaTypePattern is a media type or a type/* wildcard.
var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]{0,62}/([a-z0-9][a-z0-9!#$&^_.+-]{0,62}|\*)$`)

// Term changes a trigger's terms filter reacts to.
const (
	TermPresent = "present"
	TermAdded   = "added"
	TermRemoved = "removed"
)

// MaxTriggerTerms bounds a trigger's terms filter.
const MaxTriggerTerms = 20

// Selection modes of manual triggers.
const (
	SelectionPerItem = "per_item"
	SelectionAll     = "selection"
)

var (
	keyPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	eventPattern = regexp.MustCompile(`^wf\.([a-z0-9][a-z0-9_-]*)\.([a-z0-9][a-z0-9_-]*)$`)
)

// WorkflowEvent is the event type a run of workflow key raises.
func WorkflowEvent(key, event string) string { return "wf." + key + "." + event }

// RoleEvent is the event type a run of a workflow filling role raises when
// it ends: completed or failed.
func RoleEvent(role, event string) string { return "role." + role + "." + event }

// roleEventPattern matches role.{role}.completed and role.{role}.failed.
var roleEventPattern = regexp.MustCompile(`^role\.([a-z0-9][a-z0-9._-]*)\.(completed|failed)$`)

func isItemTrigger(t string) bool {
	for _, v := range ItemTriggers {
		if v == t {
			return true
		}
	}
	return false
}

// ParseDefinition decodes raw strictly: unknown fields are errors, so a typo
// never silently changes what a workflow does.
func ParseDefinition(raw json.RawMessage) (Definition, error) {
	var d Definition
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return d, dms.Invalid("invalid workflow definition: " + err.Error())
	}
	if dec.More() {
		return d, dms.Invalid("invalid workflow definition: trailing data")
	}
	return d, nil
}

// schedule returns the parsed cron schedule of t.
func (t Trigger) schedule() (cron.Schedule, error) {
	schedule, err := model.WorkflowSchedule(t.Cron, t.TimeZone)
	if err != nil {
		return nil, dms.Invalid(err.Error())
	}
	return schedule, nil
}

// validate checks everything that needs no database: shapes and limits.
// References to collections, content types and fields are checked by the
// store.
func (d Definition) validate() error {
	if len(d.Triggers) == 0 || len(d.Triggers) > MaxTriggers {
		return dms.Invalid(fmt.Sprintf("a workflow needs 1 to %d triggers", MaxTriggers))
	}
	manual, calls := 0, 0
	for i, t := range d.Triggers {
		if t.Type == TriggerManual && t.Disabled {
			return dms.Invalid("use manual_enabled to control manual runs")
		}
		at := fmt.Sprintf("triggers[%d]: ", i)
		if t.Surface != "" && (t.Type != TriggerSchedule || t.CollectionID == "" || t.Surface != "head" && t.Surface != "published") {
			return dms.Invalid(at + "surface is head or published, on schedule triggers with collection_id")
		}
		if t.Selection != "" && (t.Type != TriggerManual || t.Selection != SelectionPerItem && t.Selection != SelectionAll) {
			return dms.Invalid(at + "selection is per_item or selection, on manual triggers")
		}
		if len(t.Terms) > 0 || t.TermChange != "" {
			if t.Type != dms.EventItemCreated && t.Type != dms.EventItemUpdated && t.Type != dms.EventItemPublished {
				return dms.Invalid(at + "terms belong to item.created, item.updated and item.published triggers")
			}
			if len(t.Terms) == 0 || len(t.Terms) > MaxTriggerTerms {
				return dms.Invalid(at + fmt.Sprintf("terms needs 1 to %d term IDs", MaxTriggerTerms))
			}
			for _, id := range t.Terms {
				if id == "" {
					return dms.Invalid(at + "terms must hold term IDs")
				}
			}
			switch t.TermChange {
			case "", TermPresent, TermAdded, TermRemoved:
			default:
				return dms.Invalid(at + "term_change is present, added or removed")
			}
		}
		if len(t.Sources) > 0 {
			if t.Type != dms.EventFileAdded && t.Type != dms.EventFileUpdated {
				return dms.Invalid(at + "sources belong to file.added and file.updated triggers")
			}
			seen := map[string]bool{}
			for _, s := range t.Sources {
				if !slices.Contains(dms.FileSources, s) || seen[s] {
					return dms.Invalid(at + "sources are distinct values of " + strings.Join(dms.FileSources, ", "))
				}
				seen[s] = true
			}
		}
		if len(t.MediaTypes) > 0 {
			if t.Type != dms.EventFileAdded && t.Type != dms.EventFileUpdated {
				return dms.Invalid(at + "media_types belong to file.added and file.updated triggers")
			}
			seen := map[string]bool{}
			for _, m := range t.MediaTypes {
				if len(t.MediaTypes) > 20 || !mediaTypePattern.MatchString(m) || seen[m] {
					return dms.Invalid(at + "media_types are up to 20 distinct lower-case media types, such as application/pdf, or wildcards such as image/*")
				}
				seen[m] = true
			}
		}
		if len(t.Actions) > 0 {
			if t.Type != dms.EventPermissionsChanged {
				return dms.Invalid(at + "actions belong to permissions.changed triggers")
			}
			seen := map[string]bool{}
			for _, a := range t.Actions {
				if !slices.Contains(PermissionActions, a) || seen[a] {
					return dms.Invalid(at + "actions are distinct values of " + strings.Join(PermissionActions, ", "))
				}
				seen[a] = true
			}
		}
		if t.TaskKey != "" && (!slices.Contains(tasks.Events, t.Type) || !tasks.ValidKey(t.TaskKey)) {
			return dms.Invalid(at + "task_key is a task key, on task.created, task.completed and task.cancelled triggers")
		}
		switch {
		case isItemTrigger(t.Type):
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
			if (t.Type == dms.EventItemDeleted || t.Type == dms.EventItemPurged) && d.Condition != nil {
				return dms.Invalid(at + "a condition cannot test deleted items")
			}
		case slices.Contains(FolderTriggers, t.Type):
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
			if d.Condition != nil {
				return dms.Invalid(at + "a condition tests items; folder triggers take collection_id and content_type_id")
			}
		case t.Type == TriggerSchedule:
			if t.ContentTypeID != "" {
				return dms.Invalid(at + "content_type_id belongs to item triggers")
			}
			if _, err := t.schedule(); err != nil {
				return dms.Invalid(at + err.Error())
			}
		case t.Type == TriggerManual:
			manual++
			if t.Cron != "" || t.TimeZone != "" || t.ContentTypeID != "" {
				return dms.Invalid(at + "manual triggers take only collection_id and selection")
			}
		case t.Type == TriggerCall:
			calls++
			if t.Cron != "" || t.TimeZone != "" || t.ContentTypeID != "" {
				return dms.Invalid(at + "call triggers take only collection_id")
			}
		case slices.Contains(tasks.Events, t.Type):
			if t.Cron != "" || t.TimeZone != "" || t.ContentTypeID != "" {
				return dms.Invalid(at + "task triggers take collection_id and task_key")
			}
			if d.Condition != nil {
				return dms.Invalid(at + "a condition tests items; filter task triggers with task_key")
			}
		case t.Type == dms.EventPermissionsChanged:
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
		case t.Type == dms.EventTermMerged || t.Type == dms.EventTermMoved || t.Type == dms.EventTermChanged:
			if t.CollectionID != "" || t.ContentTypeID != "" || t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + t.Type + " triggers take no options")
			}
		case eventPattern.MatchString(t.Type):
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
		case roleEventPattern.MatchString(t.Type):
			if roles[roleEventPattern.FindStringSubmatch(t.Type)[1]] == nil {
				return dms.Invalid(at + "unknown role in " + t.Type)
			}
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
		default:
			return dms.Invalid(at + "unknown trigger type " + t.Type)
		}
		if t.ContentTypeID != "" && t.CollectionID == "" {
			return dms.Invalid(at + "content_type_id needs collection_id")
		}
		if d.Condition != nil && t.CollectionID == "" {
			return dms.Invalid(at + "a condition needs collection_id on every trigger")
		}
	}
	if manual > 1 || calls > 1 {
		return dms.Invalid("a workflow has at most one manual trigger and one call trigger")
	}
	if d.InputSchema != nil {
		if manual == 0 && calls == 0 {
			return dms.Invalid("input_schema belongs to workflows with a manual or call trigger")
		}
		if err := forms.ValidateLaunch(d.InputSchema, "input_schema"); err != nil {
			return dms.Invalid(err.Error())
		}
		if _, hints := d.InputSchema["x-papergo-selection"]; hints && !d.selection() {
			return dms.Invalid("input_schema: x-papergo-selection belongs to selection workflows")
		}
	}
	switch d.Concurrency {
	case "", ConcurrencyParallel, ConcurrencySkip, ConcurrencyReplace:
	case ConcurrencyCoalesce:
		if d.selection() {
			return dms.Invalid("coalesce runs one item at a time; selection runs share several")
		}
	default:
		return dms.Invalid("concurrency is parallel, skip, replace or coalesce")
	}
	if d.OutputSchema != nil {
		if err := forms.Validate(d.OutputSchema, "output_schema"); err != nil {
			return dms.Invalid(err.Error())
		}
	}
	return nil
}

// selection reports whether the manual trigger starts one run per selection.
func (d Definition) selection() bool {
	for _, t := range d.Triggers {
		if t.Type == TriggerManual && t.Selection == SelectionAll {
			return true
		}
	}
	return false
}

// launchInputs applies the launch form's defaults to a manual start's
// values and checks them.
func (d Definition) launchInputs(values map[string]any) (map[string]any, error) {
	return forms.Values(d.InputSchema, values, "inputs")
}
