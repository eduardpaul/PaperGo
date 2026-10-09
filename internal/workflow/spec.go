// Package workflow is PaperGo's automation model: workflow definitions with
// triggers, a condition and a flow of activity nodes, their immutable
// versions, built-in workflows, and the interpreter that runs a version. It
// does not depend on the runner engine; internal/runner executes runs.
// docs/workflows.md is the user documentation.
package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"papergo/internal/dms"
)

// Trigger types. Item triggers react to committed item writes, schedule
// triggers to cron occurrences, manual triggers to starts through the API,
// and workflow events (wf.{key}.{event}) to other runs.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// ItemTriggers are the trigger types raised by item writes.
var ItemTriggers = []string{dms.EventItemCreated, dms.EventItemUpdated, dms.EventItemPublished, dms.EventItemUnpublished, dms.EventItemDeleted}

// Limits keep definitions and runs bounded.
const (
	MaxTriggers    = 10
	MaxNodes       = 100
	MaxDepth       = 5
	MaxNodeVisits  = 1000
	MaxOutputBytes = 64 * 1024
	MaxRetries     = 10
	MaxRetryDelay  = 24 * time.Hour
	MaxScheduled   = 500
)

// Definition is the content of a workflow version.
type Definition struct {
	Triggers  []Trigger       `json:"triggers"`
	Condition *dms.FilterExpr `json:"condition,omitempty"`
	// InputSchema is the launch form of manual starts (see forms.go).
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Variables   map[string]any `json:"variables,omitempty"`
	Flow        Flow           `json:"flow"`
}

// Trigger starts runs. CollectionID limits item and workflow-event triggers
// to one list or library, and makes a schedule start one run per item of
// that collection that meets the condition on Surface (head by default).
type Trigger struct {
	Type          string `json:"type"`
	CollectionID  string `json:"collection_id,omitempty"`
	ContentTypeID string `json:"content_type_id,omitempty"`
	Cron          string `json:"cron,omitempty"`
	TimeZone      string `json:"time_zone,omitempty"`
	Surface       string `json:"surface,omitempty"`
	// Selection, on a manual trigger with collection_id, is per_item (one
	// run per chosen item, the default) or selection (one run for all of
	// them, in order).
	Selection string `json:"selection,omitempty"`
	// Terms, on item.created, item.updated and item.published triggers,
	// limits them to revisions whose term and keywords fields hold one of
	// these terms (or a descendant, or a term merged into one). TermChange
	// is present (the default), added (held now, not by the revision before)
	// or removed (held before, not now).
	Terms      []string `json:"terms,omitempty"`
	TermChange string   `json:"term_change,omitempty"`
}

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

// Flow is a graph of nodes; a run starts at Start and follows the port each
// node's outcome selects.
type Flow struct {
	Start string          `json:"start"`
	Nodes map[string]Node `json:"nodes"`
}

// Node runs one activity. Next maps outcome ports to node names.
type Node struct {
	Activity string            `json:"activity"`
	Inputs   map[string]any    `json:"inputs,omitempty"`
	Next     map[string]string `json:"next,omitempty"`
	Retry    *Retry            `json:"retry,omitempty"`
}

// Retry runs a failed node again after Delay, up to Attempts times in all.
type Retry struct {
	Attempts int    `json:"attempts"`
	Delay    string `json:"delay,omitempty"`
}

// Ports every node has besides its activity's outcomes.
const (
	PortDone  = "done"
	PortError = "error"
)

