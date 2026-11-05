// Package pickers reads the x-papergo pickers of a form: the properties
// whose values are IDs of PaperGo data. Forms check the IDs a picker
// selects; workspace packages follow them to what they name and give them
// the IDs of a copy.
package pickers

import (
	"sort"
	"strconv"
)

// Picker is one x-papergo picker of a form, with the IDs its value selects.
// A string property selects one ID, an array of strings several.
type Picker struct {
	Path    string
	Kind    string
	Options map[string]any
	IDs     []string
}

// Read collects the pickers of schema s in a stable order; with values, it
// collects the IDs they select too. Paths start with path.
func Read(s map[string]any, values any, path string) []Picker {
	var out []Picker
	read(s, values, path, &out)
	return out
}

func read(s map[string]any, values any, path string, out *[]Picker) {
	if d, ok := s["x-papergo"].(map[string]any); ok {
		kind, _ := d["kind"].(string)
		p := Picker{Path: path, Kind: kind, Options: d}
		switch v := values.(type) {
		case string:
			if v != "" {
				p.IDs = []string{v}
			}
		case []any:
			for _, e := range v {
				if id, ok := e.(string); ok {
					p.IDs = append(p.IDs, id)
				}
			}
		}
		*out = append(*out, p)
		return
	}
	props, _ := s["properties"].(map[string]any)
	obj, _ := values.(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child, _ := props[name].(map[string]any)
		read(child, obj[name], path+"."+name, out)
	}
	if extra, ok := s["additionalProperties"].(map[string]any); ok {
		if obj != nil {
			extras := make([]string, 0, len(obj))
			for name := range obj {
				if props[name] == nil {
					extras = append(extras, name)
				}
			}
			sort.Strings(extras)
			for _, name := range extras {
				read(extra, obj[name], path+"."+name, out)
			}
		} else {
			read(extra, nil, path+".*", out)
		}
	}
	if item, ok := s["items"].(map[string]any); ok {
		if list, ok := values.([]any); ok {
			for i, e := range list {
				read(item, e, path+"["+strconv.Itoa(i)+"]", out)
			}
		} else if values == nil {
			read(item, nil, path+"[]", out)
		}
	}
}

// Map returns values with each ID a picker of schema s selects replaced by
// fn of the picker's kind and the ID: exactly the IDs Read reports, an
// empty string in an array among them. Values outside pickers stay as they
// are, whatever they hold.
func Map(s map[string]any, values any, fn func(kind, id string) string) any {
	if d, ok := s["x-papergo"].(map[string]any); ok {
		kind, _ := d["kind"].(string)
		switch v := values.(type) {
		case string:
			if v != "" {
				return fn(kind, v)
			}
		case []any:
			out := make([]any, len(v))
			for i, e := range v {
				out[i] = e
				if id, ok := e.(string); ok {
					out[i] = fn(kind, id)
				}
			}
			return out
		}
		return values
	}
	switch v := values.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		extra, _ := s["additionalProperties"].(map[string]any)
		out := make(map[string]any, len(v))
		for name, value := range v {
			if child, ok := props[name].(map[string]any); ok {
				out[name] = Map(child, value, fn)
			} else if props[name] == nil && extra != nil {
				out[name] = Map(extra, value, fn)
			} else {
				out[name] = value
			}
		}
		return out
	case []any:
		item, ok := s["items"].(map[string]any)
		if !ok {
			return v
		}
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = Map(item, e, fn)
		}
		return out
	}
	return values
}
