package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Contribution is a compiled set of tools a deployment may enable by its
// ID. Its tools are named with the ID, dots and hyphens replaced with "_",
// followed by "_".
type Contribution struct {
	ID    string
	Tools []Tool
}

// Registered is a tool the registry accepted, with its compiled input
// schema.
type Registered struct {
	Tool
	input *jsonschema.Resolved
}

// Registry is the frozen set of tools a server serves: the built-ins and
// the enabled contributions. It is safe for concurrent use.
type Registry struct {
	tools []*Registered
	named map[string]*Registered
}

var (
	toolName       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	contributionID = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)
)

// NewRegistry validates the built-ins and every contribution, enabled or
// not, and keeps the built-ins and the contributions enabled names. An
// invalid or duplicate name, a contribution whose prefix collides with
// another's, a tool without scopes or with an invalid schema, and an
// enabled ID no contribution has are errors.
func NewRegistry(builtins []Tool, contributions []Contribution, enabled []string) (*Registry, error) {
	r := &Registry{named: map[string]*Registered{}}
	names := map[string]string{}
	add := func(owner string, t Tool, keep bool) error {
		if !toolName.MatchString(t.Name) {
			return fmt.Errorf("%s: tool name %q must be 1 to 64 letters, digits, _ or -", owner, t.Name)
		}
		if other, taken := names[t.Name]; taken {
			return fmt.Errorf("%s: tool %s is also %s's", owner, t.Name, other)
		}
		names[t.Name] = owner
		reg, err := compile(t)
		if err != nil {
			return fmt.Errorf("%s: tool %s: %w", owner, t.Name, err)
		}
		if keep {
			r.tools = append(r.tools, reg)
			r.named[t.Name] = reg
		}
		return nil
	}
	for _, t := range builtins {
		if err := add("built-in", t, true); err != nil {
			return nil, err
		}
	}
	prefixes := map[string]string{}
	ids := map[string]bool{}
	for _, c := range contributions {
		if !contributionID.MatchString(c.ID) {
			return nil, fmt.Errorf("contribution ID %q must be lowercase letters and digits separated by single dots or hyphens", c.ID)
		}
		prefix := Prefix(c.ID)
		if other, taken := prefixes[prefix]; taken {
			return nil, fmt.Errorf("contributions %s and %s both name their tools %s", other, c.ID, prefix)
		}
		prefixes[prefix], ids[c.ID] = c.ID, true
		keep := slices.Contains(enabled, c.ID)
		for _, t := range c.Tools {
			if !strings.HasPrefix(t.Name, prefix) || len(t.Name) == len(prefix) {
				return nil, fmt.Errorf("contribution %s: tool %q must be named %s…", c.ID, t.Name, prefix)
			}
			if err := add("contribution "+c.ID, t, keep); err != nil {
				return nil, err
			}
		}
	}
	for _, id := range enabled {
		if !ids[id] {
			return nil, fmt.Errorf("no compiled contribution is %q", id)
		}
	}
	slices.SortFunc(r.tools, func(a, b *Registered) int { return strings.Compare(a.Name, b.Name) })
	return r, nil
}

// Prefix is the name prefix of a contribution's tools.
func Prefix(id string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(id) + "_"
}

// Tools are the registered tools, by name.
func (r *Registry) Tools() []*Registered { return slices.Clone(r.tools) }

// Tool is the registered tool named name, or nil.
func (r *Registry) Tool(name string) *Registered { return r.named[name] }

func compile(t Tool) (*Registered, error) {
	if len(t.Scopes) == 0 || slices.Contains(t.Scopes, "") {
		return nil, errors.New("tools need at least one required scope")
	}
	if t.Call == nil {
		return nil, errors.New("tools need a Call")
	}
	var input jsonschema.Schema
	if err := json.Unmarshal(t.InputSchema, &input); err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	if input.Type != "object" || input.AdditionalProperties == nil || !isFalse(input.AdditionalProperties) {
		return nil, errors.New(`input schema must be an object with "additionalProperties": false`)
	}
	resolved, err := input.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	if t.OutputSchema != nil {
		var output jsonschema.Schema
		if err := json.Unmarshal(t.OutputSchema, &output); err != nil {
			return nil, fmt.Errorf("output schema: %w", err)
		}
		if _, err := output.Resolve(nil); err != nil {
			return nil, fmt.Errorf("output schema: %w", err)
		}
	}
	return &Registered{Tool: t, input: resolved}, nil
}

// isFalse reports whether a schema is the false schema.
func isFalse(s *jsonschema.Schema) bool {
	raw, err := json.Marshal(s)
	return err == nil && bytes.Equal(raw, []byte("false"))
}

// Validate checks arguments against the tool's input schema and returns
// them, absent arguments as an empty object.
func (t *Registered) Validate(arguments json.RawMessage) (json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(arguments); len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		arguments = json.RawMessage(`{}`)
	}
	var instance any
	if err := json.Unmarshal(arguments, &instance); err != nil {
		return nil, Fail(CodeInvalidArguments, "arguments must be a JSON object")
	}
	if err := t.input.Validate(instance); err != nil {
		return nil, Fail(CodeInvalidArguments, err.Error())
	}
	return arguments, nil
}
