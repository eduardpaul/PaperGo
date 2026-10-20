package workflow

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"sync"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

// BuiltIn is a workflow PaperGo ships: a script embedded from
// builtin/{key}.ts with a form of parameters. Turned on in a workspace (or
// for one collection), it becomes an ordinary workflow row whose version is
// the script read with the parameters, so its runs, history and triggers
// work like any other workflow. People change it through its parameters,
// turn it off, or copy it into a workflow they edit freely.
type BuiltIn struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// PerCollection built-ins are turned on for one list or library, whose
	// ID is the collection parameter.
	PerCollection bool `json:"per_collection"`
	// Role identifies the product process. Automatic providers are configured
	// per collection; the workspace catalog exposes manual use only.
	Role string `json:"role,omitempty"`
	// Default built-ins prepare an automatic provider in every collection
	// until people explicitly pause, replace or delete it.
	Default bool `json:"default,omitempty"`
	// System built-ins are product processes: their successful automatic
	// runs follow SYSTEM_RUN_RETENTION, manual runs ordinary retention, and
	// they may read taxonomy events whatever their author's scopes.
	System            bool              `json:"system,omitempty"`
	AutomaticTriggers []Trigger         `json:"automatic_triggers"`
	ManualMode        string            `json:"manual_mode"`
	ManualMediaTypes  []blobs.MediaType `json:"manual_media_types,omitempty"`
	// NeedsOCR built-ins read text in images.
	NeedsOCR bool `json:"needs_ocr,omitempty"`
	// ParametersSchema is the form of the built-in's parameters.
	ParametersSchema map[string]any `json:"parameters_schema"`
	// Source is the script, read from builtin/{key}.ts.
	Source string `json:"-"`
}

//go:embed builtin/*.ts
var builtinSources embed.FS

var (
	builtins = map[string]*BuiltIn{}
	// describing guards describe, which reads the triggers of built-ins
	// registered since the catalog was last listed.
	describing sync.Mutex
)

// RegisterBuiltIn adds a built-in to the release catalog, with its script
// from builtin/{key}.ts unless it has one. A built-in with a role needs that
// role registered and declares outputs that fill it.
func RegisterBuiltIn(b *BuiltIn) {
	if b.Source == "" {
		source, err := builtinSources.ReadFile("builtin/" + b.Key + ".ts")
		if err != nil {
			panic("built-in " + b.Key + ": " + err.Error())
		}
		b.Source = string(source)
	}
	if b.Role != "" && (b.PerCollection || roles[b.Role] == nil) {
		panic("built-in " + b.Key + ": a role built-in uses role scoping and fills a registered role")
	}
	builtins[b.Key] = b
}

// describedCollection stands for the collection a per-collection built-in
// is turned on for, when the catalog describes it.
const describedCollection = "00000000-0000-0000-0000-000000000000"

// describe reads the triggers of every built-in not yet described from
// its script with its default parameters, for the catalog.
func describe() {
	describing.Lock()
	defer describing.Unlock()
	for _, b := range builtins {
		if b.AutomaticTriggers != nil {
			continue
		}
		b.ManualMode, b.AutomaticTriggers = "none", []Trigger{}
		params := forms.Defaults(b.ParametersSchema)
		if b.PerCollection || b.Role != "" {
			// Its triggers name the collection it is turned on for.
			params["collection"] = describedCollection
		}
		def, _, err := compile(context.Background(), b.Source, params)
		if err != nil {
			panic("built-in " + b.Key + ": " + err.Error())
		}
		for _, tr := range def.Triggers {
			if tr.CollectionID == describedCollection {
				tr.CollectionID = ""
			}
			if tr.Type != TriggerManual {
				b.AutomaticTriggers = append(b.AutomaticTriggers, tr)
				continue
			}
			b.ManualMode = "standalone"
			if tr.CollectionID != "" || tr.Selection == SelectionPerItem || b.PerCollection || b.Role != "" {
				b.ManualMode = "single"
			}
			if tr.Selection == SelectionAll {
				b.ManualMode = "selection"
			}
		}
	}
}

// systemWorkflow reports whether the built-in key identifies a system workflow.
func systemWorkflow(key string) bool {
	return builtins[key] != nil && builtins[key].System
}

