package solutions

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"papergo/internal/dms"
)

// Part is what a feature adds to solutions, under its Name in a manifest's
// parts, such as a solution's connections. Each object is installed and
// updated as the solution's own workflows and apps are: created when the
// workspace lacks it, updated when the solution changes it, and kept when
// someone changed it in PaperGo and the solution did not. Objects of a part
// are never pruned: they hold what people configured.
type Part struct {
	Name string
	// Objects reads a manifest's part: each object's key, name and what the
	// solution wants it to be, which may hold $collection:, $type: and
	// $setting: references. It is called with references resolved, and
	// with stand-ins for them when a solution is checked.
	Objects func(raw json.RawMessage) ([]PartObject, error)
	// Find returns a workspace's object by key, its ID and state as Create
	// and Update return them; found is false when it has none.
	Find func(ctx context.Context, t *dms.Service, subject, workspaceID, key string) (id string, state map[string]any, found bool, err error)
	// Create makes the object, and says what is left to do by hand, such
	// as giving a connection its address.
	Create func(ctx context.Context, t *dms.Service, subject, workspaceID string, o PartObject) (id string, state map[string]any, todo string, err error)
	// Update makes an object what the solution now wants.
	Update func(ctx context.Context, t *dms.Service, subject, id string, o PartObject) (state map[string]any, err error)
}

// PartObject is one object of a part.
type PartObject struct {
	Key  string         `json:"key"`
	Name string         `json:"name"`
	Want map[string]any `json:"want"`
}

var parts = map[string]Part{}

// RegisterPart adds a feature's part to solutions, replacing one of the
// same name.
func RegisterPart(p Part) { parts[p.Name] = p }

func partNames(m Manifest) []string {
	names := make([]string, 0, len(m.Parts))
	for name := range m.Parts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parts checks a manifest's parts: their references always, and their
// objects when this PaperGo serves the part; applying refuses a part it
// does not serve.
func (c *checker) parts() {
	for _, name := range partNames(c.s.Manifest) {
		raw := c.s.Manifest.Parts[name]
		at := "parts." + name
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			c.add(ManifestFile, at, "is not JSON: %v", err)
			continue
		}
		c.references(ManifestFile, at, value)
		part, served := parts[name]
		if !served {
			continue
		}
		resolved, _ := json.Marshal(c.resolve(value))
		if _, err := part.Objects(resolved); err != nil {
			c.add(ManifestFile, at, "%v", err)
		}
	}
}

// parts installs the objects of the manifest's parts.
func (r *run) parts() error {
	for _, name := range partNames(r.base.Manifest) {
		part, served := parts[name]
		if !served {
			r.refuse(name, "", "this PaperGo does not serve the %s of solutions", name)
			continue
		}
		var list []any
		if err := json.Unmarshal(r.base.Manifest.Parts[name], &list); err != nil {
			return failed(name, "", fmt.Errorf("parts.%s is a list of objects", name))
		}
		resolved, ok := r.resolve(name, "", map[string]any{"list": list})
		if !ok {
			continue
		}
		raw, _ := json.Marshal(resolved["list"])
		objects, err := part.Objects(raw)
		if err != nil {
			return failed(name, "", err)
		}
		for _, o := range objects {
			id, live, found, err := part.Find(r.ctx, r.t, r.subject, r.workspaceID, o.Key)
			if err != nil {
				return failed(name, o.Key, err)
			}
			action, kept := r.decide(name, o.Key, o.Want, live, found)
			switch action {
			case doCreate:
				created, state, todo, err := part.Create(r.ctx, r.t, r.subject, r.workspaceID, o)
				if err != nil {
					return failed(name, o.Key, err)
				}
				r.change("create", name, o.Key, o.Name)
				if todo != "" {
					r.warn("%s", todo)
				}
				id, live = created, state
			case doUpdate:
				state, err := part.Update(r.ctx, r.t, r.subject, id, o)
				if err != nil {
					return failed(name, o.Key, err)
				}
				r.change("update", name, o.Key, differences(live, o.Want))
				live = state
			}
			r.note(name, o.Key, id, RoleOwns, o.Want, live, kept)
		}
	}
	return nil
}
