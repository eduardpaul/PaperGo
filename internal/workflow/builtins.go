package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"papergo/ent"
	"papergo/ent/resource"
	"papergo/ent/workflow"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

// BuiltIn is a workflow PaperGo ships. Turned on in a workspace (or for one
// collection), it becomes an ordinary workflow row whose definition is the
// release definition with the parameters filled in, so its runs, history
// and triggers work like any other workflow. People change it through its
// parameters, turn it off, or copy it into a workflow they edit freely.
type BuiltIn struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// PerCollection built-ins are turned on for one list or library;
	// {param:collection} is that collection's ID.
	PerCollection bool `json:"per_collection"`
	// ParametersSchema is the form of the built-in's parameters.
	ParametersSchema map[string]any `json:"parameters_schema"`
	definition       string
}

var builtins = map[string]*BuiltIn{}

func registerBuiltIn(b *BuiltIn) { builtins[b.Key] = b }

// BuiltIns returns the release catalog, sorted by key.
func BuiltIns() []*BuiltIn {
	out := make([]*BuiltIn, 0, len(builtins))
	for _, b := range builtins {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func init() {
	registerBuiltIn(&BuiltIn{
		Key:           "items.expire",
		Name:          "Unpublish expired items",
		Description:   "Every day, unpublishes the published items whose date field is before today.",
		PerCollection: true,
		ParametersSchema: forms.MustParse(`{"type": "object", "required": ["field"], "properties": {
			"field": {"type": "string", "title": "Expiry field", "description": "Key of an indexed date field holding the expiry date."},
			"cron": {"type": "string", "title": "Schedule", "description": "When to check, as a cron expression.", "default": "0 1 * * *"},
			"time_zone": {"type": "string", "title": "Time zone", "description": "Time zone of the schedule.", "default": "UTC"}
		}}`),
		definition: `{
			"triggers": [{"type": "schedule", "collection_id": "{param:collection}", "cron": "{param:cron}", "time_zone": "{param:time_zone}", "surface": "published"}],
			"condition": {"field": "{param:field}", "op": "lt", "value_ref": "today"},
			"flow": {"start": "unpublish", "nodes": {"unpublish": {"activity": "item.unpublish"}}}
		}`,
	})
}

func init() {
	registerBuiltIn(&BuiltIn{
		Key:           "documents.optimize",
		Name:          "Optimize image storage",
		Description:   "When an image is added, proposes a smaller WebP copy whose smallest text keeps a readable height, and asks the owners (or the approvers) to compare both before it replaces the file. A newer file cancels the waiting review. Also runs on demand.",
		PerCollection: true,
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text keeps.", "minimum": 4, "maximum": 200, "default": 12},
			"min_confidence": {"type": "number", "title": "Minimum confidence", "description": "Detector score a text line needs, 0 to 100.", "minimum": 0, "maximum": 100, "default": 50},
			"percentile": {"type": "number", "title": "Height percentile", "description": "Percentile of the text heights taken as the smallest text.", "minimum": 0, "maximum": 100, "default": 5},
			"max_analysis_dimension": {"type": "integer", "title": "Analysis size", "description": "Longest side in pixels of the copy the detector analyzes.", "minimum": 256, "maximum": 2600, "default": 2600},
			"minimum_scale": {"type": "number", "title": "Minimum scale", "description": "How far an image may shrink.", "minimum": 0.01, "maximum": 1, "default": 0.05},
			"quality": {"type": "integer", "title": "WebP quality", "minimum": 1, "maximum": 100, "default": 80},
			"approvers": {"type": "array", "title": "Approvers", "description": "Who reviews the smaller file; none means the workspace's owners.", "maxItems": 50, "items": {"type": "string"}, "x-papergo": {"kind": "people", "access": "read_draft"}}
		}}`),
		definition: `{
			"triggers": [{"type": "file.added", "collection_id": "{param:collection}"}, {"type": "manual", "collection_id": "{param:collection}"}],
			"concurrency": "replace",
			"flow": {"start": "prepare", "nodes": {
				"prepare": {"activity": "file.optimize_image", "inputs": {"target_height": "{param:target_height}", "min_confidence": "{param:min_confidence}",
					"percentile": "{param:percentile}", "max_analysis_dimension": "{param:max_analysis_dimension}", "minimum_scale": "{param:minimum_scale}", "quality": "{param:quality}"},
					"next": {"candidate": "review"}},
				"review": {"activity": "approval", "inputs": {"title": "Review a smaller file for {item:name}",
					"description": "The proposed WebP file is shown beside the current file at full size. Approve if its smallest text is still readable.",
					"assignees": "{param:approvers}", "review": {"type": "file.compare", "staged_file_id": "{step:prepare.staged_file_id}"}},
					"next": {"approved": "promote", "rejected": "discard"}},
				"promote": {"activity": "file.promote", "inputs": {"staged_file_id": "{step:prepare.staged_file_id}"}},
				"discard": {"activity": "file.discard", "inputs": {"staged_file_id": "{step:prepare.staged_file_id}"}}
			}}
		}`,
	})
}

func init() {
	registerBuiltIn(&BuiltIn{
		Key:           "documents.photos_to_pdf",
		Name:          "Photos to PDF",
		Description:   "Combines selected photos, in the chosen order, into one searchable PDF. After the owners (or the approvers) review it, the primary item gets the PDF as its new file, keeping its metadata and file history, and the other photos go to the recycle bin. Started on a selection.",
		PerCollection: true,
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text of each page keeps; pages never grow.", "minimum": 8, "maximum": 200, "default": 32},
			"quality": {"type": "integer", "title": "JPEG quality", "minimum": 1, "maximum": 100, "default": 80},
			"approvers": {"type": "array", "title": "Approvers", "description": "Who reviews the PDF; none means the workspace's owners.", "maxItems": 50, "items": {"type": "string"}, "x-papergo": {"kind": "people", "access": "read_draft"}}
		}}`),
		definition: `{
			"input_schema": {"type": "object", "properties": {}, "x-papergo-selection": {"preview": "image", "item_label": "page", "order_label": "Page order",
				"primary_description": "This item gets the approved PDF as its new file, keeping its metadata and file history. The other photos go to the recycle bin."}},
			"triggers": [{"type": "manual", "collection_id": "{param:collection}", "selection": "selection"}],
			"concurrency": "skip",
			"flow": {"start": "compose", "nodes": {
				"compose": {"activity": "file.compose_pdf", "inputs": {"target_height": "{param:target_height}", "quality": "{param:quality}"}, "next": {"done": "review"}},
				"review": {"activity": "approval", "inputs": {"title": "Review the PDF made from photos for {item:name}",
					"description": "The pages are the selected photos in order. Approve to replace the primary item's file with this PDF and move the other photos to the recycle bin.",
					"assignees": "{param:approvers}", "review": {"type": "file.preview", "staged_file_id": "{step:compose.staged_file_id}"}},
					"next": {"approved": "promote", "rejected": "discard"}},
				"promote": {"activity": "file.promote", "inputs": {"staged_file_id": "{step:compose.staged_file_id}", "recycle_sources": true}},
				"discard": {"activity": "file.discard", "inputs": {"staged_file_id": "{step:compose.staged_file_id}"}}
			}}
		}`,
	})
}

