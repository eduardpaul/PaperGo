// Package solutions reads, checks and packs solutions: the definitions of
// lists, workflows and apps that install together into a workspace. A
// solution is a folder, or its ZIP, and carries no content: no items, files,
// people, permissions or IDs.
package solutions

import (
	"papergo/internal/dal/catalog"
	"papergo/internal/model"
)

// Files of a solution.
const (
	ManifestFile = "solution.json"
	WorkflowsDir = "workflows"
	AppsDir      = "apps"
)

// Reference prefixes. A reference stands for an ID the solution cannot
// know: it names something the solution declares, and becomes that thing's
// ID in the workspace when the solution is applied.
const (
	RefCollection = "$collection:"
	RefType       = "$type:"
	RefSetting    = "$setting:"
)

// OrganizationDefault starts a setting's default that is read from the
// organization's preferences when the solution is installed, such as
// "=organization.time_zone".
const OrganizationDefault = "=organization."

// Manifest is solution.json.
type Manifest struct {
	// Key names the solution. Everything it owns starts with it.
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Version is the solution's own version, MAJOR.MINOR.PATCH.
	Version string `json:"version"`
	// PaperGo is the range of PaperGo versions the solution was written for,
	// such as ">=0.2.0".
	PaperGo string `json:"papergo,omitempty"`
	// Requires names capabilities of the deployment: true refuses an
	// installation without it, false installs and reports it missing.
	Requires map[string]bool `json:"requires,omitempty"`
	// Settings is the form asked once when the solution is installed. Its
	// values reach workflow and app parameters as $setting:<name>.
	Settings          map[string]any     `json:"settings,omitempty"`
	Fields            []Field            `json:"fields,omitempty"`
	ContentTypes      []ContentType      `json:"content_types,omitempty"`
	RelationshipTypes []RelationshipType `json:"relationship_types,omitempty"`
	Collections       []Collection       `json:"collections,omitempty"`
}

// Field is a catalog field the solution owns.
type Field struct {
	Key      string             `json:"key"`
	Label    string             `json:"label"`
	Type     catalog.FieldType  `json:"type"`
	Required bool               `json:"required,omitempty"`
	Choices  []string           `json:"choices,omitempty"`
	Indexed  bool               `json:"indexed,omitempty"`
	Scale    int                `json:"scale,omitempty"`
	Options  model.FieldOptions `json:"options,omitzero"`
}

// ContentType is a catalog content type the solution owns, with its fields
// by key.
type ContentType struct {
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// RelationshipType is an organization relationship type the solution needs,
// found by its name.
type RelationshipType struct {
	Name         string `json:"name"`
	InverseLabel string `json:"inverse_label,omitempty"`
	Directed     bool   `json:"directed,omitempty"`
	MaxIncoming  *int   `json:"max_incoming,omitempty"`
	MaxOutgoing  *int   `json:"max_outgoing,omitempty"`
}

// Collection is a slot: a list or library the solution works on. A slot
// with ContentTypes is owned: it is created unless the installer binds it to
// a collection that exists. A slot with Use or Expects is another's: the
// solution verifies what it expects there and changes only what it extends.
type Collection struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Name is the collection's name when the solution creates it.
	Name string `json:"name,omitempty"`
	// ContentTypes are the solution's types the collection accepts; the
	// first is its default.
	ContentTypes  []string `json:"content_types,omitempty"`
	WebDAVEnabled bool     `json:"webdav_enabled,omitempty"`
	// Search says whether the collection's items are indexed for search;
	// absent leaves it as it is.
	Search *bool  `json:"search,omitempty"`
	Views  []View `json:"views,omitempty"`
	// Use binds the slot to a collection another solution declares.
	Use *Use `json:"use,omitempty"`
	// Expects is what the solution relies on in a collection it does not
	// own: content types by key, each with the fields it needs.
	Expects map[string][]string `json:"expects,omitempty"`
	// Extends adds the solution's own fields to types of a collection it
	// does not own.
	Extends map[string][]string `json:"extends,omitempty"`
}

// Owned reports whether the solution owns the slot's collection.
func (c Collection) Owned() bool { return c.Use == nil && len(c.Expects) == 0 }

// Use names a collection of another solution, which becomes a dependency.
type Use struct {
	Solution   string `json:"solution"`
	Collection string `json:"collection"`
	// Version is the range of the other solution's versions, such as ">=1.2.0".
	Version string `json:"version,omitempty"`
}

// View is a saved view of a slot's collection.
type View struct {
	Key       string         `json:"key"`
	Name      string         `json:"name"`
	Layout    string         `json:"layout,omitempty"`
	IsDefault bool           `json:"is_default,omitempty"`
	Columns   []string       `json:"columns,omitempty"`
	Query     map[string]any `json:"query,omitempty"`
	Settings  map[string]any `json:"settings,omitempty"`
}

// Workflow is a workflow's settings file, workflows/<name>.json, with the
// script it names.
type Workflow struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Script is the path of the workflow's TypeScript file, beside the
	// settings file.
	Script           string         `json:"script"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	AutomaticEnabled *bool          `json:"automatic_enabled,omitempty"`
	ManualEnabled    *bool          `json:"manual_enabled,omitempty"`
	State            string         `json:"state,omitempty"`

	// File is the settings file's path and Source the script's text.
	File   string `json:"-"`
	Source string `json:"-"`
}

// App is an app's settings file, apps/<name>.json, with the source it names.
type App struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// SourceFile is the path of the app's TSX file, beside the settings file.
	SourceFile string         `json:"source"`
	Parameters map[string]any `json:"parameters,omitempty"`
	State      string         `json:"state,omitempty"`

	File   string `json:"-"`
	Source string `json:"-"`
}

// Solution is a solution read from its folder or ZIP.
type Solution struct {
	Manifest  Manifest
	Workflows []Workflow
	Apps      []App
	// Locales are its display names in other languages, by language.
	Locales map[string]Locale
}