// BuiltIns returns the release catalog, sorted by key.
func BuiltIns() []*BuiltIn {
	describe()
	out := make([]*BuiltIn, 0, len(builtins))
	for _, b := range builtins {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// params applies defaults and checks a built-in's parameters.
func (b *BuiltIn) params(values map[string]any, collectionID string) (map[string]any, error) {
	out, err := forms.Values(b.ParametersSchema, values, "parameters")
	if err != nil {
		return nil, err
	}
	if b.PerCollection || b.Role != "" {
		out["collection"] = collectionID
	}
	return out, nil
}

// compile reads the built-in's version with params, with the disabled
// automatic triggers turned off.
func (b *BuiltIn) compile(ctx context.Context, params map[string]any, disabled []string) (Definition, workflows.Content, error) {
	def, content, err := compile(ctx, b.Source, params)
	if err != nil {
		return Definition{}, workflows.Content{}, err
	}
	if len(disabled) == 0 {
		return def, content, nil
	}
	if content.Definition, err = withDisabledTriggers(content.Definition, disabled); err != nil {
		return Definition{}, workflows.Content{}, err
	}
	def, err = ParseDefinition(content.Definition)
	return def, content, err
}

// SetBuiltIn turns a built-in on or off and sets its parameters. The first
// call creates its workflow; later calls need the workflow's version.
type SetBuiltIn struct {
	CollectionID     string         `json:"collection_id,omitempty"`
	AutomaticEnabled bool           `json:"automatic_enabled"`
	ManualEnabled    *bool          `json:"manual_enabled,omitempty"`
	State            string         `json:"state,omitempty"`
	DisabledTriggers *[]string      `json:"disabled_triggers,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	// Version is the workflow's concurrency version, required once it exists.
	Version int `json:"-"`
}

func (s *Service) builtInRow(ctx context.Context, t *dms.Service, workspaceID string, b *BuiltIn, collectionID string) (*workflows.Workflow, error) {
	rows, err := listWorkflows(ctx, t, workflows.Filter{WorkspaceID: workspaceID, BuiltinKey: b.Key, CollectionID: collectionID, CollectionAbsent: collectionID == "", LiveOnly: true})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (s *Service) builtInScope(ctx context.Context, t *dms.Service, subject, workspaceID string, b *BuiltIn, collectionID string) error {
	if err := workspace(ctx, t, subject, workspaceID, "manage"); err != nil {
		return err
	}
	switch {
	case collectionID == "" && b.PerCollection:
		return dms.Invalid("this built-in is turned on per list or library; give collection_id")
	case collectionID == "":
		return nil
	case !b.PerCollection && b.Role == "":
		return dms.Invalid("this built-in belongs to the workspace; omit collection_id")
	}
	c, err := t.Authorize(ctx, subject, collectionID, "manage")
	if err != nil {
		return err
	}
	if (b.Key == "documents.optimize" || b.Key == "documents.photos_to_pdf") && c.Kind != "library" {
		return dms.Invalid("Choose a library for this file feature.")
	}
	if c.WorkspaceID != workspaceID || (c.Kind != "list" && c.Kind != "library") {
		return dms.Invalid("collection_id must be a list or library of the workspace")
	}
	return nil
}

// storedParams are the parameters kept with a workflow: the collection is
// its scope, not a parameter.
func storedParams(params map[string]any) map[string]any {
	stored := map[string]any{}
	for k, v := range params {
		if k != "collection" {
			stored[k] = v
		}
	}
	return stored
}

// createBuiltIn creates the workflow of a built-in in a workspace, or for
// collection c, with its script read with the parameters, and indexes its
// triggers.
func createBuiltIn(ctx context.Context, t *dms.Service, subject, workspaceID string, b *BuiltIn, collection string, enabled bool, values map[string]any, options ...SetBuiltIn) (*workflows.Workflow, *workflows.Version, error) {
	params, err := b.params(values, collection)
	if err != nil {
		return nil, nil, err
	}
	var disabled []string
	if len(options) > 0 && options[0].DisabledTriggers != nil {
		disabled = *options[0].DisabledTriggers
	}
	def, content, err := b.compile(ctx, params, disabled)
	if err != nil {
		return nil, nil, err
	}
	if err = checkDefinition(ctx, t, subject, workspaceID, def); err != nil {
		return nil, nil, dms.Invalid("parameters: " + err.Error())
	}
	if enabled {
		if err := automaticScope(def); err != nil {
			return nil, nil, err
		}
	}
	name, wkey := b.Name, keyFromName(b.Key)
	if collection != "" {
		c, err := t.DAL.Resources().Resource(ctx, collection)
		if err != nil {
			return nil, nil, err
		}
		name = fmt.Sprintf("%s (%s)", b.Name, c.Name)
		suffix := c.ID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		wkey = keyFromName(b.Key + "-" + suffix)
	}
	if wkey, err = freeKey(ctx, t, workspaceID, wkey); err != nil {
		return nil, nil, err
	}
	baseName := name
	for n := 2; ; n++ {
		exists, e := t.DAL.Workflows().Exists(ctx, workflows.Filter{WorkspaceID: workspaceID, Name: name, LiveOnly: true})
		if e != nil {
			return nil, nil, e
		}
		if !exists {
			break
		}
		name = fmt.Sprintf("%s (%d)", baseName, n)
	}
	in := workflows.CreateInput{WorkspaceID: workspaceID, Key: wkey, Name: name, Description: b.Description, AutomaticEnabled: enabled, ManualEnabled: true, BuiltinKey: b.Key, Role: b.Role, CollectionID: collection, AutomaticProvider: b.Role != "" && collection != "", CreatedBy: subject, Content: content}
	if len(options) > 0 {
		option := options[0]
		if option.State != "" {
			if err := validState(workflows.State(option.State)); err != nil {
				return nil, nil, err
			}
			in.State = workflows.State(option.State)
		}
		if option.ManualEnabled != nil {
			in.ManualEnabled = *option.ManualEnabled
		}
	}
	if in.Parameters, err = jsonObject(storedParams(params)); err != nil {
		return nil, nil, err
	}
	w, err := createWorkflow(ctx, t, in)
	if err != nil {
		return nil, nil, err
	}
	v, err := current(ctx, t, w)
	if err != nil {
		return nil, nil, err
	}
	return w, v, index(ctx, t, w, def)
}

// UpdateBuiltIn creates or changes the workflow of a built-in.
func (s *Service) UpdateBuiltIn(ctx context.Context, subject, workspaceID, key string, in SetBuiltIn) (out Workflow, err error) {
	b := builtins[key]
	if b == nil {
		return out, dms.ErrNotFound
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if in.State != "" {
			if err := validState(workflows.State(in.State)); err != nil {
				return err
			}
		}
		err := s.builtInScope(ctx, t, subject, workspaceID, b, in.CollectionID)
		if err != nil {
			return err
		}
		if in.AutomaticEnabled && b.Role != "" && in.CollectionID == "" {
			return dms.Invalid("Choose a specific list or library for automatic processing.")
		}
		params, err := b.params(in.Parameters, in.CollectionID)
		if err != nil {
			return err
		}
		w, err := s.builtInRow(ctx, t, workspaceID, b, in.CollectionID)
		if err != nil {
			return err
		}
		var v *workflows.Version
		if w == nil {
			if in.Version != 0 {
				return dms.ErrPreconditionFailed
			}
			if w, v, err = createBuiltIn(ctx, t, subject, workspaceID, b, in.CollectionID, in.AutomaticEnabled, in.Parameters, in); err != nil {
				return err
			}
		} else {
			if in.Version != w.Version {
				return dms.ErrPreconditionFailed
			}
			cur, err := current(ctx, t, w)
			if err != nil {
				return err
			}
			disabled := disabledTriggerTypes(cur.Definition)
			if in.DisabledTriggers != nil {
				disabled = *in.DisabledTriggers
			}
			def, content, err := b.compile(ctx, params, disabled)
			if err != nil {
				return err
			}
			if err = checkDefinition(ctx, t, subject, workspaceID, def); err != nil {
				return dms.Invalid("parameters: " + err.Error())
			}
			if w, v, err = revise(ctx, t, subject, w, content, func(u *workflows.Changes) {
				// Whoever configures a built-in becomes its runs' author.
				u.Author = true
				u.AutomaticEnabled = &in.AutomaticEnabled
				values := storedParams(params)
				u.Parameters = &values
				if b.Role != "" && in.CollectionID != "" {
					u.AutomaticProvider = boolPtr(true)
				}
				if in.ManualEnabled != nil {
					u.ManualEnabled = in.ManualEnabled
				}
				if in.State != "" {
					state := workflows.State(in.State)
					u.State = &state
				}
			}); err != nil {
				return err
			}
			if err = index(ctx, t, w, def); err != nil {
				return err
			}
		}
		if err = claimRole(ctx, t, subject, w); err != nil {
			return err
		}
		if out, err = toWorkflow(w, v); err != nil {
			return err
		}
		return audit(ctx, t, subject, "workflow.builtin", w, map[string]any{"workflow_id": w.ID, "builtin_key": b.Key, "automatic_enabled": in.AutomaticEnabled, "version": w.CurrentVersion})
	})
	return
}

// CopyBuiltIn creates an editable draft without changing the source or
// claiming automatic provider ownership.
type CopyBuiltIn struct {
	CollectionID string         `json:"collection_id,omitempty"`
	Name         string         `json:"name"`
	Parameters   map[string]any `json:"parameters,omitempty"`
}

// CopyBuiltIn creates a draft workflow with the built-in's script and
// parameters, which its managers edit freely.
func (s *Service) CopyBuiltIn(ctx context.Context, subject, workspaceID, key string, in CopyBuiltIn) (out Workflow, err error) {
	b := builtins[key]
	if b == nil {
		return out, dms.ErrNotFound
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := s.builtInScope(ctx, t, subject, workspaceID, b, in.CollectionID); err != nil {
			return err
		}
		params, err := b.params(in.Parameters, in.CollectionID)
		if err != nil {
			return err
		}
		// A copy that is not a role provider keeps its collection among its
		// parameters, where its script reads it.
		if b.Role != "" {
			params = storedParams(params)
		}
		out, err = s.create(ctx, t, subject, workspaceID, Save{Name: in.Name, Description: b.Description, Source: b.Source, Parameters: params,
			State: "draft", Role: b.Role, CollectionID: roleScope(b, in.CollectionID)})
		return err
	})
	return
}

// BuiltInState is a built-in with the workflows that turn it on in a
// workspace.
type BuiltInState struct {
	*BuiltIn
	Workflows []Workflow `json:"workflows"`
}

// ListBuiltIns returns the catalog with the workspace's built-in workflows.
func (s *Service) ListBuiltIns(ctx context.Context, subject, workspaceID string) ([]BuiltInState, error) {
	if err := s.DMS.Write(ctx, func(t *dms.Service) error {
		if err := workspace(ctx, t, subject, workspaceID, "read"); err != nil {
			return err
		}
		return EnsureDefaults(ctx, t, workspaceID)
	}); err != nil {
		return nil, err
	}
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
// changed script becomes a new version (running runs keep theirs), and a
// built-in no longer shipped is turned off. Parameters that no longer
// validate keep the old version, turned off. Startup calls it.
func (s *Service) SyncBuiltIns(ctx context.Context) error {
	return s.DMS.Write(ctx, func(t *dms.Service) error {
		rows, err := listWorkflows(ctx, t, workflows.Filter{OnlyBuiltins: true, LiveOnly: true})
		if err != nil {
			return err
		}
		for _, w := range rows {
			cur, err := current(ctx, t, w)
			if err != nil {
				return err
			}
			content, def, ok := cur.Content, Definition{}, false
			if b := builtins[w.BuiltinKey]; b != nil {
				if params, e := b.params(w.Parameters, w.CollectionID); e == nil {
					var next workflows.Content
					if def, next, e = b.compile(ctx, params, disabledTriggerTypes(cur.Definition)); e == nil && checkDefinition(ctx, t, w.UpdatedBy, w.WorkspaceID, def) == nil {
						content, ok = next, true
					}
				}
			}
			enabled := w.AutomaticEnabled && ok
			if !ok {
				if def, err = ParseDefinition(cur.Definition); err != nil {
					return err
				}
			}
			if cur.BundleHash == content.BundleHash && cur.Source == content.Source && string(cur.Definition) == string(content.Definition) && enabled == w.AutomaticEnabled {
				continue
			}
			if w, _, err = revise(ctx, t, w.UpdatedBy, w, content, func(u *workflows.Changes) { u.AutomaticEnabled = &enabled }); err != nil {
				return err
			}
			if err = index(ctx, t, w, def); err != nil {
				return err
			}
		}
		workspaces, err := t.DAL.Resources().Resources(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, State: resources.Live})
		if err != nil {
			return err
		}
		for _, ws := range workspaces {
			if err = EnsureDefaults(ctx, t, ws.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SystemBuiltIns lists the keys of the system built-ins.
func SystemBuiltIns() []string {
	var out []string
	for _, b := range BuiltIns() {
		if b.System {
			out = append(out, b.Key)
		}
	}
	return out
}

// roleScope is the collection a copy of b fills b's role for: copies of
// other built-ins are ordinary workspace workflows.
func roleScope(b *BuiltIn, collectionID string) string {
	if b.Role == "" {
		return ""
	}
	return collectionID
}
