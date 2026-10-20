package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"papergo/internal/dal/resources"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"papergo/internal/dal/dberr"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

// Service manages workflow definitions on top of the DMS: the same writer,
// authorization and audit trail as every other change.
type Service struct {
	DMS *dms.Service
	// Tools are what activities with heavy work use outside transactions.
	Tools Tools
}

// Workflow is a workflow with its current definition.
type Workflow struct {
	ExecutionAuthor   string         `json:"execution_author"`
	ID                string         `json:"id"`
	WorkspaceID       string         `json:"workspace_id"`
	Key               string         `json:"key"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	AutomaticEnabled  bool           `json:"automatic_enabled"`
	ManualEnabled     bool           `json:"manual_enabled"`
	AutomaticProvider bool           `json:"automatic_provider"`
	State             string         `json:"state"`
	BuiltInKey        *string        `json:"builtin_key,omitempty"`
	Role              *string        `json:"role,omitempty"`
	CollectionID      *string        `json:"collection_id,omitempty"`
	Parameters        map[string]any `json:"parameters,omitempty"`
	CurrentVersion    int            `json:"current_version"`
	Definition        Definition     `json:"definition"`
	CreatedBy         string         `json:"created_by"`
	UpdatedBy         string         `json:"updated_by"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	Version           int            `json:"version"`
}

// Version is one immutable definition of a workflow.
type Version struct {
	Number     int        `json:"number"`
	Definition Definition `json:"definition"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Save creates or replaces a workflow. Key is optional on create (it is made
// from the name) and immutable afterwards.
type Save struct {
	Key              string          `json:"key,omitempty"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	AutomaticEnabled *bool           `json:"automatic_enabled,omitempty"`
	ManualEnabled    *bool           `json:"manual_enabled,omitempty"`
	State            string          `json:"state,omitempty"`
	Definition       json.RawMessage `json:"definition"`
	// Role makes the workflow a filler of that role (see Role), in the
	// collection (CollectionID). Both are set when
	// the workflow is created.
	Role         string `json:"role,omitempty"`
	CollectionID string `json:"collection_id,omitempty"`
}

var nonKey = regexp.MustCompile(`[^a-z0-9]+`)

func keyFromName(name string) string {
	k := strings.Trim(nonKey.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(k) > 90 {
		k = strings.Trim(k[:90], "-")
	}
	if k == "" {
		k = "workflow"
	}
	return k
}

func validName(name string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 100 {
		return dms.Invalid("name needs 1 to 100 characters")
	}
	return nil
}

func toWorkflow(w *workflows.Workflow, v *workflows.Version) (Workflow, error) {
	def, err := ParseDefinition(v.Definition)
	if err != nil {
		return Workflow{}, err
	}
	out := Workflow{ExecutionAuthor: v.CreatedBy, ID: w.ID, WorkspaceID: w.WorkspaceID, Key: w.Key, Name: w.Name, Description: w.Description, AutomaticEnabled: w.AutomaticEnabled, ManualEnabled: w.ManualEnabled, State: string(w.State), AutomaticProvider: w.AutomaticProvider, BuiltInKey: optional(w.BuiltinKey), Role: optional(w.Role), CollectionID: optional(w.CollectionID),
		CurrentVersion: w.CurrentVersion, Definition: def, CreatedBy: w.CreatedBy, UpdatedBy: w.UpdatedBy, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Version: w.Version}
	if w.BuiltinKey != "" {
		out.Parameters = w.Parameters
	}
	return out, nil
}

func current(ctx context.Context, t *dms.Service, w *workflows.Workflow) (*workflows.Version, error) {
	return findVersion(ctx, t, w.ID, int64(w.CurrentVersion))
}

// live loads a workflow that is not deleted.
func live(ctx context.Context, t *dms.Service, id string) (*workflows.Workflow, error) {
	w, err := findWorkflow(ctx, t, id)
	if dberr.IsNotFound(err) || err == nil && w.DeletedAt != nil {
		return nil, dms.ErrNotFound
	}
	return w, err
}

// List returns the workspace's workflows; workspace readers may list them.
func (s *Service) List(ctx context.Context, subject, workspaceID string) ([]Workflow, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Workflow, error) {
		if err := workspace(ctx, t, subject, workspaceID, "read"); err != nil {
			return nil, err
		}
		rows, err := listWorkflows(ctx, t, workflows.Filter{WorkspaceID: workspaceID, LiveOnly: true, Order: "name"})
		if err != nil {
			return nil, err
		}
		out := make([]Workflow, 0, len(rows))
		for _, w := range rows {
			v, err := current(ctx, t, w)
			if err != nil {
				return nil, err
			}
			item, err := toWorkflow(w, v)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	})
}

// Get returns one workflow to workspace readers.
func (s *Service) Get(ctx context.Context, subject, id string) (Workflow, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Workflow, error) {
		w, err := live(ctx, t, id)
		if err != nil {
			return Workflow{}, err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "read"); err != nil {
			return Workflow{}, err
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return Workflow{}, err
		}
		return toWorkflow(w, v)
	})
}

// Versions returns every version of a workflow, newest first, to managers.
func (s *Service) Versions(ctx context.Context, subject, id string) ([]Version, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Version, error) {
		w, err := live(ctx, t, id)
		if err != nil {
			return nil, err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "manage"); err != nil {
			return nil, err
		}
		rows, err := t.DAL.Workflows().Versions(ctx, id)
		if err != nil {
			return nil, err
		}
		out := make([]Version, 0, len(rows))
		for _, v := range rows {
			def, err := ParseDefinition(v.Definition)
			if err != nil {
				return nil, err
			}
			out = append(out, Version{Number: v.Number, Definition: def, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt})
		}
		return out, nil
	})
}

