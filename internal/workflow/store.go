package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/resource"
	"papergo/ent/workflow"
	"papergo/ent/workflowrun"
	"papergo/ent/workflowtrigger"
	"papergo/ent/workflowversion"
	"papergo/internal/dms"
)

// Service manages workflow definitions on top of the DMS: the same writer,
// authorization and audit trail as every other change.
type Service struct {
	DMS *dms.Service
}

// Workflow is a workflow with its current definition.
type Workflow struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id"`
	Key            string         `json:"key"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Enabled        bool           `json:"enabled"`
	BuiltInKey     *string        `json:"builtin_key,omitempty"`
	CollectionID   *string        `json:"collection_id,omitempty"`
	Parameters     map[string]any `json:"parameters,omitempty"`
	CurrentVersion int            `json:"current_version"`
	Definition     Definition     `json:"definition"`
	CreatedBy      string         `json:"created_by"`
	UpdatedBy      string         `json:"updated_by"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	Version        int            `json:"version"`
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
	Key         string          `json:"key,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Definition  json.RawMessage `json:"definition"`
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

func toWorkflow(w *ent.Workflow, v *ent.WorkflowVersion) (Workflow, error) {
	def, err := ParseDefinition(v.Definition)
	if err != nil {
		return Workflow{}, err
	}
	out := Workflow{ID: w.ID, WorkspaceID: w.WorkspaceID, Key: w.Key, Name: w.Name, Description: w.Description, Enabled: w.Enabled, BuiltInKey: w.BuiltinKey, CollectionID: w.CollectionID,
		CurrentVersion: w.CurrentVersion, Definition: def, CreatedBy: w.CreatedBy, UpdatedBy: w.UpdatedBy, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, Version: w.Version}
	if w.BuiltinKey != nil {
		out.Parameters = w.Parameters
	}
	return out, nil
}

func current(ctx context.Context, t *dms.Service, w *ent.Workflow) (*ent.WorkflowVersion, error) {
	return t.Client.WorkflowVersion.Query().Where(workflowversion.WorkflowIDEQ(w.ID), workflowversion.NumberEQ(w.CurrentVersion)).Only(ctx)
}

// live loads a workflow that is not deleted.
func live(ctx context.Context, t *dms.Service, id string) (*ent.Workflow, error) {
	w, err := t.Client.Workflow.Get(ctx, id)
	if ent.IsNotFound(err) || err == nil && w.DeletedAt != nil {
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
		rows, err := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.DeletedAtIsNil()).Order(ent.Asc(workflow.FieldName)).All(ctx)
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
		rows, err := t.Client.WorkflowVersion.Query().Where(workflowversion.WorkflowIDEQ(id)).Order(ent.Desc(workflowversion.FieldNumber)).All(ctx)
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
	if r.Kind != resource.KindWorkspace {
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
		def, raw, err := s.check(ctx, t, subject, workspaceID, in.Definition)
		if err != nil {
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
		enabled := in.Enabled == nil || *in.Enabled
		w, err := t.Client.Workflow.Create().SetWorkspaceID(workspaceID).SetKey(key).SetName(in.Name).SetDescription(in.Description).SetEnabled(enabled).SetCurrentVersion(1).SetCreatedBy(subject).SetUpdatedBy(subject).Save(ctx)
		if err != nil {
			return err
		}
		v, err := t.Client.WorkflowVersion.Create().SetWorkflowID(w.ID).SetNumber(1).SetDefinition(raw).SetCreatedBy(subject).Save(ctx)
		if err != nil {
			return err
		}
		if err = index(ctx, t, w, def); err != nil {
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
		taken, err := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.KeyEQ(key), workflow.DeletedAtIsNil()).Exist(ctx)
		if err != nil || !taken {
			return key, err
		}
		key = fmt.Sprintf("%s-%d", base, n)
	}
}

func audit(ctx context.Context, t *dms.Service, subject, action string, w *ent.Workflow, details map[string]any) error {
	r, err := t.Client.Resource.Get(ctx, w.WorkspaceID)
	if err != nil {
		return err
	}
	return t.Record(ctx, subject, action, r, details)
}

// check validates a definition, including the collections, content types
// and condition it references, and returns it with its normalized JSON.
func (s *Service) check(ctx context.Context, t *dms.Service, subject, workspaceID string, raw json.RawMessage) (Definition, json.RawMessage, error) {
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
		if tr.CollectionID != "" && !checked[tr.CollectionID] {
			c, err := t.Authorize(ctx, subject, tr.CollectionID, "read")
			if err != nil {
				return def, nil, dms.Invalid(at + "collection_id must be a readable list or library")
			}
			if c.WorkspaceID != workspaceID || (c.Kind != resource.KindList && c.Kind != resource.KindLibrary) {
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
			ok, err := t.Client.ContentType.Query().Where(contenttype.IDEQ(tr.ContentTypeID), contenttype.ContainerIDEQ(tr.CollectionID)).Exist(ctx)
			if err != nil {
				return def, nil, err
			}
			if !ok {
				return def, nil, dms.Invalid(at + "content_type_id must belong to collection_id")
			}
		}
		if m := eventPattern.FindStringSubmatch(tr.Type); m != nil && m[2] != "completed" && m[2] != "failed" {
			// Raised by event.raise in the named workflow; nothing to check here,
			// that workflow may be created later.
			continue
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
			return dms.ErrConflict
		}
		if w.BuiltinKey != nil {
			return dms.Invalid("built-in workflows change through their parameters; copy one to edit its definition")
		}
		if in.Key != "" && in.Key != w.Key {
			return dms.Invalid("key is immutable")
		}
		if err = validName(in.Name); err != nil {
			return err
		}
		def, raw, err := s.check(ctx, t, subject, w.WorkspaceID, in.Definition)
		if err != nil {
			return err
		}
		enabled := w.Enabled
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		w, v, err := revise(ctx, t, subject, w, raw, func(u *ent.WorkflowUpdateOne) { u.SetName(in.Name).SetDescription(in.Description).SetEnabled(enabled) })
		if err != nil {
			return err
		}
		if err = index(ctx, t, w, def); err != nil {
			return err
		}
		if out, err = toWorkflow(w, v); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.update", w, map[string]any{"workflow_id": w.ID, "version": w.CurrentVersion, "enabled": w.Enabled})
	})
	return
}

// revise applies change to w and adds a version when raw differs from the
// current definition.
func revise(ctx context.Context, t *dms.Service, subject string, w *ent.Workflow, raw json.RawMessage, change func(*ent.WorkflowUpdateOne)) (*ent.Workflow, *ent.WorkflowVersion, error) {
	v, err := current(ctx, t, w)
	if err != nil {
		return nil, nil, err
	}
	u := t.Client.Workflow.UpdateOne(w).Where(workflow.VersionEQ(w.Version)).AddVersion(1).SetUpdatedBy(subject).SetUpdatedAt(time.Now().UTC())
	change(u)
	if string(v.Definition) != string(raw) {
		if v, err = t.Client.WorkflowVersion.Create().SetWorkflowID(w.ID).SetNumber(w.CurrentVersion + 1).SetDefinition(raw).SetCreatedBy(subject).Save(ctx); err != nil {
			return nil, nil, err
		}
		u.SetCurrentVersion(v.Number)
	}
	w, err = u.Save(ctx)
	if ent.IsNotFound(err) {
		return nil, nil, dms.ErrConflict
	}
	return w, v, err
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
			return dms.ErrConflict
		}
		n, err := t.Client.Workflow.Update().Where(workflow.IDEQ(id), workflow.VersionEQ(version)).SetDeletedAt(time.Now().UTC()).AddVersion(1).SetUpdatedBy(subject).Save(ctx)
		if err != nil {
			return err
		}
		if n != 1 {
			return dms.ErrConflict
		}
		if _, err = t.Client.WorkflowTrigger.Delete().Where(workflowtrigger.WorkflowIDEQ(id)).Exec(ctx); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.delete", w, map[string]any{"workflow_id": id})
	})
}

// index replaces the trigger index rows of w: one per trigger of its current
// definition while it is enabled. A schedule's next occurrence counts from
// now, so a change never starts runs for times already past.
func index(ctx context.Context, t *dms.Service, w *ent.Workflow, def Definition) error {
	if _, err := t.Client.WorkflowTrigger.Delete().Where(workflowtrigger.WorkflowIDEQ(w.ID)).Exec(ctx); err != nil {
		return err
	}
	if !w.Enabled || w.DeletedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	for i, tr := range def.Triggers {
		c := t.Client.WorkflowTrigger.Create().SetWorkflowID(w.ID).SetWorkspaceID(w.WorkspaceID).SetType(tr.Type).SetPosition(i)
		if tr.CollectionID != "" {
			c.SetCollectionID(tr.CollectionID)
		}
		if tr.Type == TriggerSchedule {
			sched, err := tr.schedule()
			if err != nil {
				return err
			}
			c.SetNextAt(sched.Next(now).UTC())
		}
		if _, err := c.Save(ctx); err != nil {
			return err
		}
	}
	return nil
}

// RunID is the runner's ID of the run of workflowID for eventID.
func RunID(workflowID, eventID string) string { return "wf:" + workflowID + ":" + eventID }

// Start asks for a manual run. Without items it starts one run with no item.
type Start struct {
	ItemIDs []string       `json:"item_ids,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
}