var paramPattern = regexp.MustCompile(`\{param:([a-z][a-z0-9_]*)\}`)

// fill replaces {param:x} in the release definition. A string that is
// exactly one parameter takes the parameter's typed value.
func (b *BuiltIn) fill(params map[string]any) (json.RawMessage, error) {
	var tree any
	if err := json.Unmarshal([]byte(b.definition), &tree); err != nil {
		return nil, err
	}
	var walk func(any) any
	walk = func(v any) any {
		switch node := v.(type) {
		case string:
			if m := paramPattern.FindStringSubmatch(node); m != nil && m[0] == node {
				return params[m[1]]
			}
			return paramPattern.ReplaceAllStringFunc(node, func(token string) string {
				return text2(params[paramPattern.FindStringSubmatch(token)[1]])
			})
		case map[string]any:
			for k, child := range node {
				node[k] = walk(child)
			}
		case []any:
			for i, child := range node {
				node[i] = walk(child)
			}
		}
		return v
	}
	return json.Marshal(walk(tree))
}

// params applies defaults and checks a built-in's parameters.
func (b *BuiltIn) params(values map[string]any, collectionID string) (map[string]any, error) {
	out, err := forms.Values(b.ParametersSchema, values, "parameters")
	if err != nil {
		return nil, err
	}
	if b.PerCollection {
		out["collection"] = collectionID
	}
	return out, nil
}