func workspace(ctx context.Context, t *dms.Service, subject, id, action string) error {
	r, err := t.Authorize(ctx, subject, id, action)
	if err != nil {
		return err
	}
	if r.Kind != "workspace" {
		return dms.Invalid("workflows belong to a workspace")
	}
	return nil
}

// Create adds a workflow to a workspace; workspace managers may.
func (s *Service) Create(ctx context.Context, subject, workspaceID string, in Save) (out Workflow, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		out, err = s.create(ctx, t, subject, workspaceID, in)
		return err
	})
	return
}

func (s *Service) create(ctx context.Context, t *dms.Service, subject, workspaceID string, in Save) (out Workflow, err error) {
	err = func() error {
		if err := workspace(ctx, t, subject, workspaceID, "manage"); err != nil {
			return err
		}
		if err := validName(in.Name); err != nil {
			return err
		}
		def, raw, err := checkDefinition(ctx, t, subject, workspaceID, in.Definition)
		if err != nil {
			return err
		}
		if err = checkRole(ctx, t, subject, workspaceID, in.Role, in.CollectionID, def); err != nil {
			return err
		}
		key := in.Key
		if key != "" {
			if !keyPattern.MatchString(key) || len(key) > 100 {
				return dms.Invalid("key must be lowercase letters, digits, '-' or '_' (at most 100)")
			}
		} else if key, err = freeKey(ctx, t, workspaceID, keyFromName(in.Name)); err != nil {
			return err
		}
		state := workflows.State(in.State)
		if state == "" {
			state = workflows.StatePublished
		}
		if err = validState(state); err != nil {
			return err
		}
		if state == workflows.StatePublished && in.AutomaticEnabled != nil && *in.AutomaticEnabled {
			if in.Role != "" && in.CollectionID == "" {
				return dms.Invalid("An automatic role provider requires a specific list or library.")
			}
			if err = automaticScope(def); err != nil {
				return err
			}
		}

		w, err := createWorkflow(ctx, t, workflows.CreateInput{WorkspaceID: workspaceID, Key: key, Name: in.Name, Description: in.Description, AutomaticEnabled: in.AutomaticEnabled != nil && *in.AutomaticEnabled, ManualEnabled: in.ManualEnabled == nil || *in.ManualEnabled, State: state, Role: in.Role, CollectionID: in.CollectionID, AutomaticProvider: in.Role != "" && in.AutomaticEnabled != nil && *in.AutomaticEnabled && state == workflows.StatePublished, Definition: raw, CreatedBy: subject})
		if err != nil {
			return err
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return err
		}
		if err = index(ctx, t, w, def); err != nil {
			return err
		}
		if err = claimRole(ctx, t, subject, w); err != nil {
			return err
		}
		if out, err = toWorkflow(w, v); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.create", w, map[string]any{"workflow_id": w.ID, "key": w.Key, "version": 1})
	}()
	return
}

func freeKey(ctx context.Context, t *dms.Service, workspaceID, base string) (string, error) {
	key := base
	for n := 2; ; n++ {
		taken, err := t.DAL.Workflows().Exists(ctx, workflows.Filter{WorkspaceID: workspaceID, Key: key, LiveOnly: true})
		if err != nil || !taken {
			return key, err
		}
		key = fmt.Sprintf("%s-%d", base, n)
	}
}

func audit(ctx context.Context, t *dms.Service, subject, action string, w *workflows.Workflow, details map[string]any) error {
	return t.RecordEntity(ctx, subject, action, dms.AuditTarget{WorkspaceID: w.WorkspaceID, ResourceID: w.WorkspaceID, Kind: "workspace"}, details)
}