// MaxStartItems bounds one manual start.
const MaxStartItems = 100

// StartRuns starts a workflow by hand and returns the run IDs. Starting on
// items needs write access to each item; starting without an item needs
// manage access to the workspace. Everything is checked before any run
// starts, and the runs start when the request's transaction commits.
func (s *Service) StartRuns(ctx context.Context, subject, id string, in Start) (runs []string, err error) {
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		w, err := live(ctx, t, id)
		if err != nil {
			return err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "read"); err != nil {
			return err
		}
		if !w.Enabled {
			return dms.Invalid("the workflow is turned off")
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return err
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
		inputs, err := def.launchInputs(in.Inputs)
		if err != nil {
			return err
		}
		if len(in.ItemIDs) > MaxStartItems {
			return dms.Invalid(fmt.Sprintf("start at most %d items at once", MaxStartItems))
		}
		data := map[string]any{"workflow_id": w.ID, "inputs": inputs}
		if len(in.ItemIDs) == 0 {
			if manual.CollectionID != "" {
				return dms.Invalid("this workflow starts on items of its collection; give item_ids")
			}
			if err = workspace(ctx, t, subject, w.WorkspaceID, "manage"); err != nil {
				return err
			}
			e := dms.Event{ID: uuid.NewString(), Type: TriggerManual, WorkspaceID: w.WorkspaceID, Actor: subject, Data: data}
			t.Emit(ctx, e)
			runs = append(runs, RunID(w.ID, e.ID))
			return nil
		}
		seen := map[string]bool{}
		for _, itemID := range in.ItemIDs {
			if seen[itemID] {
				return dms.Invalid("item_ids has duplicates")
			}
			seen[itemID] = true
			r, err := t.Authorize(ctx, subject, itemID, "write")
			if err != nil {
				return err
			}
			if r.Kind != resource.KindItem || r.WorkspaceID != w.WorkspaceID {
				return dms.Invalid("item_ids must be items of the workflow's workspace")
			}
			if manual.CollectionID != "" && (r.ContainerID == nil || *r.ContainerID != manual.CollectionID) {
				return dms.Invalid("item " + itemID + " is not in the workflow's collection")
			}
			if def.Condition != nil {
				ok, err := matches(ctx, t, v.CreatedBy, *r.ContainerID, itemID, def.Condition)
				if err != nil {
					return err
				}
				if !ok {
					return dms.Invalid("item " + itemID + " does not meet the workflow's condition")
				}
			}
			e := dms.Event{ID: uuid.NewString(), Type: TriggerManual, WorkspaceID: w.WorkspaceID, CollectionID: *r.ContainerID, ResourceID: itemID, Actor: subject, Data: data}
			t.Emit(ctx, e)
			runs = append(runs, RunID(w.ID, e.ID))
		}
		return nil
	})
	return
}

