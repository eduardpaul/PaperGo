package workflow

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"papergo/ent"
	"papergo/ent/resource"
	"papergo/ent/workflow"
	"papergo/internal/dms"
)

// Role is a product process that a workflow fills, such as search.index.
// A role's built-in fills it by default; copies and alternatives fill it
// instead, one enabled filler per scope: the workspace, or one collection,
// which takes precedence for its own items. A filler declares outputs
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
func claimRole(ctx context.Context, t *dms.Service, subject string, w *ent.Workflow) error {
	if w.Role == nil || !w.Enabled {
		return nil
	}
	q := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(w.WorkspaceID), workflow.RoleEQ(*w.Role), workflow.EnabledEQ(true), workflow.DeletedAtIsNil(), workflow.IDNEQ(w.ID))
	if w.CollectionID != nil {
		q.Where(workflow.CollectionIDEQ(*w.CollectionID))
	} else {
		q.Where(workflow.CollectionIDIsNil())
	}
	others, err := q.All(ctx)
	if err != nil {
		return err
	}
	for _, other := range others {
		other, _, err = revise(ctx, t, subject, other, mustCurrent(ctx, t, other), func(u *ent.WorkflowUpdateOne) { u.SetEnabled(false) })
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

// overridden reports whether the collection has its own filler of the role,
// enabled or not. Then the workspace's fillers do not run for its items, and
// a disabled filler turns the role off there.
func overridden(ctx context.Context, t *dms.Service, workspaceID, role, collectionID string) (bool, error) {
	if collectionID == "" {
		return false, nil
	}
	return t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.RoleEQ(role), workflow.CollectionIDEQ(collectionID), workflow.DeletedAtIsNil()).Exist(ctx)
}

// OverriddenCollections lists the collections of the workspace that have
// their own filler of the role, enabled or not: work a workspace-wide filler
// does across collections skips them.
func OverriddenCollections(ctx context.Context, t *dms.Service, workspaceID, role string) ([]string, error) {
	rows, err := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.RoleEQ(role), workflow.CollectionIDNotNil(), workflow.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, w := range rows {
		if !slices.Contains(out, *w.CollectionID) {
			out = append(out, *w.CollectionID)
		}
	}
	return out, nil
}

// EnsureDefaults turns on the default built-ins (BuiltIn.Default) in a live
// workspace that has never had them and has no other filler of their role:
// the workspace's creator becomes their author. A built-in turned off,
// copied, replaced or deleted stays as people left it.
func EnsureDefaults(ctx context.Context, t *dms.Service, workspaceID string) error {
	ws, err := t.Client.Resource.Query().Where(resource.IDEQ(workspaceID), resource.KindEQ(resource.KindWorkspace), resource.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, b := range BuiltIns() {
		if !b.Default {
			continue
		}
		had, err := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(workspaceID), workflow.Or(workflow.BuiltinKeyEQ(b.Key), workflow.RoleEQ(b.Role))).Exist(ctx)
		if err != nil {
			return err
		}
		if had {
			continue
		}
		if _, _, err = createBuiltIn(ctx, t, ws.CreatedBy, workspaceID, b, nil, true, map[string]any{}); err != nil {
			return fmt.Errorf("turn on %s: %w", b.Key, err)
		}
	}
	return nil
}

// RoleState is who fills a role for a collection: the collection's own
// filler, else the workspace's.
type RoleState struct {
	Role *Role `json:"role"`
	// Scope is collection or workspace, where the filler comes from; empty
	// when nothing fills the role.
	Scope    string    `json:"scope,omitempty"`
	Enabled  bool      `json:"enabled"`
	Workflow *Workflow `json:"workflow,omitempty"`
}

// CollectionRoles reports which workflow fills each role for a collection.
func (s *Service) CollectionRoles(ctx context.Context, subject, collectionID string) ([]RoleState, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]RoleState, error) {
		out := []RoleState{}
		c, err := t.Authorize(ctx, subject, collectionID, "read")
		if err != nil {
			return nil, err
		}
		if c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
			return nil, dms.Invalid("roles belong to lists and libraries")
		}
		if err = workspace(ctx, t, subject, c.WorkspaceID, "read"); err != nil {
			return nil, err
		}
		for _, r := range Roles() {
			st := RoleState{Role: r}
			for _, scope := range []string{"collection", "workspace"} {
				q := t.Client.Workflow.Query().Where(workflow.WorkspaceIDEQ(c.WorkspaceID), workflow.RoleEQ(r.Key), workflow.DeletedAtIsNil())
				if scope == "collection" {
					q.Where(workflow.CollectionIDEQ(c.ID))
				} else {
					q.Where(workflow.CollectionIDIsNil())
				}
				rows, err := q.Order(ent.Desc(workflow.FieldEnabled), ent.Asc(workflow.FieldID)).All(ctx)
				if err != nil {
					return nil, err
				}
				if len(rows) == 0 {
					continue
				}
				v, err := current(ctx, t, rows[0])
				if err != nil {
					return nil, err
				}
				w, err := toWorkflow(rows[0], v)
				if err != nil {
					return nil, err
				}
				st.Scope, st.Enabled, st.Workflow = scope, rows[0].Enabled, &w
				break
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
	if c.WorkspaceID != workspaceID || c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
		return dms.Invalid("collection_id must be a list or library of the workspace")
	}
	return nil
}