// check validates a definition, including the collections, content types
// and condition it references, and returns it with its normalized JSON.
func checkDefinition(ctx context.Context, t *dms.Service, subject, workspaceID string, raw json.RawMessage) (Definition, json.RawMessage, error) {
	if len(raw) == 0 {
		return Definition{}, nil, dms.Invalid("definition is required")
	}
	def, err := ParseDefinition(raw)
	if err != nil {
		return def, nil, err
	}
	if err = def.validate(); err != nil {
		return def, nil, err
	}
	checked := map[string]bool{}
	for i, tr := range def.Triggers {
		at := fmt.Sprintf("triggers[%d]: ", i)
		if tr.Type == dms.EventTermChanged {
			if err := t.RequireScope(ctx, subject, "taxonomy.read"); err != nil {
				return def, nil, fmt.Errorf("%sterm.changed requires taxonomy.read: %w", at, err)
			}
		}
		if tr.CollectionID != "" && !checked[tr.CollectionID] {
			c, err := t.Authorize(ctx, subject, tr.CollectionID, "read")
			if err != nil {
				return def, nil, dms.Invalid(at + "collection_id must be a readable list or library")
			}
			if c.WorkspaceID != workspaceID || (c.Kind != "list" && c.Kind != "library") {
				return def, nil, dms.Invalid(at + "collection_id must be a list or library of the workflow's workspace")
			}
			if def.Condition != nil {
				// Compiling the condition checks its fields exist and are indexed.
				if _, err = t.Query(ctx, subject, tr.CollectionID, dms.QueryRequest{Query: dms.QuerySpec{Filter: def.Condition}, Surface: "head", Limit: 1}); err != nil {
					return def, nil, dms.Invalid(at + "condition: " + err.Error())
				}
			}
			checked[tr.CollectionID] = true
		}
		if tr.ContentTypeID != "" {
			ok, err := t.DAL.Catalog().Accepts(ctx, tr.CollectionID, tr.ContentTypeID)
			if err != nil {
				return def, nil, err
			}
			if !ok {
				return def, nil, dms.Invalid(at + "content_type_id must belong to collection_id")
			}
		}
		for _, id := range tr.Terms {
			ok, err := t.DAL.Taxonomy().TermExists(ctx, id)
			if err != nil {
				return def, nil, err
			}
			if !ok {
				return def, nil, dms.Invalid(at + "terms must exist in the organization term store")
			}
		}
		if m := eventPattern.FindStringSubmatch(tr.Type); m != nil && m[2] != "completed" && m[2] != "failed" {
			// Raised by event.raise in the named workflow; nothing to check here,
			// that workflow may be created later.
			continue
		}
	}
	if def.InputSchema != nil {
		if err = forms.CheckConfig(ctx, t, subject, workspaceID, def.InputSchema, "input_schema"); err != nil {
			return def, nil, err
		}
	}
	for name, node := range def.Flow.Nodes {
		if node.Activity == "taxonomy.decide" {
			if err = checkStaticTaxonomyDecision(ctx, t, subject, node.Inputs); err != nil {
				return def, nil, fmt.Errorf("flow.nodes.%s: %w", name, err)
			}
		}
		if schema, ok := node.Inputs["input_schema"].(map[string]any); ok && (node.Activity == "task" || node.Activity == "approval") {
			if err = forms.CheckConfig(ctx, t, subject, workspaceID, schema, "flow.nodes."+name+".inputs.input_schema"); err != nil {
				return def, nil, err
			}
		}
	}
	normalized, err := json.Marshal(def)
	return def, normalized, err
}

// Update replaces a workflow's name, description, enabled state and, when it
// changed, its definition as a new version. Built-in workflows change only
// through their parameters.
func (s *Service) Update(ctx context.Context, subject, id string, version int, in Save) (out Workflow, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		w, err := live(ctx, t, id)
		if err != nil {
			return err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "manage"); err != nil {
			return err
		}
		if w.Version != version {
			return dms.ErrPreconditionFailed
		}
		if w.BuiltinKey != "" {
			return dms.Invalid("built-in workflows change through their parameters; copy one to edit its definition")
		}
		if in.Key != "" && in.Key != w.Key {
			return dms.Invalid("key is immutable")
		}
		if in.Role != "" && (w.Role == "" || in.Role != w.Role) || in.CollectionID != "" && (w.CollectionID == "" || in.CollectionID != w.CollectionID) {
			return dms.Invalid("role and collection_id are set when a workflow is created")
		}
		if err = validName(in.Name); err != nil {
			return err
		}
		def, raw, err := checkDefinition(ctx, t, subject, w.WorkspaceID, in.Definition)
		if err != nil {
			return err
		}
		if w.Role != "" {
			if err = roles[w.Role].fills(def); err != nil {
				return err
			}
		}
		enabled := w.AutomaticEnabled
		if in.AutomaticEnabled != nil {
			enabled = *in.AutomaticEnabled
		}
		nextState := w.State
		if in.State != "" {
			nextState = workflows.State(in.State)
		}
		if err = validState(nextState); err != nil {
			return err
		}
		if enabled && nextState == "published" {
			if w.Role != "" && w.CollectionID == "" {
				return dms.Invalid("An automatic role provider requires a specific list or library.")
			}
			if err := automaticScope(def); err != nil {
				return err
			}
		}
		w, v, err := revise(ctx, t, subject, w, raw, func(u *workflows.Changes) {
			u.Name, u.Description, u.AutomaticEnabled = &in.Name, &in.Description, &enabled
			if enabled && nextState == "published" && w.Role != "" {
				yes := true
				u.AutomaticProvider = &yes
			}
			if in.ManualEnabled != nil {
				u.ManualEnabled = in.ManualEnabled
			}
			if in.State != "" {
				state := workflows.State(in.State)
				u.State = &state
			}
		})
		if err != nil {
			return err
		}
		if err = index(ctx, t, w, def); err != nil {
			return err
		}
		if err = claimRole(ctx, t, subject, w); err != nil {
			return err
		}
		if out, err = toWorkflow(w, v); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.update", w, map[string]any{"workflow_id": w.ID, "version": w.CurrentVersion, "automatic_enabled": w.AutomaticEnabled})
	})
	return
}

// revise applies change to w and adds a version when raw differs from the
// current definition.
func revise(ctx context.Context, t *dms.Service, subject string, w *workflows.Workflow, raw json.RawMessage, change func(*workflows.Changes)) (*workflows.Workflow, *workflows.Version, error) {
	changes := workflows.Changes{}
	change(&changes)
	out, version, err := t.DAL.Workflows().Revise(ctx, w.ID, w.Version, subject, raw, changes)
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return nil, nil, dms.ErrPreconditionFailed
	}
	if err != nil {
		return nil, nil, err
	}
	return &out, &version, nil
}

// Delete removes a workflow; its versions and runs stay for history, and it
// starts no new runs. Running runs finish with their version.
func (s *Service) Delete(ctx context.Context, subject, id string, version int) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		w, err := live(ctx, t, id)
		if err != nil {
			return err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "manage"); err != nil {
			return err
		}
		if w.Version != version {
			return dms.ErrPreconditionFailed
		}
		if err := t.DAL.Workflows().Delete(ctx, id, version, subject); err != nil {
			if errors.Is(err, dberr.ErrConcurrentUpdate) {
				return dms.ErrPreconditionFailed
			}
			return err
		}
		return audit(ctx, t, subject, "workflow.delete", w, map[string]any{"workflow_id": id})
	})
}