// Match finds the runs a committed write's events start, records them, and
// returns their inputs for the runner to enqueue in the same transaction.
// Events at MaxDepth or deeper start nothing, which stops workflows that
// keep triggering each other.
func Match(ctx context.Context, t *dms.Service, events []dms.Event) ([]RunInput, error) {
	var out []RunInput
	versions := map[string]*matchable{}
	// A bulk write raises many events of few kinds; look each kind up once.
	lookups := map[[3]string][]*ent.Workflow{}
	for _, e := range events {
		if e.Depth >= MaxDepth {
			continue
		}
		var candidates []*ent.Workflow
		switch e.Type {
		case TriggerManual, TriggerSchedule:
			id, _ := e.Data["workflow_id"].(string)
			w, err := live(ctx, t, id)
			if err != nil {
				return nil, err
			}
			candidates = []*ent.Workflow{w}
		default:
			key := [3]string{e.Type, e.WorkspaceID, e.CollectionID}
			if cached, ok := lookups[key]; ok {
				candidates = cached
				break
			}
			rows, err := t.Client.WorkflowTrigger.Query().Where(workflowtrigger.TypeEQ(e.Type), workflowtrigger.WorkspaceIDEQ(e.WorkspaceID),
				workflowtrigger.Or(workflowtrigger.CollectionIDIsNil(), workflowtrigger.CollectionIDEQ(e.CollectionID))).All(ctx)
			if err != nil {
				return nil, err
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
				if candidates, err = t.Client.Workflow.Query().Where(workflow.IDIn(list...), workflow.EnabledEQ(true), workflow.DeletedAtIsNil()).Order(ent.Asc(workflow.FieldID)).All(ctx); err != nil {
					return nil, err
				}
			}
			lookups[key] = candidates
		}
		for _, w := range candidates {
			m := versions[w.ID]
			if m == nil {
				v, err := current(ctx, t, w)
				if err != nil {
					return nil, err
				}
				def, err := ParseDefinition(v.Definition)
				if err != nil {
					return nil, err
				}
				m = &matchable{w: w, v: v, def: def}
				versions[w.ID] = m
			}
			ok, err := m.matches(ctx, t, e)
			if err != nil || !ok {
				if err != nil {
					return nil, err
				}
				continue
			}
			runID := RunID(w.ID, e.ID)
			exists, err := t.Client.WorkflowRun.Query().Where(workflowrun.WorkflowIDEQ(w.ID), workflowrun.EventIDEQ(e.ID)).Exist(ctx)
			if err != nil {
				return nil, err
			}
			if exists {
				continue
			}
			c := t.Client.WorkflowRun.Create().SetID(runID).SetWorkflowID(w.ID).SetWorkflowVersion(m.v.Number).SetWorkspaceID(w.WorkspaceID).SetEventID(e.ID).SetEventType(e.Type).SetDepth(e.Depth).SetActor(e.Actor)
			if e.ResourceID != "" && e.Type != dms.EventItemDeleted {
				c.SetItemID(e.ResourceID)
			}
			if _, err = c.Save(ctx); err != nil {
				return nil, err
			}
			in := RunInput{RunID: runID, WorkflowID: w.ID, Version: m.v.Number, WorkspaceID: w.WorkspaceID, ItemID: e.ResourceID, Event: e}
			if e.Type == dms.EventItemDeleted {
				in.ItemID = ""
			}
			if inputs, ok := e.Data["inputs"].(map[string]any); ok && e.Type == TriggerManual {
				in.Inputs = inputs
			}
			out = append(out, in)
		}
	}
	return out, nil
}

