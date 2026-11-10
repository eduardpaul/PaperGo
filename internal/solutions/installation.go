package solutions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"papergo/internal/dal/settings"
	"papergo/internal/dms"
)

// Kinds of the objects a solution installs.
const (
	KindField            = "field"
	KindContentType      = "content_type"
	KindRelationshipType = "relationship_type"
	KindCollection       = "collection"
	KindView             = "view"
	KindWorkflow         = "workflow"
	KindApp              = "app"
	// KindExtension is the solution's fields added to a type of a
	// collection it does not own; its key is <slot>/<content type>.
	KindExtension = "extension"
)

// Roles an installation has towards an object.
const (
	// RoleOwns: the solution declares the object and keeps it as it says.
	RoleOwns = "owns"
	// RoleUses: the object is another's, or a collection chosen by hand;
	// the solution relies on it and never changes it.
	RoleUses = "uses"
	// RoleExtends: the solution added its own fields to another's type.
	RoleExtends = "extends"
)

// Installation is a solution installed in a workspace: its version, the
// settings it was given, and which object each of its keys became.
type Installation struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Settings are the values of the solution's settings form.
	Settings map[string]any `json:"settings"`
	// Bindings are the slots bound by hand, to the collection chosen.
	Bindings map[string]string `json:"bindings,omitempty"`
	// Language is the language its display names were installed in, of
	// the solution's locales; empty for the solution's own text.
	Language string `json:"language,omitempty"`
	// Paused keeps the solution's workflows from starting on their own.
	Paused    bool      `json:"paused"`
	AppliedBy string    `json:"applied_by"`
	AppliedAt time.Time `json:"applied_at"`
	Objects   []Object  `json:"objects"`
	// Manifest is the solution's manifest as it was applied: what the
	// installation is checked against afterwards.
	Manifest Manifest `json:"manifest"`
	// Components are the settings files of the workflows and apps as they
	// were applied, by "<kind>:<key>": where each came from and the
	// parameters it named, references included.
	Components map[string]Component `json:"components,omitempty"`
	// Locales are the solution's display names in other languages.
	Locales map[string]Locale `json:"locales,omitempty"`
}

// Component is the settings file of a workflow or an app as it was applied.
type Component struct {
	File             string         `json:"file"`
	Source           string         `json:"source"`
	Name             string         `json:"name,omitempty"`
	Description      string         `json:"description,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	AutomaticEnabled *bool          `json:"automatic_enabled,omitempty"`
	ManualEnabled    *bool          `json:"manual_enabled,omitempty"`
}

// Object is one object of an installation.
type Object struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	ID   string `json:"id"`
	Role string `json:"role"`
	// Applied is the hash of what the solution said the object is when it
	// was last applied, and Live the hash of the object as it then was. An
	// object whose hash is no longer Live was edited in PaperGo since.
	Applied string `json:"applied,omitempty"`
	Live    string `json:"live,omitempty"`
	// Retired marks an object the solution's version no longer declares.
	// It stays until it is pruned or the solution is removed.
	Retired bool `json:"retired,omitempty"`
}

func (i *Installation) object(kind, key string) *Object {
	if i == nil {
		return nil
	}
	for n := range i.Objects {
		if i.Objects[n].Kind == kind && i.Objects[n].Key == key {
			return &i.Objects[n]
		}
	}
	return nil
}

// installed is what a workspace keeps of its solutions, by key.
type installed struct {
	Installations map[string]Installation `json:"installations"`
	version       int64
}

func (r installed) keys() []string {
	keys := make([]string, 0, len(r.Installations))
	for k := range r.Installations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readInstalled reads the solutions installed in a workspace.
func readInstalled(ctx context.Context, t *dms.Service, workspaceID string) (installed, error) {
	out := installed{Installations: map[string]Installation{}}
	row, found, err := t.DAL.Settings().Find(ctx, settings.KeySolutions, workspaceID)
	if err != nil || !found {
		return out, err
	}
	if err := json.Unmarshal(row.Value, &out); err != nil {
		return out, err
	}
	if out.Installations == nil {
		out.Installations = map[string]Installation{}
	}
	out.version = row.Version
	return out, nil
}

func writeInstalled(ctx context.Context, t *dms.Service, workspaceID, subject string, r installed) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = t.DAL.Settings().Put(ctx, settings.KeySolutions, workspaceID, raw, r.version, subject)
	return err
}

// hash is the hash of a JSON value, the same for the same value however
// its numbers and maps were held.
func hash(value any) string {
	raw, _ := json.Marshal(value)
	var plain any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&plain) == nil {
		raw, _ = json.Marshal(plain)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
