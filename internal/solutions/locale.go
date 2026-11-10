package solutions

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// LocalesDir holds a solution's display names in other languages, one file
// per language: locales/<language>.json.
const LocalesDir = "locales"

var languageCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// Locale is a solution's display names in one language: what people read.
// Keys, references and choice values are the solution's and have no
// language. What a locale leaves out keeps the solution's own text.
type Locale struct {
	Name         string                    `json:"name,omitempty"`
	Description  string                    `json:"description,omitempty"`
	Settings     map[string]SettingText    `json:"settings,omitempty"`
	Fields       map[string]FieldText      `json:"fields,omitempty"`
	ContentTypes map[string]string         `json:"content_types,omitempty"`
	Collections  map[string]CollectionText `json:"collections,omitempty"`
	Workflows    map[string]ComponentText  `json:"workflows,omitempty"`
	Apps         map[string]ComponentText  `json:"apps,omitempty"`
}

// SettingText is a setting's title and description in a language.
type SettingText struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// FieldText is a field's label and description in a language.
type FieldText struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// CollectionText is a collection's name and its views' names, by view key.
type CollectionText struct {
	Name  string            `json:"name,omitempty"`
	Views map[string]string `json:"views,omitempty"`
}

// ComponentText is a workflow's or an app's name and description.
type ComponentText struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Languages are the languages a solution has display names in, sorted.
func (s Solution) Languages() []string {
	out := make([]string, 0, len(s.Locales))
	for language := range s.Locales {
		out = append(out, language)
	}
	sort.Strings(out)
	return out
}

// language picks the solution's locale for a language: the language
// itself, else its base (es for es-MX). Empty when it has neither.
func (s Solution) language(wanted string) string {
	if _, ok := s.Locales[wanted]; ok {
		return wanted
	}
	if base, _, regional := strings.Cut(wanted, "-"); regional {
		if _, ok := s.Locales[base]; ok {
			return base
		}
	}
	return ""
}

// Localized returns the solution as people of a language read it: a copy
// with the display names of its locale for that language, and the locale
// used. Without one it is the solution as it is, and "".
func (s Solution) Localized(wanted string) (Solution, string) {
	used := s.language(wanted)
	if used == "" {
		return s, ""
	}
	l := s.Locales[used]
	out := s
	// A deep copy of the manifest: its slices and maps are changed below.
	raw, _ := json.Marshal(s.Manifest)
	out.Manifest = Manifest{}
	_ = json.Unmarshal(raw, &out.Manifest)
	m := &out.Manifest
	pick := func(text, fallback string) string {
		if text != "" {
			return text
		}
		return fallback
	}
	m.Name, m.Description = pick(l.Name, m.Name), pick(l.Description, m.Description)
	if props, ok := m.Settings["properties"].(map[string]any); ok {
		for name, text := range l.Settings {
			if p, ok := props[name].(map[string]any); ok {
				if text.Title != "" {
					p["title"] = text.Title
				}
				if text.Description != "" {
					p["description"] = text.Description
				}
			}
		}
	}
	for i := range m.Fields {
		if text, ok := l.Fields[m.Fields[i].Key]; ok {
			m.Fields[i].Label = pick(text.Label, m.Fields[i].Label)
			m.Fields[i].Options.Description = pick(text.Description, m.Fields[i].Options.Description)
		}
	}
	for i := range m.ContentTypes {
		m.ContentTypes[i].Name = pick(l.ContentTypes[m.ContentTypes[i].Key], m.ContentTypes[i].Name)
	}
	for i := range m.Collections {
		text := l.Collections[m.Collections[i].Key]
		m.Collections[i].Name = pick(text.Name, m.Collections[i].Name)
		for j := range m.Collections[i].Views {
			m.Collections[i].Views[j].Name = pick(text.Views[m.Collections[i].Views[j].Key], m.Collections[i].Views[j].Name)
		}
	}
	out.Workflows = append([]Workflow(nil), s.Workflows...)
	for i := range out.Workflows {
		text := l.Workflows[out.Workflows[i].Key]
		out.Workflows[i].Name, out.Workflows[i].Description = pick(text.Name, out.Workflows[i].Name), pick(text.Description, out.Workflows[i].Description)
	}
	out.Apps = append([]App(nil), s.Apps...)
	for i := range out.Apps {
		text := l.Apps[out.Apps[i].Key]
		out.Apps[i].Name, out.Apps[i].Description = pick(text.Name, out.Apps[i].Name), pick(text.Description, out.Apps[i].Description)
	}
	return out, used
}

// checkLocales finds the names a locale gives to what the solution lacks.
func (c *checker) locales() {
	for _, language := range c.s.Languages() {
		l, file := c.s.Locales[language], LocalesDir+"/"+language+".json"
		for name := range l.Settings {
			if _, ok := c.settings[name]; !ok {
				c.add(file, "settings."+name, "is not a setting of the solution")
			}
		}
		for key := range l.Fields {
			if _, ok := c.fields[key]; !ok {
				c.add(file, "fields."+key, "is not a field of the solution")
			}
		}
		for key := range l.ContentTypes {
			if _, ok := c.types[key]; !ok {
				c.add(file, "content_types."+key, "is not a content type of the solution")
			}
		}
		for key, text := range l.Collections {
			slot, ok := c.slots[key]
			if !ok {
				c.add(file, "collections."+key, "is not a collection of the solution")
				continue
			}
			views := map[string]bool{}
			for _, v := range slot.Views {
				views[v.Key] = true
			}
			for view := range text.Views {
				if !views[view] {
					c.add(file, "collections."+key+".views."+view, "is not a view of the collection")
				}
			}
		}
		workflows, apps := map[string]bool{}, map[string]bool{}
		for _, w := range c.s.Workflows {
			workflows[w.Key] = true
		}
		for _, a := range c.s.Apps {
			apps[a.Key] = true
		}
		for key := range l.Workflows {
			if !workflows[key] {
				c.add(file, "workflows."+key, "is not a workflow of the solution")
			}
		}
		for key := range l.Apps {
			if !apps[key] {
				c.add(file, "apps."+key, "is not an app of the solution")
			}
		}
	}
}