type matchable struct {
	w   *ent.Workflow
	v   *ent.WorkflowVersion
	def Definition
}

// matches reports whether any trigger of e's type accepts e and the
// workflow's condition holds for e's item.
func (m *matchable) matches(ctx context.Context, t *dms.Service, e dms.Event) (bool, error) {
	if e.Type == TriggerManual || e.Type == TriggerSchedule {
		// Targeted events were checked when they were raised.
		return true, nil
	}
	accepted := false
	for _, tr := range m.def.Triggers {
		if tr.Type != e.Type || tr.CollectionID != "" && tr.CollectionID != e.CollectionID {
			continue
		}
		if tr.ContentTypeID != "" {
			typ, _ := e.Data["content_type_id"].(string)
			if typ == "" && e.ResourceID != "" {
				r, err := t.Client.Resource.Get(ctx, e.ResourceID)
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
		accepted = true
		break
	}
	if !accepted {
		return false, nil
	}
	if m.def.Condition == nil {
		return true, nil
	}
	if e.ResourceID == "" || e.CollectionID == "" {
		return false, nil
	}
	return matches(ctx, t, m.v.CreatedBy, e.CollectionID, e.ResourceID, m.def.Condition)
}

// Tick raises the schedule events that are due at now and moves each
// schedule to its next occurrence. Occurrences missed while PaperGo was down
// start once. A schedule with a collection starts one run per item that
// meets the condition, up to MaxScheduled per occurrence. A schedule that
// cannot run (its condition no longer compiles, its author lost access)
// still moves on, so it never holds up the others; such errors come back
// as skipped for the runner to log, while err is a failure of the tick.
func Tick(ctx context.Context, t *dms.Service, now time.Time) (raised int, skipped, err error) {
	due, err := t.Client.WorkflowTrigger.Query().Where(workflowtrigger.NextAtNotNil(), workflowtrigger.NextAtLTE(now)).Order(ent.Asc(workflowtrigger.FieldNextAt)).Limit(100).All(ctx)
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
		if err = t.Client.WorkflowTrigger.UpdateOne(row).SetNextAt(next).Exec(ctx); err != nil {
			return raised, nil, err
		}
	}
	return raised, errors.Join(failures...), nil
}

// occurrence computes the events of one due schedule trigger and its next
// occurrence after now. On error it returns no events.
func occurrence(ctx context.Context, t *dms.Service, row *ent.WorkflowTrigger, now time.Time) ([]dms.Event, time.Time, error) {
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