// index replaces the trigger index rows of w: one per trigger of its current
// definition while it is enabled. A schedule's next occurrence counts from
// now, so a change never starts runs for times already past.
func index(ctx context.Context, t *dms.Service, w *workflows.Workflow, def Definition) error {
	var inputs []workflows.TriggerInput
	if w.AutomaticEnabled && w.State == workflows.StatePublished && w.DeletedAt == nil && !(w.Role != "" && w.CollectionID != "" && !w.AutomaticProvider) {
		now := time.Now().UTC()
		for i, tr := range def.Triggers {
			if tr.Type == TriggerManual || tr.Disabled {
				continue
			}
			entry := workflows.TriggerInput{CollectionID: tr.CollectionID, Type: tr.Type, Position: i}
			if tr.Type == TriggerSchedule {
				sched, err := tr.schedule()
				if err != nil {
					return err
				}
				next := sched.Next(now).UTC()
				entry.NextAt = &next
			}
			inputs = append(inputs, entry)
		}
	}
	return t.DAL.Workflows().ReplaceTriggers(ctx, w.ID, inputs)
}

// RunID is the runner's ID of the run of workflowID for eventID.
func RunID(workflowID, eventID string) string { return "wf:" + workflowID + ":" + eventID }

// Start asks for one manual run, with the trigger's standalone, single-item or selection association.
// A trigger with selection mode starts one run for all items, in their
// order; PrimaryItemID (default: the first) is that run's item.
type Start struct {
	WorkflowVersion int            `json:"workflow_version,omitempty"`
	ItemIDs         []string       `json:"item_ids,omitempty"`
	PrimaryItemID   string         `json:"primary_item_id,omitempty"`
	Inputs          map[string]any `json:"inputs,omitempty"`
}

// MaxStartItems bounds one manual start, and one selection.
const MaxStartItems = 100

// StartRuns starts a workflow by hand and returns the run IDs. Starting on
// items needs write access to each item; starting without an item needs
// manage access to the workspace. Everything is checked before any run
// starts. The start is logged as events with the request's transaction; the
// dispatcher starts the runs moments later.
func (s *Service) StartRuns(ctx context.Context, subject, id string, in Start) (runs []string, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		events, e := s.planStart(ctx, t, subject, id, in, true)
		if e != nil {
			return e
		}
		for _, event := range events {
			t.Emit(ctx, event)
			runs = append(runs, RunID(id, event.ID))
		}
		return nil
	})
	return
}

func (s *Service) planStart(ctx context.Context, t *dms.Service, subject, id string, in Start, checkInputs bool) (events []dms.Event, err error) {
	err = func() error {
		w, err := live(ctx, t, id)
		if err != nil {
			return err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "read"); err != nil {
			return err
		}
		if err = t.RequireScope(ctx, subject, "list.write"); err != nil {
			return err
		}
		if !w.ManualEnabled || w.State != workflows.StatePublished {
			return dms.Invalid("manual use requires a published workflow with manual runs allowed")
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return err
		}
		if in.WorkflowVersion > 0 && in.WorkflowVersion != v.Number {
			return dms.ErrPreconditionFailed
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return err
		}
		var manual *Trigger
		for i := range def.Triggers {
			if def.Triggers[i].Type == TriggerManual {
				manual = &def.Triggers[i]
			}
		}
		if manual == nil {
			return dms.Invalid("the workflow has no manual trigger")
		}
		inputs := in.Inputs
		if checkInputs {
			inputs, err = def.launchInputs(in.Inputs)
			if err != nil {
				return err
			}
			if def.InputSchema != nil {
				if err = forms.CheckValues(ctx, t, subject, w.WorkspaceID, def.InputSchema, inputs, "inputs"); err != nil {
					return err
				}
			}
		}
		// Duplicates are dropped; the first occurrence keeps its place.
		items := make([]string, 0, len(in.ItemIDs))
		seen := map[string]bool{}
		for _, itemID := range in.ItemIDs {
			if !seen[itemID] {
				seen[itemID] = true
				items = append(items, itemID)
			}
		}
		if len(items) > MaxStartItems {
			return dms.Invalid(fmt.Sprintf("start at most %d items at once", MaxStartItems))
		}
		selection := manual.Selection == SelectionAll
		itemBound := manual.CollectionID != "" || manual.Selection != ""
		if !itemBound && len(items) != 0 {
			return dms.Invalid("standalone workflows do not accept item_ids")
		}
		if itemBound && !selection && len(items) != 1 {
			return issue("selection_required", "Choose exactly one item for this workflow.")
		}
		if in.PrimaryItemID != "" && (!selection || !seen[in.PrimaryItemID]) {
			return dms.Invalid("primary_item_id must be one of item_ids, on a selection workflow")
		}
		data := map[string]any{"workflow_id": w.ID, "workflow_version": v.Number, "inputs": inputs}
		if len(items) == 0 {
			if itemBound {
				return issue("selection_required", "Choose items before running this workflow.")
			}
			if err = workspace(ctx, t, subject, w.WorkspaceID, "manage"); err != nil {
				return err
			}
			if err = s.manualReadiness(ctx, t, w, v.CreatedBy, def, nil); err != nil {
				return err
			}
			e := dms.Event{ID: dms.NewEventID(), Type: TriggerManual, WorkspaceID: w.WorkspaceID, Actor: subject, Data: data}
			events = append(events, e)
			return nil
		}
		collection := ""
		collections := map[string]string{}
		for _, itemID := range items {
			r, err := t.Authorize(ctx, subject, itemID, "write")
			if err != nil {
				return err
			}
			if r.Kind != "item" || r.WorkspaceID != w.WorkspaceID {
				return issue("wrong_workspace", "Choose items from this workflow's workspace.")
			}
			if manual.CollectionID != "" && (r.ContainerID == nil || *r.ContainerID != manual.CollectionID) {
				return issue("wrong_collection", "Choose items from this workflow's library or list.")
			}
			collection = *r.ContainerID
			collections[itemID] = collection
			if def.Condition != nil {
				ok, err := matches(ctx, t, v.CreatedBy, collection, itemID, def.Condition)
				if err != nil {
					return err
				}
				if !ok {
					return dms.Invalid("item " + itemID + " does not meet the workflow's condition")
				}
			}
			if !selection {
				e := dms.Event{ID: dms.NewEventID(), Type: TriggerManual, WorkspaceID: w.WorkspaceID, CollectionID: collection, ResourceID: itemID, Actor: subject, Data: data}
				events = append(events, e)
			}
		}
		if err = s.manualReadiness(ctx, t, w, v.CreatedBy, def, items); err != nil {
			return err
		}
		if selection {
			primary := in.PrimaryItemID
			if primary == "" {
				primary = items[0]
			}
			members := make([]any, len(items))
			for i, itemID := range items {
				members[i] = itemID
			}
			data["items"] = members
			e := dms.Event{ID: dms.NewEventID(), Type: TriggerManual, WorkspaceID: w.WorkspaceID, CollectionID: collections[primary], ResourceID: primary, Actor: subject, Data: data}
			events = append(events, e)
		}
		return nil
	}()
	return
}

