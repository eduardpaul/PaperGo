package workflow

import (
	"context"
	"fmt"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"slices"
	"sort"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
)

// Role is a product process that a workflow fills, such as search.index.
// A role's built-in fills it by default; copies and alternatives fill it
// instead, one automatic provider per collection. A filler declares outputs
// compatible with the role's OutputSchema, so workflows that follow the role
// (wf.{role}.completed) keep working when the filler is replaced.
type Role struct {
	Key          string         `json:"key"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

var roles = map[string]*Role{}

// RegisterRole adds a role to the catalog.
func RegisterRole(r *Role) { roles[r.Key] = r }

// Roles returns the role catalog, sorted by key.
func Roles() []*Role {
	out := make([]*Role, 0, len(roles))
	for _, r := range roles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// fills checks that def can fill the role: its output schema has every
// output of the role with the same type, and requires what the role
// requires.
func (r *Role) fills(def Definition) error {
	want, _ := r.OutputSchema["properties"].(map[string]any)
	if len(want) == 0 {
		return nil
	}
	have, _ := def.OutputSchema["properties"].(map[string]any)
	for name, raw := range want {
		w, _ := raw.(map[string]any)
		h, ok := have[name].(map[string]any)
		if !ok || h["type"] != w["type"] {
			return dms.Invalid(fmt.Sprintf("a workflow filling %s declares output %s of type %v", r.Key, name, w["type"]))
		}
	}
	required, _ := def.OutputSchema["required"].([]any)
	wanted, _ := r.OutputSchema["required"].([]any)
	for _, name := range wanted {
		if !slices.Contains(required, name) {
			return dms.Invalid(fmt.Sprintf("a workflow filling %s requires output %v", r.Key, name))
		}
	}
	return nil
}

// claimRole makes w the only enabled filler of its role in its scope: other
// enabled fillers of the role in the same workspace and collection are
// turned off.
func claimRole(ctx context.Context, t *dms.Service, subject string, w *workflows.Workflow) error {
	if w.Role == "" || !w.AutomaticEnabled || w.State != workflows.StatePublished || !w.AutomaticProvider {
		return nil
	}
	others, err := listWorkflows(ctx, t, workflows.Filter{WorkspaceID: w.WorkspaceID, Role: w.Role, AutomaticEnabled: boolPtr(true), State: workflows.StatePublished, LiveOnly: true, ExcludeID: w.ID, CollectionID: func() string {
		if w.CollectionID != "" {
			return w.CollectionID
		}
		return ""
	}(), CollectionAbsent: w.CollectionID == ""})
	if err != nil {
		return err
	}
	for _, other := range others {
		other, _, err = revise(ctx, t, subject, other, mustCurrent(ctx, t, other), func(u *workflows.Changes) { u.AutomaticEnabled = boolPtr(false) })
		if err != nil {
			return err
		}
		v, err := current(ctx, t, other)
		if err != nil {
			return err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return err
		}
		if err = index(ctx, t, other, def); err != nil {
			return err
		}
	}
	return nil
}

// EnsureDefaults prepares manual catalog entries and collection-specific default
// providers. Explicit settings, including paused or deleted providers, are preserved.
func EnsureDefaults(ctx context.Context, t *dms.Service, workspaceID string) error {
	ws, err := t.DAL.Resources().Resource(ctx, workspaceID)
	if dberr.IsNotFound(err) || err == nil && (ws.Kind != refs.KindWorkspace || ws.DeletedAt != nil) {
		return nil
	}
	if err != nil {
		return err
	}
	collections, err := t.DAL.Resources().Resources(ctx, resources.Filter{WorkspaceID: workspaceID, Kinds: []refs.ResourceKind{refs.KindList, refs.KindLibrary}, State: resources.Live})
	if err != nil {
		return err
	}
	for _, b := range BuiltIns() {
		if !b.Default {
			continue
		}
		// Workspace entries expose manual use; automatic processing belongs to collections.
		had, err := t.DAL.Workflows().Exists(ctx, workflows.Filter{WorkspaceID: workspaceID, BuiltinKey: b.Key, CollectionAbsent: true})
		if err != nil {
			return err
		}
		if !had && b.ManualMode != "none" {
			if _, _, err = createBuiltIn(ctx, t, ws.CreatedBy, workspaceID, b, "", false, map[string]any{}); err != nil {
				return err
			}
		}
		for _, collection := range collections {
			had, err := t.DAL.Workflows().HasProvider(ctx, workspaceID, collection.ID, b.Key, b.Role)
			if err != nil {
				return err
			}
			if !had {
				if _, _, err = createBuiltIn(ctx, t, ws.CreatedBy, workspaceID, b, collection.ID, true, map[string]any{}); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// RoleState identifies a collection's automatic provider.
type RoleState struct {
	Role *Role `json:"role"`
	// Scope is collection when a provider is configured; empty
	// when nothing fills the role.
	Scope            string    `json:"scope,omitempty"`
	AutomaticEnabled bool      `json:"automatic_enabled"`
	Workflow         *Workflow `json:"workflow,omitempty"`
}

// CollectionRoles reports which workflow fills each role for a collection.
func (s *Service) CollectionRoles(ctx context.Context, subject, collectionID string) ([]RoleState, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]RoleState, error) {
		out := []RoleState{}
		c, err := t.Authorize(ctx, subject, collectionID, "read")
		if err != nil {
			return nil, err
		}
		if c.Kind != "list" && c.Kind != "library" {
			return nil, dms.Invalid("roles belong to lists and libraries")
		}
		if err = workspace(ctx, t, subject, c.WorkspaceID, "read"); err != nil {
			return nil, err
		}
		for _, r := range Roles() {
			st := RoleState{Role: r}
			rows, err := listWorkflows(ctx, t, workflows.Filter{WorkspaceID: c.WorkspaceID, Role: r.Key, CollectionID: c.ID, AutomaticProvider: boolPtr(true), LiveOnly: true, Order: "provider"})
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				selected := rows[0]
				for _, candidate := range rows {
					if candidate.AutomaticEnabled && candidate.State == workflows.StatePublished {
						selected = candidate
						break
					}
				}
				v, err := current(ctx, t, selected)
				if err != nil {
					return nil, err
				}
				w, err := toWorkflow(selected, v)
				if err != nil {
					return nil, err
				}
				st.Scope, st.AutomaticEnabled, st.Workflow = "collection", selected.AutomaticEnabled && selected.State == workflows.StatePublished, &w
			}
			out = append(out, st)
		}
		return out, nil
	})
}

// checkRole validates a new workflow's role and scope: a known role, a
// definition whose outputs fill it, and a collection of the workspace.
func checkRole(ctx context.Context, t *dms.Service, subject, workspaceID, role, collectionID string, def Definition) error {
	if role == "" {
		if collectionID != "" {
			return dms.Invalid("collection_id scopes a workflow that fills a role; give role")
		}
		return nil
	}
	r := roles[role]
	if r == nil {
		return dms.Invalid("unknown role " + role)
	}
	if err := r.fills(def); err != nil {
		return err
	}
	if collectionID == "" {
		return nil
	}
	c, err := t.Authorize(ctx, subject, collectionID, "manage")
	if err != nil {
		return err
	}
	if c.WorkspaceID != workspaceID || c.Kind != "list" && c.Kind != "library" {
		return dms.Invalid("collection_id must be a list or library of the workspace")
	}
	return nil
}
