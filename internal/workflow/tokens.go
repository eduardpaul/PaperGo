package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tokenPattern matches {source:path} and {now}/{today}. Sources:
//
//	item     the run's item: id, name, tags, values.<field>, collection_id, ...
//	var      run variables
//	step     node outputs: {step:<node>.<path>}
//	trigger  the event that started the run: type, actor, data.<key>, ...
//	input    manual launch inputs
//	run      id, workflow_id, workflow_key, version, actor
var tokenPattern = regexp.MustCompile(`\{\{|\}\}|\{(item|var|step|trigger|input|run):([^{}]+)\}|\{(now|today)\}`)

// scope is what tokens can read while a node runs.
type scope struct {
	ctx     context.Context
	trigger map[string]any
	inputs  map[string]any
	vars    map[string]any
	steps   map[string]any
	run     map[string]any
	now     time.Time
	// item loads the run's item once, when a token or activity needs it.
	item func(context.Context) (map[string]any, error)
}

func (s *scope) lookup(source, path string) (any, error) {
	switch source {
	case "item":
		if s.item == nil {
			return nil, fmt.Errorf("{item:%s}: the run has no item", path)
		}
		item, err := s.item(s.ctx)
		if err != nil {
			return nil, err
		}
		return walk(item, path), nil
	case "var":
		return walk(s.vars, path), nil
	case "step":
		node, rest, _ := strings.Cut(path, ".")
		out, ok := s.steps[node]
		if !ok {
			return nil, nil
		}
		if rest == "" {
			return out, nil
		}
		return walk(out, rest), nil
	case "trigger":
		return walk(s.trigger, path), nil
	case "input":
		return walk(s.inputs, path), nil
	case "run":
		return walk(s.run, path), nil
	case "now":
		return s.now.UTC().Format(time.RFC3339), nil
	case "today":
		return s.now.UTC().Format("2006-01-02"), nil
	}
	return nil, nil
}

// walk follows a dotted path through maps and arrays.
func walk(v any, path string) any {
	if path == "" {
		return v
	}
	for _, part := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			v = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}

// resolve expands tokens in v. A string that is exactly one token takes the
// token's value with its JSON type (a number stays a number); other strings
// get the values as text. Maps and arrays are resolved recursively.
func (s *scope) resolve(v any) (any, error) {
	switch node := v.(type) {
	case string:
		return s.resolveString(node)
	case map[string]any:
		out := make(map[string]any, len(node))
		for k, child := range node {
			r, err := s.resolve(child)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(node))
		for i, child := range node {
			r, err := s.resolve(child)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

func (s *scope) resolveString(text string) (any, error) {
	if m := tokenPattern.FindStringSubmatchIndex(text); m != nil && m[0] == 0 && m[1] == len(text) && text != "{{" && text != "}}" {
		source, path := tokenParts(text, m)
		return s.lookup(source, path)
	}
	var err error
	out := tokenPattern.ReplaceAllStringFunc(text, func(token string) string {
		switch token {
		case "{{":
			return "{"
		case "}}":
			return "}"
		}
		m := tokenPattern.FindStringSubmatchIndex(token)
		source, path := tokenParts(token, m)
		v, e := s.lookup(source, path)
		if e != nil && err == nil {
			err = e
		}
		return text2(v)
	})
	return out, err
}

func tokenParts(text string, m []int) (string, string) {
	if m[2] >= 0 {
		return text[m[2]:m[3]], text[m[4]:m[5]]
	}
	return text[m[6]:m[7]], ""
}

// text2 renders a token value inside text.
func text2(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = text2(e)
		}
		return strings.Join(parts, ", ")
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// text resolves v and requires text.
func (s *scope) text(v any) (string, error) {
	r, err := s.resolve(v)
	if err != nil {
		return "", err
	}
	switch t := r.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	}
	return text2(r), nil
}