// Match finds the runs that logged events start, records them, and returns
// their inputs for the runner to enqueue in the dispatch transaction. The
// dispatcher calls it for undispatched events, on whichever node claims
// them. Events at MaxDepth or deeper start nothing, which stops workflows
// that keep triggering each other. A condition that cannot be evaluated
// (a field no longer indexed, the author lost access) still starts the run,
// which then fails with the reason, so the problem is visible.
func Match(ctx context.Context, t *dms.Service, events []dms.Event, active ActiveFunc) ([]RunInput, []string, error) {
	var out []RunInput
	var cancel []string
	// started are the runs this call starts, with their members, by workflow.
	type startedRun struct {
		id      string
		members []string
	}
	started := map[string][]startedRun{}
	versions := map[string]*matchable{}
	// A bulk write raises many events of few kinds; look each kind up once.
	lookups := map[[3]string][]*workflows.Workflow{}
	for _, e := range events {
		if e.Depth >= MaxDepth {
			continue
		}
		// Purge removes revision payloads. Older queued content events must not
		// block dispatch or start actions against an item that no longer exists.
		if e.ResourceID != "" && (e.Type == dms.EventItemCreated || e.Type == dms.EventItemUpdated || e.Type == dms.EventItemPublished || e.Type == dms.EventItemUnpublished || e.Type == dms.EventItemRestored || e.Type == dms.EventFileAdded || e.Type == dms.EventFileUpdated || e.Type == dms.EventPermissionsChanged ||
			e.Type == dms.EventFolderCreated || e.Type == dms.EventFolderUpdated || e.Type == dms.EventFolderRestored) {
			exists, err := t.DAL.Resources().Exists(ctx, resources.Filter{IDs: []string{e.ResourceID}})
			if err != nil {
				return nil, nil, err
			}
			if !exists {
				continue
			}
		}
		var candidates []*workflows.Workflow
		switch e.Type {
		case TriggerManual, TriggerSchedule:
			id, _ := e.Data["workflow_id"].(string)
			w, err := findWorkflow(ctx, t, id)
			if err != nil {
				return nil, nil, err
			}
			candidates = []*workflows.Workflow{w}
		default:
			key := [3]string{e.Type, e.WorkspaceID, e.CollectionID}
			if cached, ok := lookups[key]; ok {
				candidates = cached
				break
			}
			rows, err := t.DAL.Workflows().Triggers(ctx, workflows.TriggerFilter{Type: e.Type, WorkspaceID: e.WorkspaceID, CollectionID: e.CollectionID, MatchCollection: true})
			if err != nil {
				return nil, nil, err
			}
			ids := map[string]bool{}
			for _, r := range rows {
				ids[r.WorkflowID] = true
			}
			if len(ids) > 0 {
				list := make([]string, 0, len(ids))
				for id := range ids {
					list = append(list, id)
				}
				sort.Strings(list)
				if candidates, err = listWorkflows(ctx, t, workflows.Filter{IDs: list, AutomaticEnabled: boolPtr(true), State: workflows.StatePublished, LiveOnly: true}); err != nil {
					return nil, nil, err
				}
			}
			lookups[key] = candidates
		}
		for _, w := range candidates {
			if e.Type != TriggerManual && w.Role != "" && (w.CollectionID == "" || e.CollectionID != "" && w.CollectionID != e.CollectionID || !w.AutomaticProvider) {
				continue
			}
			versionKey := w.ID
			if e.Type == TriggerManual {
				versionKey += fmt.Sprint(e.Data["workflow_version"])
			}
			m := versions[versionKey]
			if m == nil {
				v, err := current(ctx, t, w)
				if e.Type == TriggerManual {
					number, ok := forms.Number(e.Data["workflow_version"])
					if !ok {
						return nil, nil, dms.Invalid("manual event requires its accepted workflow version")
					}
					v, err = findVersion(ctx, t, w.ID, int64(number))
				}
				if err != nil {
					return nil, nil, err
				}
				def, err := ParseDefinition(v.Definition)
				if err != nil {
					return nil, nil, err
				}
				m = &matchable{w: w, v: v, def: def}
				versions[versionKey] = m
			}
			ok, err := m.accepts(ctx, t, e)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			if e.Type == dms.EventTermChanged && !systemWorkflow(w.BuiltinKey) {
				if err := t.RequireScope(ctx, m.v.CreatedBy, "taxonomy.read"); errors.Is(err, dms.ErrForbidden) {
					continue
				} else if err != nil {
					return nil, nil, err
				}
			}
			startError := ""
			if m.def.Condition != nil && e.Type != TriggerManual && e.Type != TriggerSchedule {
				if e.ResourceID == "" || e.CollectionID == "" {
					continue
				}
				holds, err := matches(ctx, t, m.v.CreatedBy, e.CollectionID, e.ResourceID, m.def.Condition)
				if err != nil {
					startError = "condition could not be evaluated: " + err.Error()
				} else if !holds {
					continue
				}
			}
			members := eventMembers(e)
			if e.Type == TriggerManual {
				for _, id := range members {
					if _, err := t.Authorize(ctx, e.Actor, id, "write"); err != nil {
						startError = "Manual run is no longer authorized for the selected items."
						break
					}
				}
			}
			if c := m.def.Concurrency; c != "" && c != ConcurrencyParallel && len(members) > 0 {
				busy, err := activeRuns(ctx, t, w.ID, members, active)
				if err != nil {
					return nil, nil, err
				}
				delete(busy, RunID(w.ID, e.ID))
				// Runs this batch starts are not in the runner's statuses yet.
				for _, earlier := range started[w.ID] {
					if overlaps(earlier.members, members) {
						busy[earlier.id] = runstore.StatusQueued
					}
				}
				switch c {
				case ConcurrencySkip:
					if len(busy) > 0 {
						if e.Type != TriggerManual {
							continue
						}
						startError = "manual launch skipped because another run is active"
					}
				case ConcurrencyReplace:
					ids := make([]string, 0, len(busy))
					for id := range busy {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					cancel = append(cancel, ids...)
				case ConcurrencyCoalesce:
					// A queued run will read the item as it is when it starts.
					queued := false
					for _, state := range busy {
						queued = queued || state == runstore.StatusQueued
					}
					if queued && e.Type != TriggerManual {
						continue
					}
				}
			}
			runID := RunID(w.ID, e.ID)
			started[w.ID] = append(started[w.ID], startedRun{id: runID, members: members})
			exists, err := t.DAL.Runs().Exists(ctx, w.ID, e.ID)
			if err != nil {
				return nil, nil, err
			}
			if exists {
				continue
			}
			if _, err = t.DAL.Runs().Create(ctx, runstore.CreateInput{ID: runID, WorkflowID: w.ID, WorkflowVersion: m.v.Number, WorkspaceID: w.WorkspaceID, EventID: e.ID, EventType: e.Type, Depth: e.Depth, Actor: e.Actor, ItemID: eventItemID(e)}); err != nil {
				return nil, nil, err
			}
			in := RunInput{RunID: runID, WorkflowID: w.ID, Version: m.v.Number, WorkspaceID: w.WorkspaceID, ItemID: eventItemID(e), Event: e, StartError: startError, System: systemWorkflow(w.BuiltinKey)}
			if m.def.Concurrency == ConcurrencyCoalesce && len(members) > 0 {
				in.Partition = w.ID + ":" + members[0]
			}
			if e.Type == TriggerManual {
				if inputs, ok := e.Data["inputs"].(map[string]any); ok {
					in.Inputs = inputs
				}
				members, _ := e.Data["items"].([]any)
				for position, m := range members {
					itemID, _ := m.(string)
					in.Items = append(in.Items, itemID)
					if err = t.DAL.Runs().AddItem(ctx, runID, itemID, position); err != nil {
						return nil, nil, err
					}
				}
			}
			out = append(out, in)
		}
	}
	return out, cancel, nil
}

// ActiveFunc reports the status of the given runs that are still active:
// queued, running or waiting. Runs that are not active are left out. It
// reads in t's transaction.
type ActiveFunc func(ctx context.Context, t *dms.Service, runIDs []string) (map[string]runstore.Status, error)

// eventMembers are the items a run started by e works on.
func eventMembers(e dms.Event) []string {
	if e.Type == TriggerManual {
		if list, _ := e.Data["items"].([]any); len(list) > 0 {
			out := make([]string, 0, len(list))
			for _, v := range list {
				if id, _ := v.(string); id != "" {
					out = append(out, id)
				}
			}
			return out
		}
	}
	if itemID := eventItemID(e); itemID != "" {
		return []string{itemID}
	}
	return nil
}

func eventItemID(e dms.Event) string {
	if e.Type == dms.EventItemDeleted || e.Type == dms.EventItemPurged || e.Type == dms.EventFolderDeleted || e.Type == dms.EventFolderPurged {
		return ""
	}
	return e.ResourceID
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// activeRuns returns the queued, running or waiting runs of a workflow that
// work on any of items, as primary item or selection member. Recorded
// statuses lag behind the runner, so active confirms them.
func activeRuns(ctx context.Context, t *dms.Service, workflowID string, items []string, active ActiveFunc) (map[string]runstore.Status, error) {
	rows, err := t.DAL.Runs().List(ctx, runstore.Filter{WorkflowID: workflowID, Statuses: runstore.Active, ItemIDs: items})
	out := map[string]runstore.Status{}
	if err != nil || len(rows) == 0 {
		return out, err
	}
	if active == nil {
		for _, row := range rows {
			out[row.ID] = row.Status
		}
		return out, nil
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return active(ctx, t, ids)
}

type matchable struct {
	w   *workflows.Workflow
	v   *workflows.Version
	def Definition
}

// accepts reports whether any trigger of e's type accepts e.
func (m *matchable) accepts(ctx context.Context, t *dms.Service, e dms.Event) (bool, error) {
	if e.Type == TriggerManual || e.Type == TriggerSchedule {
		// Targeted events were checked when they were raised.
		return true, nil
	}
	accepted := false
	for _, tr := range m.def.Triggers {
		if tr.Disabled || tr.Type != e.Type || tr.CollectionID != "" && tr.CollectionID != e.CollectionID {
			continue
		}
		if tr.TaskKey != "" {
			if key, _ := e.Data["key"].(string); key != tr.TaskKey {
				continue
			}
		}
		if e.Type == dms.EventPermissionsChanged {
			if action, _ := e.Data["action"].(string); !tr.acceptsAction(action) {
				continue
			}
		}
		if e.Type == dms.EventFileAdded || e.Type == dms.EventFileUpdated {
			source, _ := e.Data["source"].(string)
			media, _ := e.Data["content_type"].(string)
			if !slices.Contains(tr.sources(), source) || !tr.acceptsMediaType(media) {
				continue
			}
		}
		if tr.ContentTypeID != "" {
			typ, _ := e.Data["content_type_id"].(string)
			if typ == "" && e.ResourceID != "" {
				r, err := t.DAL.Resources().Resource(ctx, e.ResourceID)
				if err != nil {
					return false, err
				}
				if r.ContentTypeID != nil {
					typ = *r.ContentTypeID
				}
			}
			if typ != tr.ContentTypeID {
				continue
			}
		}
		if len(tr.Terms) > 0 {
			held, err := termsChanged(ctx, t, tr, e)
			if err != nil {
				return false, err
			}
			if !held {
				continue
			}
		}
		accepted = true
		break
	}
	return accepted, nil
}

// termsChanged applies a trigger's terms filter to the revision of e.
func termsChanged(ctx context.Context, t *dms.Service, tr Trigger, e dms.Event) (bool, error) {
	revisionID, _ := e.Data["revision_id"].(string)
	if revisionID == "" {
		return false, nil
	}
	current, previous, err := t.RevisionTerms(ctx, revisionID)
	if err != nil {
		return false, err
	}
	if e.Data["relationship_id"] != nil {
		// A relationship write changes no metadata terms.
		previous = current
	}
	now, err := t.MatchTerms(ctx, tr.Terms, current)
	if err != nil {
		return false, err
	}
	if tr.TermChange == "" || tr.TermChange == TermPresent {
		return len(now) > 0, nil
	}
	before, err := t.MatchTerms(ctx, tr.Terms, previous)
	if err != nil {
		return false, err
	}
	for _, id := range tr.Terms {
		if tr.TermChange == TermAdded && now[id] && !before[id] || tr.TermChange == TermRemoved && !now[id] && before[id] {
			return true, nil
		}
	}
	return false, nil
}

// Tick raises the schedule events that are due at now and moves each
// schedule to its next occurrence. Occurrences missed while PaperGo was down
// start once. A schedule with a collection starts one run per item that
// meets the condition, up to MaxScheduled per occurrence. A schedule that
// cannot run (its condition no longer compiles, its author lost access)
// still moves on, so it never holds up the others; such errors come back
// as skipped for the runner to log, while err is a failure of the tick.
func Tick(ctx context.Context, t *dms.Service, now time.Time) (raised int, skipped, err error) {
	due, err := t.DAL.Workflows().Triggers(ctx, workflows.TriggerFilter{DueAt: &now, Limit: 100})
	if err != nil {
		return 0, nil, err
	}
	var failures []error
	for _, row := range due {
		events, next, err := occurrence(ctx, t, row, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("workflow %s schedule: %w", row.WorkflowID, err))
		}
		for _, e := range events {
			t.Emit(ctx, e)
		}
		raised += len(events)
		if err = t.DAL.Workflows().AdvanceTrigger(ctx, row.ID, next); err != nil {
			return raised, nil, err
		}
	}
	return raised, errors.Join(failures...), nil
}

// occurrence computes the events of one due schedule trigger and its next
// occurrence after now. On error it returns no events.
func occurrence(ctx context.Context, t *dms.Service, row workflows.Trigger, now time.Time) ([]dms.Event, time.Time, error) {
	retry := now.Add(time.Hour).UTC()
	w, err := live(ctx, t, row.WorkflowID)
	if err != nil {
		return nil, retry, err
	}
	v, err := current(ctx, t, w)
	if err != nil {
		return nil, retry, err
	}
	def, err := ParseDefinition(v.Definition)
	if err != nil {
		return nil, retry, err
	}
	if row.Position >= len(def.Triggers) {
		return nil, retry, errors.New("trigger index out of date")
	}
	tr := def.Triggers[row.Position]
	sched, err := tr.schedule()
	if err != nil {
		return nil, retry, err
	}
	next := sched.Next(now).UTC()
	at := row.NextAt.UTC()
	data := map[string]any{"workflow_id": w.ID, "occurrence": at.Format(time.RFC3339)}
	event := func(itemID string) dms.Event {
		sum := sha256.Sum256([]byte(row.ID + "|" + at.Format(time.RFC3339Nano) + "|" + itemID))
		return dms.Event{ID: hex.EncodeToString(sum[:16]), Type: TriggerSchedule, WorkspaceID: w.WorkspaceID, CollectionID: tr.CollectionID, ResourceID: itemID, Actor: v.CreatedBy, Data: data}
	}
	if tr.CollectionID == "" {
		return []dms.Event{event("")}, next, nil
	}
	surface := tr.Surface
	if surface == "" {
		surface = "head"
	}
	var events []dms.Event
	q := dms.QueryRequest{Query: dms.QuerySpec{Filter: def.Condition}, Surface: surface, Limit: 100}
	for len(events) < MaxScheduled {
		res, err := t.Query(ctx, v.CreatedBy, tr.CollectionID, q)
		if err != nil {
			return nil, next, err
		}
		for _, item := range res.Data {
			if len(events) < MaxScheduled {
				events = append(events, event(item.ID))
			}
		}
		if res.NextCursor == "" {
			break
		}
		q.After = res.NextCursor
	}
	return events, next, nil
}

// PermissionBatch is how many items and folders the dispatcher delivers a
// permissions.changed event to per batch. Tests lower it.
var PermissionBatch = 500

// Listens reports whether an enabled workflow of the root permissions.changed
// event's workspace may accept a delivery of it: only then is it expanded to
// the items and folders it affects.
func Listens(ctx context.Context, t *dms.Service, root dms.Event) (bool, error) {
	if root.Depth >= MaxDepth {
		return false, nil
	}
	rows, err := t.DAL.Workflows().Triggers(ctx, workflows.TriggerFilter{Type: dms.EventPermissionsChanged, WorkspaceID: root.WorkspaceID, CollectionID: root.CollectionID, MatchCollection: root.CollectionID != ""})
	if err != nil || len(rows) == 0 {
		return false, err
	}
	list := make([]string, len(rows))
	for i, r := range rows {
		list[i] = r.WorkflowID
	}
	ids, err := listWorkflows(ctx, t, workflows.Filter{IDs: list, AutomaticEnabled: boolPtr(true), State: workflows.StatePublished, LiveOnly: true})
	if err != nil {
		return false, err
	}
	action, _ := root.Data["action"].(string)
	for _, w := range ids {
		v, err := current(ctx, t, w)
		if err != nil {
			return false, err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return false, err
		}
		for _, tr := range def.Triggers {
			if tr.Type == dms.EventPermissionsChanged && tr.acceptsAction(action) {
				return true, nil
			}
		}
	}
	return false, nil
}

// PermissionDeliveries turns a root permissions.changed event into one event
// per affected item or folder, which runs get as their item. Each carries
// the change, the scope it moved between, and the root resource and event.
func PermissionDeliveries(root dms.Event, targets []dms.PermissionTarget) []dms.Event {
	data := map[string]any{"root_event_id": root.ID, "root_id": root.ResourceID, "root_kind": root.Data["resource_kind"], "action": root.Data["action"]}
	previous, current := root.Data["previous_scope_id"], root.Data["current_scope_id"]
	if p, ok := root.Data["previous"].(map[string]any); ok {
		previous = p["scope_id"]
	}
	if c, ok := root.Data["current"].(map[string]any); ok {
		current = c["scope_id"]
	}
	if previous != nil {
		data["previous_scope_id"] = previous
	}
	if current != nil {
		data["current_scope_id"] = current
	}
	for _, key := range []string{"member_subject", "previous_role", "role"} {
		if v, ok := root.Data[key]; ok {
			data[key] = v
		}
	}
	out := make([]dms.Event, len(targets))
	for i, target := range targets {
		d := make(map[string]any, len(data)+1)
		for k, v := range data {
			d[k] = v
		}
		d["resource_kind"] = target.Kind
		out[i] = dms.Event{ID: root.ID + ":" + target.ID, Type: root.Type, WorkspaceID: root.WorkspaceID, CollectionID: target.CollectionID, ResourceID: target.ID,
			Actor: root.Actor, Data: d, Depth: root.Depth, CauseRunID: root.CauseRunID}
	}
	return out
}

func boolPtr(value bool) *bool { return &value }

// findWorkflow loads a workflow, deleted or not.
func findWorkflow(ctx context.Context, t *dms.Service, id string) (*workflows.Workflow, error) {
	w, err := t.DAL.Workflows().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// findVersion loads a version of a workflow.
func findVersion(ctx context.Context, t *dms.Service, id string, number int64) (*workflows.Version, error) {
	v, err := t.DAL.Workflows().Version(ctx, id, number)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// listWorkflows loads the workflows a filter selects.
func listWorkflows(ctx context.Context, t *dms.Service, f workflows.Filter) ([]*workflows.Workflow, error) {
	rows, err := t.DAL.Workflows().List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]*workflows.Workflow, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// createWorkflow records a workflow with its first version.
func createWorkflow(ctx context.Context, t *dms.Service, in workflows.CreateInput) (*workflows.Workflow, error) {
	w, err := t.DAL.Workflows().Create(ctx, in)
	if err != nil {
		return nil, err
	}
	return &w, nil
}