var (
	keyPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	eventPattern     = regexp.MustCompile(`^wf\.([a-z0-9][a-z0-9_-]*)\.([a-z0-9][a-z0-9_-]*)$`)
	nodeNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,99}$`)
	inputNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	cronParser       = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
)

// WorkflowEvent is the event type a run of workflow key raises.
func WorkflowEvent(key, event string) string { return "wf." + key + "." + event }

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
	spec := t.Cron
	if t.TimeZone != "" {
		if _, err := time.LoadLocation(t.TimeZone); err != nil {
			return nil, dms.Invalid("unknown time_zone " + t.TimeZone)
		}
		spec = "CRON_TZ=" + t.TimeZone + " " + spec
	}
	s, err := cronParser.Parse(spec)
	if err != nil {
		return nil, dms.Invalid(fmt.Sprintf("invalid cron %q: %v", t.Cron, err))
	}
	return s, nil
}

// validate checks everything that needs no database: shapes, limits, the
// flow graph and each node's inputs. References to collections, content
// types and fields are checked by the store.
func (d Definition) validate() error {
	if len(d.Triggers) == 0 || len(d.Triggers) > MaxTriggers {
		return dms.Invalid(fmt.Sprintf("a workflow needs 1 to %d triggers", MaxTriggers))
	}
	manual := 0
	for i, t := range d.Triggers {
		at := fmt.Sprintf("triggers[%d]: ", i)
		if t.Surface != "" && (t.Type != TriggerSchedule || t.CollectionID == "" || t.Surface != "head" && t.Surface != "published") {
			return dms.Invalid(at + "surface is head or published, on schedule triggers with collection_id")
		}
		if t.Selection != "" && (t.Type != TriggerManual || t.Selection != SelectionPerItem && t.Selection != SelectionAll) {
			return dms.Invalid(at + "selection is per_item or selection, on manual triggers")
		}
		if t.Selection == SelectionAll && t.CollectionID == "" {
			return dms.Invalid(at + "selection runs need collection_id: the items come from one list or library")
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
		switch {
		case isItemTrigger(t.Type):
			if t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "cron and time_zone belong to schedule triggers")
			}
			if t.Type == dms.EventItemDeleted && d.Condition != nil {
				return dms.Invalid(at + "a condition cannot test deleted items")
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
		case t.Type == dms.EventTermMerged:
			if t.CollectionID != "" || t.ContentTypeID != "" || t.Cron != "" || t.TimeZone != "" {
				return dms.Invalid(at + "term.merged triggers take no options")
			}
		case eventPattern.MatchString(t.Type):
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
	if manual > 1 {
		return dms.Invalid("a workflow has at most one manual trigger")
	}
	if d.InputSchema != nil {
		if manual == 0 {
			return dms.Invalid("input_schema belongs to workflows with a manual trigger")
		}
		if err := validateSchema(d.InputSchema, "input_schema", 0, true); err != nil {
			return dms.Invalid(err.Error())
		}
		if _, hints := d.InputSchema["x-papergo-selection"]; hints && !d.selection() {
			return dms.Invalid("input_schema: x-papergo-selection belongs to selection workflows")
		}
	}
	for name := range d.Variables {
		if !inputNamePattern.MatchString(name) {
			return dms.Invalid("invalid variable name " + name)
		}
	}
	return d.Flow.validate()
}

func (f Flow) validate() error {
	if len(f.Nodes) == 0 || len(f.Nodes) > MaxNodes {
		return dms.Invalid(fmt.Sprintf("a flow needs 1 to %d nodes", MaxNodes))
	}
	if _, ok := f.Nodes[f.Start]; !ok {
		return dms.Invalid("flow start must name a node")
	}
	for name, n := range f.Nodes {
		at := "node " + name + ": "
		if !nodeNamePattern.MatchString(name) {
			return dms.Invalid(at + "names use letters, digits, spaces, '_' and '-' (at most 100)")
		}
		a := activities[n.Activity]
		if a == nil {
			return dms.Invalid(at + "unknown activity " + n.Activity)
		}
		ports := map[string]bool{PortDone: true, PortError: true}
		for _, p := range a.Ports {
			ports[p] = true
		}
		for port, target := range n.Next {
			if !ports[port] {
				return dms.Invalid(at + "activity " + n.Activity + " has no port " + port)
			}
			if _, ok := f.Nodes[target]; !ok {
				return dms.Invalid(at + "port " + port + " leads to unknown node " + target)
			}
		}
		if a.Terminal && len(n.Next) > 0 {
			return dms.Invalid(at + n.Activity + " ends the run and has no next nodes")
		}
		if n.Retry != nil {
			if n.Retry.Attempts < 1 || n.Retry.Attempts > MaxRetries {
				return dms.Invalid(fmt.Sprintf("%sretry attempts must be 1 to %d", at, MaxRetries))
			}
			if n.Retry.Delay != "" {
				d, err := time.ParseDuration(n.Retry.Delay)
				if err != nil || d < 0 || d > MaxRetryDelay {
					return dms.Invalid(at + "retry delay must be a duration up to 24h")
				}
			}
		}
		if err := a.Validate(n.Inputs); err != nil {
			return dms.Invalid(at + err.Error())
		}
	}
	reached := map[string]bool{}
	for queue := []string{f.Start}; len(queue) > 0; queue = queue[1:] {
		name := queue[0]
		if reached[name] {
			continue
		}
		reached[name] = true
		for _, next := range f.Nodes[name].Next {
			queue = append(queue, next)
		}
	}
	if len(reached) != len(f.Nodes) {
		for name := range f.Nodes {
			if !reached[name] {
				return dms.Invalid("node " + name + " cannot be reached from start")
			}
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
	return formValues(d.InputSchema, values, "inputs")
}