// SetBuiltIn turns a built-in on or off and sets its parameters. The first
// call creates its workflow; later calls need the workflow's version.
type SetBuiltIn struct {
	CollectionID string         `json:"collection_id,omitempty"`
	Enabled      bool           `json:"enabled"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	// Version is the workflow's concurrency version, required once it exists.
	Version int `json:"-"`
}

func (s *Service) builtInRow(ctx context.Context, t *dms.Service, workspaceID string, b *BuiltIn, collectionID string) (*ent.Workflow, error) {
	q := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.BuiltinKeyEQ(b.Key), workflow.DeletedAtIsNil())
	if collectionID != "" {
		q.Where(workflow.CollectionIDEQ(collectionID))
	} else {
		q.Where(workflow.CollectionIDIsNil())
	}
	w, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return w, err
}

func (s *Service) builtInScope(ctx context.Context, t *dms.Service, subject, workspaceID string, b *BuiltIn, collectionID string) (*ent.Resource, error) {
	if err := workspace(ctx, t, subject, workspaceID, "manage"); err != nil {
		return nil, err
	}
	if !b.PerCollection {
		if collectionID != "" {
			return nil, dms.Invalid("this built-in belongs to the workspace; omit collection_id")
		}
		return nil, nil
	}
	if collectionID == "" {
		return nil, dms.Invalid("this built-in is turned on per list or library; give collection_id")
	}
	c, err := t.Authorize(ctx, subject, collectionID, "manage")
	if err != nil {
		return nil, err
	}
	if c.WorkspaceID != workspaceID || (c.Kind != resource.KindList && c.Kind != resource.KindLibrary) {
		return nil, dms.Invalid("collection_id must be a list or library of the workspace")
	}
	return c, nil
}

// UpdateBuiltIn creates or changes the workflow of a built-in.
func (s *Service) UpdateBuiltIn(ctx context.Context, subject, workspaceID, key string, in SetBuiltIn) (out Workflow, err error) {
	b := builtins[key]
	if b == nil {
		return out, dms.ErrNotFound
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		c, err := s.builtInScope(ctx, t, subject, workspaceID, b, in.CollectionID)
		if err != nil {
			return err
		}
		params, err := b.params(in.Parameters, in.CollectionID)
		if err != nil {
			return err
		}
		filled, err := b.fill(params)
		if err != nil {
			return err
		}
		def, raw, err := s.check(ctx, t, subject, workspaceID, filled)
		if err != nil {
			return dms.Invalid("parameters: " + err.Error())
		}
		w, err := s.builtInRow(ctx, t, workspaceID, b, in.CollectionID)
		if err != nil {
			return err
		}
		stored := map[string]any{}
		for k, v := range params {
			if k != "collection" {
				stored[k] = v
			}
		}
		var v *ent.WorkflowVersion
		if w == nil {
			if in.Version != 0 {
				return dms.ErrPreconditionFailed
			}
			name, wkey := b.Name, keyFromName(b.Key)
			if c != nil {
				name = fmt.Sprintf("%s (%s)", b.Name, c.Name)
				wkey = keyFromName(b.Key + "-" + c.ID[:8])
			}
			if wkey, err = freeKey(ctx, t, workspaceID, wkey); err != nil {
				return err
			}
			create := t.Client.Workflow.Create().SetWorkspaceID(workspaceID).SetKey(wkey).SetName(name).SetDescription(b.Description).SetEnabled(in.Enabled).SetBuiltinKey(b.Key).SetParameters(stored).SetCurrentVersion(1).SetCreatedBy(subject).SetUpdatedBy(subject)
			if c != nil {
				create.SetCollectionID(c.ID)
			}
			if w, err = create.Save(ctx); err != nil {
				return err
			}
			if v, err = t.Client.WorkflowVersion.Create().SetWorkflowID(w.ID).SetNumber(1).SetDefinition(raw).SetCreatedBy(subject).Save(ctx); err != nil {
				return err
			}
		} else {
			if in.Version != w.Version {
				return dms.ErrPreconditionFailed
			}
			if w, v, err = revise(ctx, t, subject, w, raw, func(u *ent.WorkflowUpdateOne) { u.SetEnabled(in.Enabled).SetParameters(stored) }); err != nil {
				return err
			}
		}
		if err = index(ctx, t, w, def); err != nil {
			return err
		}
		if out, err = toWorkflow(w, v); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.builtin", w, map[string]any{"workflow_id": w.ID, "builtin_key": b.Key, "enabled": in.Enabled, "version": w.CurrentVersion})
	})
	return
}

// CopyBuiltIn creates an ordinary, freely editable workflow from a built-in
// and turns the built-in off where it was on, so the copy replaces it.
type CopyBuiltIn struct {
	CollectionID string         `json:"collection_id,omitempty"`
	Name         string         `json:"name"`
	Parameters   map[string]any `json:"parameters,omitempty"`
}

func (s *Service) CopyBuiltIn(ctx context.Context, subject, workspaceID, key string, in CopyBuiltIn) (out Workflow, err error) {
	b := builtins[key]
	if b == nil {
		return out, dms.ErrNotFound
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if _, err := s.builtInScope(ctx, t, subject, workspaceID, b, in.CollectionID); err != nil {
			return err
		}
		params, err := b.params(in.Parameters, in.CollectionID)
		if err != nil {
			return err
		}
		filled, err := b.fill(params)
		if err != nil {
			return err
		}
		if out, err = s.create(ctx, t, subject, workspaceID, Save{Name: in.Name, Description: b.Description, Definition: filled}); err != nil {
			return err
		}
		w, err := s.builtInRow(ctx, t, workspaceID, b, in.CollectionID)
		if err != nil || w == nil || !w.Enabled {
			return err
		}
		if w, _, err = revise(ctx, t, subject, w, mustCurrent(ctx, t, w), func(u *ent.WorkflowUpdateOne) { u.SetEnabled(false) }); err != nil {
			return err
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return err
		}
		return index(ctx, t, w, def)
	})
	return
}

func mustCurrent(ctx context.Context, t *dms.Service, w *ent.Workflow) json.RawMessage {
	v, err := current(ctx, t, w)
	if err != nil {
		return nil
	}
	return v.Definition
}

// BuiltInState is a built-in with the workflows that turn it on in a
// workspace.
type BuiltInState struct {
	*BuiltIn
	Workflows []Workflow `json:"workflows"`
}

// ListBuiltIns returns the catalog with the workspace's built-in workflows.
func (s *Service) ListBuiltIns(ctx context.Context, subject, workspaceID string) ([]BuiltInState, error) {
	all, err := s.List(ctx, subject, workspaceID)
	if err != nil {
		return nil, err
	}
	out := []BuiltInState{}
	for _, b := range BuiltIns() {
		st := BuiltInState{BuiltIn: b, Workflows: []Workflow{}}
		for _, w := range all {
			if w.BuiltInKey != nil && *w.BuiltInKey == b.Key {
				st.Workflows = append(st.Workflows, w)
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// SyncBuiltIns brings every built-in workflow to the running release: a
// changed release definition becomes a new version (running runs keep
// theirs), and a built-in no longer shipped is turned off. Parameters that
// no longer validate keep the old version, turned off. The runner calls it
// when it launches.
func (s *Service) SyncBuiltIns(ctx context.Context) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		rows, err := t.Client.Workflow.Query().Where(workflow.BuiltinKeyNotNil(), workflow.DeletedAtIsNil()).All(ctx)
		if err != nil {
			return err
		}
		for _, w := range rows {
			b := builtins[*w.BuiltinKey]
			var raw json.RawMessage
			var def Definition
			if b != nil {
				collection := ""
				if w.CollectionID != nil {
					collection = *w.CollectionID
				}
				params, err := b.params(w.Parameters, collection)
				if err == nil {
					var filled json.RawMessage
					if filled, err = b.fill(params); err == nil {
						def, raw, err = s.check(ctx, t, w.UpdatedBy, w.WorkspaceID, filled)
					}
				}
				if err != nil {
					raw = nil
				}
			}
			enabled := w.Enabled && raw != nil
			if raw == nil {
				raw = mustCurrent(ctx, t, w)
				if def, err = ParseDefinition(raw); err != nil {
					return err
				}
			}
			if v, err := current(ctx, t, w); err != nil {
				return err
			} else if string(v.Definition) == string(raw) && enabled == w.Enabled {
				continue
			}
			if w, _, err = revise(ctx, t, w.UpdatedBy, w, raw, func(u *ent.WorkflowUpdateOne) { u.SetEnabled(enabled) }); err != nil {
				return err
			}
			if err = index(ctx, t, w, def); err != nil {
				return err
			}
		}
		return nil
	})
}
