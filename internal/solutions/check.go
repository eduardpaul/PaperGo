package solutions

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"

	"papergo/internal/appcode"
	"papergo/internal/appicon"
	"papergo/internal/dal/catalog"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/forms/pickers"
	"papergo/internal/workflow"
)

// Capabilities are what a solution may require of a deployment.
var Capabilities = []string{"ai.batch", "ai.chat", "embeddings", "ocr", "search"}

// organizationDefaults are the organization preferences a setting may take
// its default from, each with the value that stands for it while a solution
// is checked.
var organizationDefaults = map[string]string{
	"time_zone": "UTC", "language": "en", "default_currency": "USD", "number_format": "en", "date_format": "yyyy-MM-dd", "time_format": "24h",
}

var (
	solutionKey  = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)
	schemaKey    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	componentKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,89}$`)
	slotKey      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	anyID        = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// Check finds everything wrong with a solution that needs no server: its
// keys, its references, and whether each workflow and app compiles. A
// solution without problems can be packed.
func Check(ctx context.Context, s Solution) Problems {
	c := checker{ctx: ctx, s: s, slots: map[string]Collection{}, fields: map[string]Field{}, types: map[string]ContentType{}, foreign: map[string]bool{}, settings: map[string]map[string]any{}}
	c.manifest()
	c.parts()
	c.workflows()
	c.apps()
	c.locales()
	return c.problems
}

type checker struct {
	ctx      context.Context
	s        Solution
	problems Problems
	slots    map[string]Collection
	fields   map[string]Field
	types    map[string]ContentType
	// foreign are the content types of other solutions the slots expect.
	foreign  map[string]bool
	settings map[string]map[string]any
}

func (c *checker) add(file, at, format string, args ...any) {
	c.problems.add(file, at, format, args...)
}

func (c *checker) manifest() {
	m, file := c.s.Manifest, ManifestFile
	if !solutionKey.MatchString(m.Key) {
		c.add(file, "key", "%q must be 2 to 32 lowercase letters and digits, starting with a letter", m.Key)
	}
	if strings.TrimSpace(m.Name) == "" {
		c.add(file, "name", "is required")
	}
	if _, err := semver.StrictNewVersion(m.Version); err != nil {
		c.add(file, "version", "%q must be MAJOR.MINOR.PATCH, such as 1.2.0", m.Version)
	}
	if m.PaperGo != "" {
		if _, err := semver.NewConstraint(m.PaperGo); err != nil {
			c.add(file, "papergo", "%q is not a range of versions, such as >=0.2.0", m.PaperGo)
		}
	}
	for _, name := range sortedKeys(m.Requires) {
		if !contains(Capabilities, name) {
			c.add(file, "requires."+name, "is not a capability; they are %s", strings.Join(Capabilities, ", "))
		}
	}
	c.settingsForm()
	c.schema()
	c.collections()
	raw, _ := json.Marshal(m)
	if id := anyID.FindString(string(raw)); id != "" {
		c.add(file, "", "holds the ID %s; a solution names what it declares with %s<key> and %s<key>", id, RefCollection, RefType)
	}
}

func (c *checker) settingsForm() {
	m := c.s.Manifest
	if m.Settings == nil {
		return
	}
	props, _ := m.Settings["properties"].(map[string]any)
	// The form is checked with the stand-ins of its organization defaults,
	// which a form's own rules would refuse as values of their type.
	checked := forms.Plain(m.Settings).(map[string]any)
	checkedProps, _ := checked["properties"].(map[string]any)
	for _, name := range sortedKeys(props) {
		p, _ := props[name].(map[string]any)
		c.settings[name] = p
		def, ok := p["default"].(string)
		if !ok || !strings.HasPrefix(def, OrganizationDefault) {
			continue
		}
		preference := strings.TrimPrefix(def, OrganizationDefault)
		stand, known := organizationDefaults[preference]
		if !known {
			c.add(ManifestFile, "settings.properties."+name+".default", "%q is not an organization preference; they are %s", preference, strings.Join(sortedKeys(organizationDefaults), ", "))
			continue
		}
		if p["type"] != "string" {
			c.add(ManifestFile, "settings.properties."+name+".default", "an organization preference is text, so the setting's type must be string")
			continue
		}
		checkedProps[name].(map[string]any)["default"] = stand
	}
	if err := forms.Validate(checked, "settings"); err != nil {
		c.add(ManifestFile, "", "%s", err)
	}
}

func (c *checker) schema() {
	m, file := c.s.Manifest, ManifestFile
	for i, f := range m.Fields {
		at := fmt.Sprintf("fields[%d]", i)
		c.ownKey(file, at+".key", f.Key, schemaKey, "_")
		if _, dup := c.fields[f.Key]; dup {
			c.add(file, at+".key", "%q is declared twice", f.Key)
		}
		c.fields[f.Key] = f
		if strings.TrimSpace(f.Label) == "" {
			c.add(file, at+".label", "is required")
		}
		if !f.Type.Valid() {
			c.add(file, at+".type", "%q is not a field type", f.Type)
		}
		if (f.Type == catalog.FieldChoice) != (len(f.Choices) > 0) {
			c.add(file, at+".choices", "a choice field lists its choices, and only a choice field does")
		}
		if dup := duplicate(f.Choices); dup != "" {
			c.add(file, at+".choices", "%q is listed twice", dup)
		}
		if f.Options.TermSetID != "" {
			c.add(file, at+".options.term_set_id", "a solution cannot name a term set")
		}
		if ref := f.Options.LookupContainerID; ref != "" && !strings.HasPrefix(ref, RefCollection) {
			c.add(file, at+".options.lookup_container_id", "must name a collection of the solution as %s<key>", RefCollection)
		}
	}
	for i, t := range m.ContentTypes {
		at := fmt.Sprintf("content_types[%d]", i)
		c.ownKey(file, at+".key", t.Key, schemaKey, "_")
		if _, dup := c.types[t.Key]; dup {
			c.add(file, at+".key", "%q is declared twice", t.Key)
		}
		c.types[t.Key] = t
		if strings.TrimSpace(t.Name) == "" {
			c.add(file, at+".name", "is required")
		}
		if dup := duplicate(t.Fields); dup != "" {
			c.add(file, at+".fields", "%q is listed twice", dup)
		}
		for j, key := range t.Fields {
			if _, ok := c.fields[key]; !ok {
				c.add(file, fmt.Sprintf("%s.fields[%d]", at, j), "%q is not a field of the solution", key)
			}
		}
	}
	names := map[string]bool{}
	for i, r := range m.RelationshipTypes {
		name := strings.TrimSpace(r.Name)
		if name == "" || names[strings.ToLower(name)] {
			c.add(file, fmt.Sprintf("relationship_types[%d].name", i), "is required and unique")
		}
		names[strings.ToLower(name)] = true
	}
}

// ownKey checks a key the solution owns: its form, and that it starts with
// the solution's key.
func (c *checker) ownKey(file, at, key string, form *regexp.Regexp, separator string) {
	if !form.MatchString(key) {
		c.add(file, at, "%q is not a key: lowercase letters, digits and %q, starting with a letter", key, separator)
		return
	}
	if prefix := c.s.Manifest.Key + separator; !strings.HasPrefix(key, prefix) || key == prefix {
		c.add(file, at, "%q must start with %q: everything a solution owns starts with its key", key, prefix)
	}
}

func (c *checker) collections() {
	m, file := c.s.Manifest, ManifestFile
	// Slots and the types they expect first: views and lookups name them.
	for i, col := range m.Collections {
		at := fmt.Sprintf("collections[%d]", i)
		if !slotKey.MatchString(col.Key) {
			c.add(file, at+".key", "%q is not a key: lowercase letters, digits, - and _, starting with a letter", col.Key)
		}
		if _, dup := c.slots[col.Key]; dup {
			c.add(file, at+".key", "%q is declared twice", col.Key)
		}
		c.slots[col.Key] = col
		for key := range col.Expects {
			c.foreign[key] = true
		}
	}
	for i, f := range m.Fields {
		if ref := f.Options.LookupContainerID; strings.HasPrefix(ref, RefCollection) {
			if _, ok := c.slots[strings.TrimPrefix(ref, RefCollection)]; !ok {
				c.add(file, fmt.Sprintf("fields[%d].options.lookup_container_id", i), "%s is not a collection of the solution", ref)
			}
		}
	}
	for i, col := range m.Collections {
		at := fmt.Sprintf("collections[%d]", i)
		if col.Kind != "list" && col.Kind != "library" {
			c.add(file, at+".kind", "%q must be list or library", col.Kind)
		}
		// available are the fields a view of the slot may show.
		available := map[string]bool{}
		if col.Owned() {
			if strings.TrimSpace(col.Name) == "" {
				c.add(file, at+".name", "is required: the collection is created with it")
			}
			if len(col.ContentTypes) == 0 {
				c.add(file, at+".content_types", "names the content types the collection accepts; a collection of another solution names what it expects instead")
			}
			if col.WebDAVEnabled && col.Kind != "library" {
				c.add(file, at+".webdav_enabled", "is a library's setting; a list has none")
			}
			if len(col.Extends) > 0 {
				c.add(file, at+".extends", "is for a collection of another solution; add the fields to the solution's own types")
			}
			if dup := duplicate(col.ContentTypes); dup != "" {
				c.add(file, at+".content_types", "%q is listed twice", dup)
			}
			for j, key := range col.ContentTypes {
				t, ok := c.types[key]
				if !ok {
					c.add(file, fmt.Sprintf("%s.content_types[%d]", at, j), "%q is not a content type of the solution", key)
				}
				for _, f := range t.Fields {
					available[f] = true
				}
			}
		} else {
			c.foreignSlot(at, col, available)
		}
		c.views(at, col, available)
	}
}

// foreignSlot checks a slot whose collection another solution owns.
func (c *checker) foreignSlot(at string, col Collection, available map[string]bool) {
	file, own := ManifestFile, c.s.Manifest.Key+"_"
	if len(col.ContentTypes) > 0 {
		c.add(file, at+".content_types", "belongs to a collection the solution owns; this one names what it expects")
	}
	if col.Search != nil || col.WebDAVEnabled {
		c.add(file, at, "search and webdav_enabled are decided by the collection's owner")
	}
	if u := col.Use; u != nil {
		if !solutionKey.MatchString(u.Solution) || u.Solution == c.s.Manifest.Key {
			c.add(file, at+".use.solution", "%q must be the key of another solution", u.Solution)
		}
		if !slotKey.MatchString(u.Collection) {
			c.add(file, at+".use.collection", "%q is not the key of a collection", u.Collection)
		}
		if u.Version != "" {
			if _, err := semver.NewConstraint(u.Version); err != nil {
				c.add(file, at+".use.version", "%q is not a range of versions, such as >=1.2.0", u.Version)
			}
		}
	}
	if len(col.Expects) == 0 {
		c.add(file, at+".expects", "names the content types and fields the solution relies on in the collection")
	}
	for _, key := range sortedKeys(col.Expects) {
		if !schemaKey.MatchString(key) || strings.HasPrefix(key, own) {
			c.add(file, at+".expects."+key, "must be a content type of another solution")
		}
		for j, f := range col.Expects[key] {
			if _, mine := c.fields[f]; mine || !schemaKey.MatchString(f) {
				c.add(file, fmt.Sprintf("%s.expects.%s[%d]", at, key, j), "%q must be a field of another solution; the solution's own fields go under extends", f)
			}
			available[f] = true
		}
	}
	for _, key := range sortedKeys(col.Extends) {
		if _, ok := col.Expects[key]; !ok {
			c.add(file, at+".extends."+key, "is not a content type the collection is expected to have")
		}
		for j, f := range col.Extends[key] {
			if _, mine := c.fields[f]; !mine {
				c.add(file, fmt.Sprintf("%s.extends.%s[%d]", at, key, j), "%q is not a field of the solution", f)
			}
			available[f] = true
		}
	}
}

func (c *checker) views(at string, col Collection, available map[string]bool) {
	file := ManifestFile
	keys, defaults := map[string]bool{}, 0
	for i, v := range col.Views {
		va := fmt.Sprintf("%s.views[%d]", at, i)
		if !slotKey.MatchString(v.Key) || keys[v.Key] {
			c.add(file, va+".key", "%q must be a key, once in the collection", v.Key)
		}
		keys[v.Key] = true
		if name := strings.TrimSpace(v.Name); name == "" || strings.EqualFold(name, "Archived") {
			c.add(file, va+".name", "is required, and Archived is the built-in view's")
		}
		if v.IsDefault {
			defaults++
		}
		for j, column := range v.Columns {
			if !strings.HasPrefix(column, "$") && !available[column] {
				c.add(file, fmt.Sprintf("%s.columns[%d]", va, j), "%q is not a field of the collection's content types", column)
			}
		}
		c.references(file, va+".query", v.Query)
		// The query's shape is the one views are saved with.
		raw, _ := json.Marshal(map[string]any{"name": v.Name, "columns": v.Columns, "query": c.resolve(v.Query), "layout": v.Layout, "settings": v.Settings, "is_default": v.IsDefault})
		var input dms.ViewInput
		if err := decodeStrict(raw, &input); err != nil {
			c.add(file, va, "%s", err)
		}
	}
	if defaults > 1 {
		c.add(file, at+".views", "one view is the default, not %d", defaults)
	}
}

// references checks every reference in a JSON value.
func (c *checker) references(file, at string, value any) {
	walk(value, at, func(at, text string) {
		switch {
		case strings.HasPrefix(text, RefCollection):
			if _, ok := c.slots[strings.TrimPrefix(text, RefCollection)]; !ok {
				c.add(file, at, "%s is not a collection of the solution", text)
			}
		case strings.HasPrefix(text, RefType):
			key := strings.TrimPrefix(text, RefType)
			if _, ok := c.types[key]; !ok && !c.foreign[key] {
				c.add(file, at, "%s is neither a content type of the solution nor one its collections expect", text)
			}
		case strings.HasPrefix(text, RefSetting):
			if _, ok := c.settings[strings.TrimPrefix(text, RefSetting)]; !ok {
				c.add(file, at, "%s is not a setting of the solution", text)
			}
		default:
			if id := anyID.FindString(text); id != "" {
				c.add(file, at, "holds the ID %s; a solution names what it declares with %s<key> and %s<key>", id, RefCollection, RefType)
			}
		}
	})
}

// resolve returns a JSON value with its references replaced by what stands
// for them while a solution is checked: an ID made from the reference, or a
// setting's default.
func (c *checker) resolve(value any) any {
	switch v := value.(type) {
	case string:
		switch {
		case strings.HasPrefix(v, RefCollection), strings.HasPrefix(v, RefType):
			return StandInID(v)
		case strings.HasPrefix(v, RefSetting):
			return c.settingStandIn(strings.TrimPrefix(v, RefSetting))
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = c.resolve(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = c.resolve(e)
		}
		return out
	}
	return value
}

// StandInID is the ID that stands for a reference while a solution is
// checked: always the same for the same reference.
func StandInID(reference string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("papergo:solution:"+reference)).String()
}

func (c *checker) settingStandIn(name string) any {
	p := c.settings[name]
	if def, ok := p["default"]; ok {
		if text, isText := def.(string); isText && strings.HasPrefix(text, OrganizationDefault) {
			return organizationDefaults[strings.TrimPrefix(text, OrganizationDefault)]
		}
		return def
	}
	switch p["type"] {
	case "number", "integer":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}
	return ""
}

var states = []string{"", "draft", "published", "archived"}

// component checks what workflows and apps share: key, name, state,
// references, and that the source holds no ID.
func (c *checker) component(file, key, name, state, sourceFile, source string, parameters map[string]any, seen map[string]string) {
	c.ownKey(file, "key", key, componentKey, "-")
	if other, dup := seen[key]; dup {
		c.add(file, "key", "%q is also the key of %s", key, other)
	}
	seen[key] = file
	if strings.TrimSpace(name) == "" {
		c.add(file, "name", "is required")
	}
	if !contains(states, state) {
		c.add(file, "state", "%q must be draft, published or archived", state)
	}
	c.references(file, "parameters", map[string]any(parameters))
	if id := anyID.FindString(source); id != "" {
		c.add(path.Join(path.Dir(file), sourceFile), "", "holds the ID %s; read IDs from the parameters instead", id)
	}
}

func (c *checker) workflows() {
	seen := map[string]string{}
	for _, w := range c.s.Workflows {
		c.component(w.File, w.Key, w.Name, w.State, w.Script, w.Source, w.Parameters, seen)
		if w.Source == "" {
			continue
		}
		parameters, _ := c.resolve(map[string]any(w.Parameters)).(map[string]any)
		if _, err := workflow.Check(c.ctx, w.Source, parameters); err != nil {
			c.add(path.Join(path.Dir(w.File), w.Script), "", "%s", strings.TrimPrefix(err.Error(), "source: "))
		}
	}
}

func (c *checker) apps() {
	seen := map[string]string{}
	for _, a := range c.s.Apps {
		c.component(a.File, a.Key, a.Name, a.State, a.SourceFile, a.Source, a.Parameters, seen)
		if a.Icon != "" && !appicon.ValidGlyph(a.Icon) {
			c.add(a.File, "icon", "%q is not one of the glyphs an app may choose", a.Icon)
		}
		if a.Color != "" && !appicon.ValidColor(strings.ToLower(a.Color)) {
			c.add(a.File, "color", "%q must be written as #rrggbb, such as #b45309", a.Color)
		}
		if a.Source == "" {
			continue
		}
		compiled, err := appcode.Compile(c.ctx, a.Source)
		if err != nil {
			c.add(path.Join(path.Dir(a.File), a.SourceFile), "", "%s", err)
			continue
		}
		var form map[string]any
		if err := json.Unmarshal(compiled.ParametersSchema, &form); err != nil {
			c.add(a.File, "parameters", "the app's parameters form cannot be read: %s", err)
			continue
		}
		// A picker of the form takes a reference of its own kind.
		for _, p := range pickers.Read(form, map[string]any(a.Parameters), "parameters") {
			want := map[string]string{"collection": RefCollection, "content_type": RefType}[p.Kind]
			for _, id := range p.IDs {
				switch {
				case strings.HasPrefix(id, RefSetting):
				case want == "":
					c.add(a.File, p.Path, "picks a %s, which a solution cannot name", p.Kind)
				case !strings.HasPrefix(id, want):
					c.add(a.File, p.Path, "picks a %s, so its value must be %s<key>, not %q", strings.ReplaceAll(p.Kind, "_", " "), want, id)
				}
			}
		}
		parameters, _ := c.resolve(map[string]any(a.Parameters)).(map[string]any)
		if a.State == "published" {
			if _, err := forms.Values(form, parameters, "parameters"); err != nil {
				c.add(a.File, "", "%s", err)
			}
		}
	}
}

// walk calls fn for every text in a JSON value, with its place.
func walk(value any, at string, fn func(at, text string)) {
	switch v := value.(type) {
	case string:
		fn(at, v)
	case []any:
		for i, e := range v {
			walk(e, fmt.Sprintf("%s[%d]", at, i), fn)
		}
	case map[string]any:
		for _, k := range sortedKeys(v) {
			walk(v[k], at+"."+k, fn)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func duplicate(list []string) string {
	seen := map[string]bool{}
	for _, e := range list {
		if seen[e] {
			return e
		}
		seen[e] = true
	}
	return ""
}
