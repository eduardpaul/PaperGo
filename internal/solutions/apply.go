package solutions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

	"papergo/internal/apps"
	appstore "papergo/internal/dal/apps"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/resources"
	viewstore "papergo/internal/dal/views"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

// Scope is the module scope installing and removing solutions needs, with
// manage access to the workspace. Each change a solution makes also needs
// what that change needs when made by hand.
const Scope = "solution.manage"

// Options are what the person applying a solution chooses.
type Options struct {
	// Settings are values of the solution's settings form; those left out
	// keep what the installation has, or the form's defaults.
	Settings map[string]any `json:"settings,omitempty"`
	// Bindings choose an existing collection for a slot, by slot key.
	Bindings map[string]string `json:"bindings,omitempty"`
	// Paused keeps the solution's workflows from starting on their own.
	Paused *bool `json:"paused,omitempty"`
	// Decisions settle conflicts: for "<kind>:<key>", keep what was edited
	// in PaperGo or replace it with the solution's.
	Decisions map[string]string `json:"decisions,omitempty"`
	// Prune deletes the workflows, apps and views the solution's version no
	// longer declares. Collections, content types and fields always stay.
	Prune bool `json:"prune,omitempty"`
	// Language is the language of the display names to install, of those
	// the solution has; "none" is the solution's own text. Left out, an
	// installation keeps its language, and a first one takes the
	// organization's when the solution has it.
	Language string `json:"language,omitempty"`
}

// LanguageNone asks for the solution's own text, whatever the organization's language.
const LanguageNone = "none"

// Decisions for an object that was edited in PaperGo and that the solution
// now changes too.
const (
	DecisionKeep    = "keep"
	DecisionReplace = "replace"
)

// Change is one thing applying a solution does.
type Change struct {
	// Action is create, update, attach, extend or bind.
	Action string `json:"action"`
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Detail string `json:"detail,omitempty"`
}

// Refusal is why a solution cannot be applied as it is.
type Refusal struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
	// Conflict marks an object edited in PaperGo that the solution changes
	// too: a decision for "<kind>:<key>" lifts the refusal.
	Conflict bool `json:"conflict,omitempty"`
}

// Slot states.
const (
	// SlotCreate: applying creates the collection.
	SlotCreate = "create"
	// SlotInstalled: the collection an earlier apply created.
	SlotInstalled = "installed"
	// SlotBound: a collection chosen by hand.
	SlotBound = "bound"
	// SlotUses: a collection of another installed solution.
	SlotUses = "uses"
	// SlotUnresolved: nothing stands for the slot yet; a refusal says why.
	SlotUnresolved = "unresolved"
)

// Slot is one of a solution's collections and what stands for it in the
// workspace.
type Slot struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Name is the name the solution gives the collection it creates.
	Name  string `json:"name,omitempty"`
	Owned bool   `json:"owned"`
	// Uses is the solution whose collection the slot names.
	Uses  string `json:"uses,omitempty"`
	State string `json:"state"`
	// CollectionID and CollectionName are the collection that stands for
	// the slot, unless it is yet to be created.
	CollectionID   string `json:"collection_id,omitempty"`
	CollectionName string `json:"collection_name,omitempty"`
}

// Result is what applying a solution does, or would do.
type Result struct {
	Solution string `json:"solution"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	// From is the version installed before; empty for a first installation.
	From string `json:"from,omitempty"`
	// Applied is false for a plan, and for an apply that was refused.
	Applied  bool      `json:"applied"`
	Changes  []Change  `json:"changes"`
	Refusals []Refusal `json:"refusals"`
	// Customized are the objects edited in PaperGo that were left as they are.
	Customized []string `json:"customized"`
	Warnings   []string `json:"warnings"`
	// Slots are the solution's collections and what stands for each.
	Slots []Slot `json:"slots"`
	// SettingsForm is the solution's settings form, its defaults read from
	// the organization where it says so, and Settings the values used.
	SettingsForm map[string]any `json:"settings_form,omitempty"`
	Settings     map[string]any `json:"settings"`
	// Language is the language of the display names used, of Languages,
	// those the solution has; empty for the solution's own text.
	Language  string   `json:"language,omitempty"`
	Languages []string `json:"languages"`
}

// Service plans, applies and reads the solutions of workspaces.
type Service struct {
	DMS *dms.Service
	// PaperGo is the running version, which a solution's papergo range is
	// checked against.
	PaperGo string
	// Capabilities says what the deployment provides of what solutions may
	// require; nil when that is not known, and then nothing is refused.
	Capabilities map[string]bool
}

// lacks returns the capabilities a solution names that the deployment does
// not provide: those it cannot work without, and those it works in part
// without.
func (s *Service) lacks(m Manifest) (required, optional []string) {
	if s.Capabilities == nil {
		return nil, nil
	}
	for _, name := range sortedKeys(m.Requires) {
		switch {
		case s.Capabilities[name]:
		case m.Requires[name]:
			required = append(required, name)
		default:
			optional = append(optional, name)
		}
	}
	return required, optional
}

// errRollback ends a plan's transaction without keeping what it did.
var errRollback = errors.New("solutions: roll back")

// Plan returns what applying the solution to the workspace would do. It
// runs the apply and rolls it back, so a plan is exactly what Apply does.
func (s *Service) Plan(ctx context.Context, subject, workspaceID string, sol Solution, o Options) (Result, error) {
	return s.run(ctx, subject, workspaceID, sol, o, false)
}

// Apply makes the workspace match the solution in one transaction: it
// creates what is missing and updates what the solution changed, never
// deletes, and does nothing at all when anything is refused.
func (s *Service) Apply(ctx context.Context, subject, workspaceID string, sol Solution, o Options) (Result, error) {
	return s.run(ctx, subject, workspaceID, sol, o, true)
}

func (s *Service) run(ctx context.Context, subject, workspaceID string, sol Solution, o Options, commit bool) (Result, error) {
	if problems := Check(ctx, sol); len(problems) > 0 {
		return Result{}, dms.Invalid("the solution has problems:\n" + problems.Error())
	}
	// Read before the write transaction opens: the directory reads on its own.
	preferences, err := s.DMS.Directory().Preferences(ctx, subject, true)
	if err != nil {
		return Result{}, err
	}
	r := &run{ctx: ctx, subject: subject, workspaceID: workspaceID, sol: sol, opts: o, papergo: s.PaperGo,
		ids: map[string]string{}, fieldIDs: map[string]string{}, typeIDs: map[string]string{},
		preferences: map[string]string{"time_zone": preferences.TimeZone, "language": preferences.Language, "default_currency": preferences.DefaultCurrency,
			"number_format": preferences.NumberFormat, "date_format": preferences.DateFormat, "time_format": preferences.TimeFormat},
		result: Result{Solution: sol.Manifest.Key, Name: sol.Manifest.Name, Version: sol.Manifest.Version, Changes: []Change{}, Refusals: []Refusal{}, Customized: []string{}, Warnings: []string{}, Slots: []Slot{}, Settings: map[string]any{}, Languages: sol.Languages()}}
	required, optional := s.lacks(sol.Manifest)
	for _, name := range required {
		r.refuse("solution", sol.Manifest.Key, "needs %s, which this PaperGo does not provide", name)
	}
	for _, name := range optional {
		r.warn("this PaperGo does not provide %s: the solution works in part", name)
	}
	err = s.DMS.Write(ctx, func(t *dms.Service) error {
		r.t = t
		if err := r.reconcile(); err != nil {
			return err
		}
		if !commit || len(r.result.Refusals) > 0 {
			return errRollback
		}
		r.result.Applied = true
		return nil
	})
	if err != nil && !errors.Is(err, errRollback) {
		return Result{}, err
	}
	return r.result, nil
}

// run is one apply of a solution to a workspace, inside its transaction.
type run struct {
	ctx         context.Context
	t           *dms.Service
	subject     string
	workspaceID string
	sol         Solution
	opts        Options
	papergo     string
	preferences map[string]string
	result      Result
	// base is the solution as its files say; sol is base in the
	// installation's language.
	base Solution

	installed installed
	previous  *Installation
	next      Installation
	// ids are what references stand for: $collection:<key> and $type:<key>.
	ids      map[string]string
	fieldIDs map[string]string
	typeIDs  map[string]string
	settings map[string]any
}

func (r *run) change(action, kind, key, detail string) {
	r.result.Changes = append(r.result.Changes, Change{action, kind, key, detail})
}

func (r *run) refuse(kind, key, format string, args ...any) {
	r.result.Refusals = append(r.result.Refusals, Refusal{Kind: kind, Key: key, Reason: fmt.Sprintf(format, args...)})
}

func (r *run) warn(format string, args ...any) {
	r.result.Warnings = append(r.result.Warnings, fmt.Sprintf(format, args...))
}

// failed says which object an error of the services below came from.
func failed(kind, key string, err error) error {
	return fmt.Errorf("%s %s: %w", strings.ReplaceAll(kind, "_", " "), key, err)
}

func (r *run) reconcile() error {
	m := r.sol.Manifest
	if _, err := r.t.Authorize(r.ctx, r.subject, r.workspaceID, "manage"); err != nil {
		return err
	}
	if err := r.t.RequireScope(r.ctx, r.subject, Scope); err != nil {
		return err
	}
	var err error
	if r.installed, err = readInstalled(r.ctx, r.t, r.workspaceID); err != nil {
		return err
	}
	if previous, ok := r.installed.Installations[m.Key]; ok {
		r.previous = &previous
		r.result.From = previous.Version
	}
	r.localize()
	m = r.sol.Manifest
	r.result.Name = m.Name
	r.next = Installation{Key: m.Key, Name: m.Name, Version: m.Version, Language: r.result.Language, Bindings: map[string]string{}, AppliedBy: r.subject, AppliedAt: time.Now().UTC().Truncate(time.Second)}
	if r.previous != nil {
		r.next.Paused = r.previous.Paused
	}
	if r.opts.Paused != nil {
		r.next.Paused = *r.opts.Paused
	}
	r.checkVersion()
	r.settle()
	for _, step := range []func() error{r.fields, r.relationshipTypes, r.contentTypes, r.collections, r.workflows, r.apps} {
		if err := step(); err != nil {
			return err
		}
	}
	// What an earlier version installed and this one no longer declares
	// stays, marked, until it is pruned or the solution is removed.
	if r.previous != nil {
		for _, o := range r.previous.Objects {
			if r.next.object(o.Kind, o.Key) != nil {
				continue
			}
			if r.opts.Prune && prunable(o.Kind) {
				if err := discard(r.ctx, r.t, r.subject, o); err != nil {
					return failed(o.Kind, o.Key, err)
				}
				r.change("delete", o.Kind, o.Key, "no longer part of the solution")
				continue
			}
			if !o.Retired {
				r.warn("%s %s is no longer part of the solution; it was kept", strings.ReplaceAll(o.Kind, "_", " "), o.Key)
			}
			o.Retired = true
			r.next.Objects = append(r.next.Objects, o)
		}
	}
	// The installation keeps the solution as its files say, with its
	// locales: what it is checked against, and what a pull starts from.
	r.next.Settings, r.next.Manifest, r.next.Locales = r.settings, r.base.Manifest, r.base.Locales
	r.next.Components = map[string]Component{}
	for _, w := range r.base.Workflows {
		r.next.Components[KindWorkflow+":"+w.Key] = Component{File: w.File, Source: w.Script, Name: w.Name, Description: w.Description, Parameters: w.Parameters, AutomaticEnabled: w.AutomaticEnabled, ManualEnabled: w.ManualEnabled}
	}
	for _, a := range r.base.Apps {
		r.next.Components[KindApp+":"+a.Key] = Component{File: a.File, Source: a.SourceFile, Name: a.Name, Description: a.Description, Parameters: a.Parameters}
	}
	r.installed.Installations[m.Key] = r.next
	if err := writeInstalled(r.ctx, r.t, r.workspaceID, r.subject, r.installed); err != nil {
		return err
	}
	workspace, err := r.t.Get(r.ctx, r.subject, r.workspaceID)
	if err != nil {
		return err
	}
	return r.t.Record(r.ctx, r.subject, "solution.apply", workspace, map[string]any{"solution": m.Key, "version": m.Version, "from": r.result.From, "changes": len(r.result.Changes)})
}

// localize puts the solution in the installation's language: the one
// asked for, else the one it was installed in, else the organization's
// when the solution has it.
func (r *run) localize() {
	r.base = r.sol
	wanted := r.opts.Language
	switch {
	case wanted == LanguageNone:
		return
	case wanted != "":
		if r.base.language(wanted) == "" {
			r.refuse("solution", r.sol.Manifest.Key, "has no display names in %q; it has %s", wanted, orNone(r.base.Languages()))
			return
		}
	case r.previous != nil:
		wanted = r.previous.Language
	default:
		wanted = r.preferences["language"]
	}
	r.sol, r.result.Language = r.base.Localized(wanted)
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// checkVersion refuses a solution written for another PaperGo.
func (r *run) checkVersion() {
	wants := r.sol.Manifest.PaperGo
	if wants == "" {
		return
	}
	running, err := semver.NewVersion(strings.TrimPrefix(r.papergo, "v"))
	if err != nil {
		r.warn("this PaperGo is %q, not a released version: the solution's range %s was not checked", r.papergo, wants)
		return
	}
	if range_, err := semver.NewConstraint(wants); err == nil && !range_.Check(running) {
		r.refuse("solution", r.sol.Manifest.Key, "is written for PaperGo %s; this is %s", wants, running)
	}
}

// settle works out the values of the solution's settings: what was given,
// else what the installation has, else the form's defaults, with defaults
// read from the organization's preferences.
func (r *run) settle() {
	r.settings = map[string]any{}
	form := r.sol.Manifest.Settings
	given := map[string]any{}
	if r.previous != nil {
		for k, v := range r.previous.Settings {
			given[k] = v
		}
	}
	for k, v := range r.opts.Settings {
		given[k] = v
	}
	if form == nil {
		if len(r.opts.Settings) > 0 {
			r.refuse("settings", r.sol.Manifest.Key, "the solution has no settings")
		}
		return
	}
	resolved := forms.Plain(form).(map[string]any)
	props, _ := resolved["properties"].(map[string]any)
	for _, raw := range props {
		p, _ := raw.(map[string]any)
		if def, ok := p["default"].(string); ok && strings.HasPrefix(def, OrganizationDefault) {
			p["default"] = r.preferences[strings.TrimPrefix(def, OrganizationDefault)]
		}
	}
	for name := range given {
		if _, known := props[name]; !known {
			delete(given, name) // a setting an earlier version had
		}
	}
	r.result.SettingsForm = resolved
	values, err := forms.Values(resolved, given, "settings")
	if err != nil {
		r.refuse("settings", r.sol.Manifest.Key, "%s", err)
		return
	}
	r.settings, r.result.Settings = values, values
}

// Actions settling an object can take.
const (
	doCreate = iota
	doUpdate
	doSkip
)

// decide says what to do with an object the solution owns, from three
// states: what the solution said when last applied, what it says now
// (want), and the object as it is (live). kept is the Live hash to record
// when the object stays as someone edited it.
func (r *run) decide(kind, key string, want, live any, exists bool) (action int, kept string) {
	if !exists {
		return doCreate, ""
	}
	wants, lives := hash(want), hash(live)
	before := r.previous.object(kind, key)
	if before == nil || before.Applied == "" {
		// Not this installation's yet: another installation's, or found
		// by key. The solution's version wins.
		if lives == wants {
			return doSkip, ""
		}
		return doUpdate, ""
	}
	name := kind + ":" + key
	if wants == before.Applied {
		if lives != before.Live {
			r.result.Customized = append(r.result.Customized, name)
			return doSkip, before.Live
		}
		return doSkip, ""
	}
	if lives == wants {
		return doSkip, "" // already as the solution now says, as after a pull
	}
	if lives == before.Live {
		return doUpdate, ""
	}
	switch r.opts.Decisions[name] {
	case DecisionReplace:
		return doUpdate, ""
	case DecisionKeep:
		r.result.Customized = append(r.result.Customized, name)
		return doSkip, before.Live
	}
	r.refuse(kind, key, "was edited in PaperGo and the solution changes it too: decide %q: keep or replace", name)
	r.result.Refusals[len(r.result.Refusals)-1].Conflict = true
	return doSkip, before.Live
}

// note records an object of the installation: what the solution says it is
// and how it is now.
func (r *run) note(kind, key, id, role string, want, live any, kept string) {
	o := Object{Kind: kind, Key: key, ID: id, Role: role}
	if want != nil {
		o.Applied, o.Live = hash(want), hash(live)
		if kept != "" {
			o.Live = kept
		}
	}
	r.next.Objects = append(r.next.Objects, o)
}

func fieldState(label string, required, indexed bool, choices []string, f catalog.FieldType, options any) map[string]any {
	state := map[string]any{"label": label, "required": required, "indexed": indexed, "options": options}
	if f == catalog.FieldChoice {
		state["choices"] = choices
	}
	return state
}

func (r *run) fields() error {
	all, err := r.t.CatalogFields(r.ctx, r.subject)
	if err != nil {
		return err
	}
	byID, byKey := map[string]*catalog.FieldDefinition{}, map[string][]*catalog.FieldDefinition{}
	for _, f := range all {
		byID[f.ID] = f
		byKey[f.Key] = append(byKey[f.Key], f)
	}
	for _, f := range r.sol.Manifest.Fields {
		options := f.Options
		if ref := options.LookupContainerID; ref != "" {
			id, ok := r.existingCollection(strings.TrimPrefix(ref, RefCollection))
			if !ok {
				r.refuse(KindField, f.Key, "looks up a collection this apply creates; apply the solution without the field first, or bind the collection")
				continue
			}
			options.LookupContainerID = id
		}
		want := fieldState(f.Label, f.Required, f.Indexed, f.Choices, f.Type, options)
		var live *catalog.FieldDefinition
		if before := r.previous.object(KindField, f.Key); before != nil {
			live = byID[before.ID]
		}
		if live == nil {
			if same := byKey[f.Key]; len(same) > 1 {
				r.refuse(KindField, f.Key, "%d catalog fields have this key", len(same))
				continue
			} else if len(same) == 1 {
				live = same[0]
			}
		}
		if live == nil {
			created, err := r.t.CreateCatalogField(r.ctx, r.subject, dms.CreateField{Key: f.Key, Label: f.Label, Type: f.Type, Required: f.Required, Choices: f.Choices, Indexed: f.Indexed, Scale: f.Scale, Options: options})
			if err != nil {
				return failed(KindField, f.Key, err)
			}
			r.change("create", KindField, f.Key, string(f.Type))
			r.fieldIDs[f.Key] = created.ID
			r.note(KindField, f.Key, created.ID, RoleOwns, want, fieldState(created.Label, created.Required, created.Indexed, created.Choices, created.Type, created.Options), "")
			continue
		}
		r.fieldIDs[f.Key] = live.ID
		// The field's state is the solution's part of it: its choices, of
		// those the field has.
		lives := fieldState(live.Label, live.Required, live.Indexed, present(f.Choices, live.Choices), live.Type, live.Options)
		if live.Type != f.Type || live.Scale != f.Scale || live.Options.Multiple != f.Options.Multiple {
			r.refuse(KindField, f.Key, "is a %s field (scale %d) and the solution says %s (scale %d): a field's type cannot change once it holds values", live.Type, live.Scale, f.Type, f.Scale)
			r.note(KindField, f.Key, live.ID, RoleOwns, want, lives, "")
			continue
		}
		// A choice the solution had and no longer lists would leave stored
		// values without one. Choices added in PaperGo stay where they are.
		var gone, others []string
		for _, choice := range missing(live.Choices, f.Choices) {
			if contains(r.previous.choices(f.Key), choice) {
				gone = append(gone, choice)
			} else {
				others = append(others, choice)
			}
		}
		if len(gone) > 0 {
			r.refuse(KindField, f.Key, "would lose the choices %s, which stored items may hold", strings.Join(gone, ", "))
			r.note(KindField, f.Key, live.ID, RoleOwns, want, lives, "")
			continue
		}
		action, kept := r.decide(KindField, f.Key, want, lives, true)
		choices := append(append([]string{}, f.Choices...), others...)
		in := dms.UpdateField{Options: &options, Label: &f.Label, Required: &f.Required, Indexed: &f.Indexed}
		if f.Type == catalog.FieldChoice {
			in.Choices = &choices
			// A choice the field lacks is added whatever was edited: it is the contract.
			if action == doSkip && len(present(f.Choices, live.Choices)) < len(f.Choices) {
				action, in = doUpdate, dms.UpdateField{Choices: &choices}
			}
		}
		if action == doUpdate {
			updated, err := r.t.UpdateCatalogField(r.ctx, r.subject, live.ID, live.Version, in)
			if err != nil {
				return failed(KindField, f.Key, err)
			}
			after := fieldState(updated.Label, updated.Required, updated.Indexed, present(f.Choices, updated.Choices), updated.Type, updated.Options)
			r.change("update", KindField, f.Key, differences(lives, after))
			lives = after
		}
		r.note(KindField, f.Key, live.ID, RoleOwns, want, lives, kept)
	}
	return nil
}

func (r *run) relationshipTypes() error {
	if len(r.sol.Manifest.RelationshipTypes) == 0 {
		return nil
	}
	have := map[string]string{}
	for after := ""; ; {
		page, err := r.t.RelationshipTypes(r.ctx, r.subject, after, 200)
		if err != nil {
			return err
		}
		for _, t := range page.Data {
			have[strings.ToLower(t.Term.Name)] = t.ID
		}
		if after = page.NextCursor; after == "" {
			break
		}
	}
	for _, t := range r.sol.Manifest.RelationshipTypes {
		id, ok := have[strings.ToLower(t.Name)]
		if !ok {
			created, err := r.t.CreateRelationshipType(r.ctx, r.subject, dms.RelationshipTypeInput{Name: t.Name, InverseLabel: t.InverseLabel, Directed: t.Directed, MaxIncoming: t.MaxIncoming, MaxOutgoing: t.MaxOutgoing})
			if err != nil {
				return failed(KindRelationshipType, t.Name, err)
			}
			r.change("create", KindRelationshipType, t.Name, "")
			id = created.ID
		}
		r.note(KindRelationshipType, t.Name, id, RoleUses, nil, nil, "")
	}
	return nil
}

func (r *run) contentTypes() error {
	all, err := r.t.CatalogContentTypes(r.ctx, r.subject)
	if err != nil {
		return err
	}
	byKey := map[string]*catalog.ContentType{}
	for _, t := range all {
		byKey[t.Key] = t
		r.typeIDs[t.Key] = t.ID
	}
	keyOf := map[string]string{}
	fields, err := r.t.CatalogFields(r.ctx, r.subject)
	if err != nil {
		return err
	}
	for _, f := range fields {
		keyOf[f.ID] = f.Key
	}
	for _, t := range r.sol.Manifest.ContentTypes {
		ids, ready := make([]string, 0, len(t.Fields)), true
		for _, key := range t.Fields {
			id, ok := r.fieldIDs[key]
			ids, ready = append(ids, id), ready && ok
		}
		if !ready {
			continue // one of its fields was refused
		}
		want := map[string]any{"name": t.Name, "fields": t.Fields}
		live := byKey[t.Key]
		if live == nil {
			created, err := r.t.CreateContentType(r.ctx, r.subject, dms.ContentTypeInput{Key: t.Key, Name: t.Name, FieldIds: ids})
			if err != nil {
				return failed(KindContentType, t.Key, err)
			}
			r.change("create", KindContentType, t.Key, fmt.Sprintf("%d field(s)", len(ids)))
			r.typeIDs[t.Key], r.ids[RefType+t.Key] = created.ID, created.ID
			r.note(KindContentType, t.Key, created.ID, RoleOwns, want, want, "")
			continue
		}
		r.ids[RefType+t.Key] = live.ID
		// A field the solution had on the type and no longer lists would
		// leave stored values without a field. Fields added in PaperGo,
		// the solution's own among them, stay where they are.
		var gone, others, has []string
		for _, id := range live.FieldIds {
			key := keyOf[id]
			switch {
			case contains(t.Fields, key):
			case contains(r.previous.typeFields(t.Key), key):
				gone = append(gone, key)
			default:
				others = append(others, id)
			}
		}
		for _, key := range t.Fields {
			if contains(live.FieldIds, r.fieldIDs[key]) {
				has = append(has, key)
			}
		}
		lives := map[string]any{"name": live.Name, "fields": has}
		if len(gone) > 0 {
			r.refuse(KindContentType, t.Key, "would lose the fields %s, whose stored values would have no field", strings.Join(gone, ", "))
			r.note(KindContentType, t.Key, live.ID, RoleOwns, want, lives, "")
			continue
		}
		action, kept := r.decide(KindContentType, t.Key, want, lives, true)
		// A field the type lacks is added whatever was edited: it is the contract.
		if action == doSkip && len(has) < len(t.Fields) {
			action = doUpdate
		}
		if action == doUpdate {
			name := t.Name
			if kept != "" {
				name = live.Name
			}
			in := dms.ContentTypeInput{Key: live.Key, Name: name, Group: live.Group, FieldIds: append(ids, others...), HiddenFieldIds: live.HiddenFieldIds, Rules: live.Rules, FieldOrder: live.FieldOrder, FieldSettings: live.FieldSettings}
			if live.ParentID != nil {
				in.ParentID = *live.ParentID
			}
			if _, err := r.t.UpdateContentType(r.ctx, r.subject, live.ID, live.Version, in); err != nil {
				return failed(KindContentType, t.Key, err)
			}
			r.change("update", KindContentType, t.Key, differences(lives, map[string]any{"name": name, "fields": t.Fields}))
			lives = map[string]any{"name": name, "fields": t.Fields}
		}
		r.note(KindContentType, t.Key, live.ID, RoleOwns, want, lives, kept)
	}
	return nil
}

// existingCollection is the collection a slot stands for when it already
// exists: bound by hand, or installed before.
func (r *run) existingCollection(slot string) (string, bool) {
	if id := r.opts.Bindings[slot]; id != "" {
		return id, true
	}
	if r.previous != nil {
		if id := r.previous.Bindings[slot]; id != "" {
			return id, true
		}
		if o := r.previous.object(KindCollection, slot); o != nil {
			if _, err := r.t.Get(r.ctx, r.subject, o.ID); err == nil {
				return o.ID, true
			}
		}
	}
	return "", false
}

func (r *run) collections() error {
	for _, slot := range r.sol.Manifest.Collections {
		before := r.previous.object(KindCollection, slot.Key)
		c, role, err := r.target(slot)
		if err != nil {
			return err
		}
		info := Slot{Key: slot.Key, Kind: slot.Kind, Name: slot.Name, Owned: slot.Owned(), State: SlotUnresolved}
		if slot.Use != nil {
			info.Uses = slot.Use.Solution
		}
		if c != nil {
			info.CollectionID, info.CollectionName = c.ID, c.Name
			switch {
			case r.next.Bindings[slot.Key] != "":
				info.State = SlotBound
			case !slot.Owned():
				info.State = SlotUses
			case before != nil && before.ID == c.ID:
				info.State = SlotInstalled
			default:
				info.State, info.CollectionID, info.CollectionName = SlotCreate, "", ""
			}
		}
		r.result.Slots = append(r.result.Slots, info)
		if c == nil {
			continue // refused
		}
		r.ids[RefCollection+slot.Key] = c.ID
		if slot.Owned() {
			if c, err = r.attach(slot, c); err != nil {
				return err
			}
		} else if err := r.foreign(slot, c); err != nil {
			return err
		}
		// WebDAV is a library's setting; a list has none.
		state := func(name string, webdav bool) map[string]any {
			if slot.Kind == "library" {
				return map[string]any{"name": name, "webdav_enabled": webdav}
			}
			return map[string]any{"name": name}
		}
		want, lives := state(slot.Name, slot.WebDAVEnabled), state(c.Name, c.WebdavEnabled)
		kept := ""
		if role == RoleOwns {
			var action int
			if action, kept = r.decide(KindCollection, slot.Key, want, lives, true); action == doUpdate {
				in := dms.UpdateResource{Name: &slot.Name}
				if slot.Kind == "library" {
					in.WebDAVEnabled = &slot.WebDAVEnabled
				}
				updated, err := r.t.Update(r.ctx, r.subject, c.ID, c.Version, in)
				if err != nil {
					return failed(KindCollection, slot.Key, err)
				}
				r.change("update", KindCollection, slot.Key, differences(lives, want))
				lives = state(updated.Name, updated.WebdavEnabled)
			}
			if slot.Search != nil {
				if err := r.search(slot, c.ID); err != nil {
					return err
				}
			}
			r.note(KindCollection, slot.Key, c.ID, role, want, lives, kept)
		} else {
			r.note(KindCollection, slot.Key, c.ID, role, nil, nil, "")
		}
		if err := r.views(slot, c.ID); err != nil {
			return err
		}
	}
	return nil
}

// target finds or creates the collection of a slot, and says whether the
// installation owns it. A nil collection was refused.
func (r *run) target(slot Collection) (*resources.Resource, string, error) {
	// A collection chosen by hand: the solution keeps its name and settings.
	bound := r.opts.Bindings[slot.Key]
	if bound == "" && r.previous != nil {
		bound = r.previous.Bindings[slot.Key]
	}
	if bound != "" {
		c, err := r.t.Get(r.ctx, r.subject, bound)
		if err != nil || c.WorkspaceID != r.workspaceID || (c.Kind != "list" && c.Kind != "library") {
			r.refuse(KindCollection, slot.Key, "is bound to %s, which is not a list or library of this workspace", bound)
			return nil, "", nil
		}
		if string(c.Kind) != slot.Kind {
			r.refuse(KindCollection, slot.Key, "is bound to %q, a %s; the solution needs a %s", c.Name, c.Kind, slot.Kind)
			return nil, "", nil
		}
		r.next.Bindings[slot.Key] = bound
		if r.previous == nil || r.previous.Bindings[slot.Key] != bound {
			r.change("bind", KindCollection, slot.Key, c.Name)
		}
		return c, RoleUses, nil
	}
	if !slot.Owned() {
		if slot.Use == nil {
			r.refuse(KindCollection, slot.Key, "is another's collection: choose the list or library it stands for")
			return nil, "", nil
		}
		other, ok := r.installed.Installations[slot.Use.Solution]
		if !ok {
			r.refuse(KindCollection, slot.Key, "uses a collection of the solution %q, which is not installed in this workspace", slot.Use.Solution)
			return nil, "", nil
		}
		if slot.Use.Version != "" {
			range_, _ := semver.NewConstraint(slot.Use.Version)
			if v, err := semver.NewVersion(other.Version); err != nil || range_ == nil || !range_.Check(v) {
				r.refuse(KindCollection, slot.Key, "needs %s %s; version %s is installed", slot.Use.Solution, slot.Use.Version, other.Version)
				return nil, "", nil
			}
		}
		o := other.object(KindCollection, slot.Use.Collection)
		if o == nil {
			r.refuse(KindCollection, slot.Key, "uses the collection %q of %s, which that solution does not have", slot.Use.Collection, slot.Use.Solution)
			return nil, "", nil
		}
		c, err := r.t.Get(r.ctx, r.subject, o.ID)
		if err != nil {
			r.refuse(KindCollection, slot.Key, "uses the collection %q of %s, which cannot be read", slot.Use.Collection, slot.Use.Solution)
			return nil, "", nil
		}
		if string(c.Kind) != slot.Kind {
			r.refuse(KindCollection, slot.Key, "uses %q, a %s; the solution needs a %s", c.Name, c.Kind, slot.Kind)
			return nil, "", nil
		}
		return c, RoleUses, nil
	}
	if o := r.previous.object(KindCollection, slot.Key); o != nil {
		if c, err := r.t.Get(r.ctx, r.subject, o.ID); err == nil {
			return c, RoleOwns, nil
		}
	}
	types := make([]string, 0, len(slot.ContentTypes))
	for _, key := range slot.ContentTypes {
		id, ok := r.ids[RefType+key]
		if !ok {
			return nil, "", nil // its type was refused
		}
		types = append(types, id)
	}
	c, err := r.t.Create(r.ctx, r.subject, r.workspaceID, dms.CreateResource{Kind: slot.Kind, Name: slot.Name, ContentTypeIDs: types, WebDAVEnabled: slot.WebDAVEnabled})
	if err != nil {
		return nil, "", failed(KindCollection, slot.Key, err)
	}
	r.change("create", KindCollection, slot.Key, slot.Kind+" "+slot.Name)
	return c, RoleOwns, nil
}

// attach gives a collection the solution's content types it lacks.
func (r *run) attach(slot Collection, c *resources.Resource) (*resources.Resource, error) {
	attached, err := r.t.CollectionContentTypes(r.ctx, r.subject, c.ID)
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, t := range attached {
		if t.ParentID != nil {
			has[*t.ParentID] = true
		}
	}
	for _, key := range slot.ContentTypes {
		id, ok := r.ids[RefType+key]
		if !ok || has[id] {
			continue
		}
		if c, err = r.t.AttachContentType(r.ctx, r.subject, c.ID, c.Version, dms.AttachContentTypeInput{ContentTypeID: id}); err != nil {
			return nil, failed(KindCollection, slot.Key, err)
		}
		r.change("attach", KindCollection, slot.Key, key)
	}
	return c, nil
}

// foreign verifies what a slot expects of another's collection, and adds
// the solution's own fields to the list's copies of its types.
func (r *run) foreign(slot Collection, c *resources.Resource) error {
	problems, listTypes, err := expected(r.ctx, r.t, r.subject, slot, c.ID)
	if err != nil {
		return err
	}
	for _, p := range problems {
		r.refuse(KindCollection, slot.Key, "%s", p)
	}
	for _, key := range sortedKeys(slot.Expects) {
		if id, ok := r.typeIDs[key]; ok {
			r.ids[RefType+key] = id
		}
	}
	for _, key := range sortedKeys(slot.Extends) {
		lt := listTypes[key]
		if lt == nil {
			continue // reported above
		}
		ids, added := append([]string{}, lt.FieldIds...), []string{}
		for _, f := range slot.Extends[key] {
			id, ok := r.fieldIDs[f]
			if !ok || contains(lt.FieldIds, id) || contains(lt.Effective.FieldIds, id) {
				continue
			}
			ids, added = append(ids, id), append(added, f)
		}
		name := slot.Key + "/" + key
		if len(added) > 0 {
			if _, err := r.t.UpdateListType(r.ctx, r.subject, c.ID, lt.ID, lt.Version, dms.ListTypeInput{FieldIds: ids, HiddenFieldIds: lt.HiddenFieldIds, Rules: lt.Rules, FieldOrder: lt.FieldOrder, FieldSettings: lt.FieldSettings}); err != nil {
				return failed(KindExtension, name, err)
			}
			r.change("extend", KindExtension, name, strings.Join(added, ", "))
		}
		r.note(KindExtension, name, lt.ID, RoleExtends, nil, nil, "")
	}
	return nil
}

// expected checks what a slot expects of a collection: each content type
// attached, with each field. It returns what is missing and the
// collection's list types by key.
func expected(ctx context.Context, t *dms.Service, subject string, slot Collection, collectionID string) ([]string, map[string]*catalog.ContentType, error) {
	attached, err := t.CollectionContentTypes(ctx, subject, collectionID)
	if err != nil {
		return nil, nil, err
	}
	fields, err := t.Fields(ctx, subject, collectionID)
	if err != nil {
		return nil, nil, err
	}
	keyOf := map[string]string{}
	for _, f := range fields {
		keyOf[f.ID] = f.Key
	}
	byKey := map[string]*catalog.ContentType{}
	for _, lt := range attached {
		byKey[lt.Key] = lt
	}
	var problems []string
	for _, key := range sortedKeys(slot.Expects) {
		lt := byKey[key]
		if lt == nil {
			problems = append(problems, fmt.Sprintf("expects the content type %s, which the collection does not accept", key))
			continue
		}
		has := map[string]bool{}
		for _, id := range lt.Effective.FieldIds {
			has[keyOf[id]] = true
		}
		for _, f := range slot.Expects[key] {
			if !has[f] {
				problems = append(problems, fmt.Sprintf("expects the field %s on %s, which lacks it", f, key))
			}
		}
	}
	return problems, byKey, nil
}

// search turns the indexing of a collection's items on or off: the
// search.index provider of the collection.
func (r *run) search(slot Collection, collectionID string) error {
	svc := &workflow.Service{DMS: r.t}
	states, err := svc.ListBuiltIns(r.ctx, r.subject, r.workspaceID)
	if err != nil {
		return err
	}
	for _, b := range states {
		if b.BuiltIn == nil || b.Key != "search.index" {
			continue
		}
		in := workflow.SetBuiltIn{CollectionID: collectionID, AutomaticEnabled: *slot.Search}
		on := true // a collection without a provider of its own is indexed
		for _, w := range b.Workflows {
			if w.CollectionID != nil && *w.CollectionID == collectionID {
				on, in.Version = w.AutomaticEnabled, w.Version
			}
		}
		if on == *slot.Search {
			return nil
		}
		if _, err := svc.UpdateBuiltIn(r.ctx, r.subject, r.workspaceID, b.Key, in); err != nil {
			return failed(KindCollection, slot.Key, err)
		}
		r.change("update", KindCollection, slot.Key, fmt.Sprintf("search %s", map[bool]string{true: "on", false: "off"}[*slot.Search]))
		return nil
	}
	r.warn("%s: this PaperGo runs without search, so its search setting was left", slot.Key)
	return nil
}

func viewState(name string, columns []string, query, settings any, layout string, isDefault bool) map[string]any {
	return map[string]any{"name": name, "columns": columns, "query": query, "layout": layout, "settings": settings, "is_default": isDefault}
}

// storedView is a view as PaperGo keeps it, which may say the same query
// in other words than the solution did.
func storedView(l *viewstore.ListView) map[string]any {
	var query, settings any
	_ = json.Unmarshal(l.Query, &query)
	_ = json.Unmarshal(l.Settings, &settings)
	return viewState(l.Name, l.Columns, query, settings, string(l.Layout), l.IsDefault)
}

func (r *run) views(slot Collection, collectionID string) error {
	if len(slot.Views) == 0 {
		return nil
	}
	page, err := r.t.Views(r.ctx, r.subject, collectionID, "", 200)
	if err != nil {
		return err
	}
	for _, v := range slot.Views {
		name := slot.Key + "/" + v.Key
		query, ok := r.resolve(KindView, name, v.Query)
		if !ok {
			continue
		}
		raw, _ := json.Marshal(viewState(v.Name, v.Columns, query, v.Settings, v.Layout, v.IsDefault))
		var in dms.ViewInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return failed(KindView, name, err)
		}
		var want map[string]any
		_ = json.Unmarshal(raw, &want)
		var liveID string
		var version int
		var lives map[string]any
		for i := range page.Data {
			l := &page.Data[i]
			before := r.previous.object(KindView, name)
			if l.SystemKey != "" || (before != nil && before.ID != l.ID) || (before == nil && l.Name != v.Name) {
				continue
			}
			liveID, version, lives = l.ID, l.Version, storedView(l)
		}
		action, kept := r.decide(KindView, name, want, lives, liveID != "")
		switch action {
		case doCreate:
			created, err := r.t.CreateView(r.ctx, r.subject, collectionID, in)
			if err != nil {
				return failed(KindView, name, err)
			}
			r.change("create", KindView, name, v.Name)
			liveID, lives = created.ID, storedView(created)
		case doUpdate:
			updated, err := r.t.UpdateView(r.ctx, r.subject, liveID, version, in)
			if err != nil {
				return failed(KindView, name, err)
			}
			r.change("update", KindView, name, differences(lives, want))
			lives = storedView(updated)
		}
		r.note(KindView, name, liveID, RoleOwns, want, lives, kept)
	}
	return nil
}

// resolve returns a JSON value with its references replaced by the IDs and
// settings they stand for here. It refuses a reference to something that
// was itself refused.
func (r *run) resolve(kind, key string, value map[string]any) (map[string]any, bool) {
	ok := true
	var replace func(any) any
	replace = func(v any) any {
		switch x := v.(type) {
		case string:
			switch {
			case strings.HasPrefix(x, RefCollection), strings.HasPrefix(x, RefType):
				id, found := r.ids[x]
				if !found {
					ok = false
				}
				return id
			case strings.HasPrefix(x, RefSetting):
				return r.settings[strings.TrimPrefix(x, RefSetting)]
			}
			return x
		case []any:
			out := make([]any, len(x))
			for i, e := range x {
				out[i] = replace(e)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = replace(e)
			}
			return out
		}
		return v
	}
	out, _ := replace(map[string]any(value)).(map[string]any)
	if !ok {
		r.refuse(kind, key, "names something that could not be installed")
	}
	return out, ok
}

func (r *run) workflows() error {
	if len(r.sol.Workflows) == 0 {
		return nil
	}
	svc := &workflow.Service{DMS: r.t}
	list, err := svc.List(r.ctx, r.subject, r.workspaceID)
	if err != nil {
		return err
	}
	byKey := map[string]string{}
	for _, w := range list {
		if w.BuiltInKey == nil {
			byKey[w.Key] = w.ID
		}
	}
	for _, w := range r.sol.Workflows {
		parameters, ok := r.resolve(KindWorkflow, w.Key, w.Parameters)
		if !ok {
			continue
		}
		save := workflow.Save{Name: w.Name, Description: w.Description, Source: w.Source, Parameters: parameters, AutomaticEnabled: w.AutomaticEnabled, ManualEnabled: w.ManualEnabled, State: w.State}
		if r.next.Paused {
			off := false
			save.AutomaticEnabled = &off
		}
		want := map[string]any{"name": save.Name, "description": save.Description, "source": save.Source, "parameters": parameters}
		state := func(l workflow.Workflow) map[string]any {
			s := map[string]any{"name": l.Name, "description": l.Description, "source": l.Source, "parameters": orEmpty(l.Parameters)}
			if save.AutomaticEnabled != nil {
				s["automatic_enabled"] = l.AutomaticEnabled
			}
			if save.ManualEnabled != nil {
				s["manual_enabled"] = l.ManualEnabled
			}
			if save.State != "" {
				s["state"] = l.State
			}
			return s
		}
		if save.AutomaticEnabled != nil {
			want["automatic_enabled"] = *save.AutomaticEnabled
		}
		if save.ManualEnabled != nil {
			want["manual_enabled"] = *save.ManualEnabled
		}
		if save.State != "" {
			want["state"] = save.State
		}
		want["parameters"] = orEmpty(parameters)
		id := byKey[w.Key]
		var live workflow.Workflow
		var lives map[string]any
		if id != "" {
			if live, err = svc.Get(r.ctx, r.subject, id); err != nil {
				return failed(KindWorkflow, w.Key, err)
			}
			lives = state(live)
		}
		action, kept := r.decide(KindWorkflow, w.Key, want, lives, id != "")
		switch action {
		case doCreate:
			save.Key = w.Key
			created, err := svc.Create(r.ctx, r.subject, r.workspaceID, save)
			if err != nil {
				return failed(KindWorkflow, w.Key, err)
			}
			r.change("create", KindWorkflow, w.Key, w.Name)
			id, lives = created.ID, state(created)
		case doUpdate:
			updated, err := svc.Update(r.ctx, r.subject, id, live.Version, save)
			if err != nil {
				return failed(KindWorkflow, w.Key, err)
			}
			detail := differences(lives, want)
			if updated.CurrentVersion != live.CurrentVersion {
				detail += fmt.Sprintf(" (version %d)", updated.CurrentVersion)
			}
			r.change("update", KindWorkflow, w.Key, detail)
			lives = state(updated)
		}
		r.note(KindWorkflow, w.Key, id, RoleOwns, want, lives, kept)
	}
	return nil
}

func (r *run) apps() error {
	if len(r.sol.Apps) == 0 {
		return nil
	}
	svc := &apps.Service{DMS: r.t}
	list, err := svc.List(r.ctx, r.subject, r.workspaceID)
	if err != nil {
		return err
	}
	byKey := map[string]string{}
	for _, a := range list {
		byKey[a.Key] = a.ID
	}
	for _, a := range r.sol.Apps {
		parameters, ok := r.resolve(KindApp, a.Key, a.Parameters)
		if !ok {
			continue
		}
		save := apps.Save{Name: a.Name, Description: a.Description, Source: a.Source, Parameters: orEmpty(parameters), State: appstore.State(a.State)}
		if save.State == "" {
			save.State = appstore.StateDraft
		}
		want := map[string]any{"name": save.Name, "description": save.Description, "source": save.Source, "parameters": save.Parameters, "state": string(save.State)}
		state := func(l apps.View) map[string]any {
			return map[string]any{"name": l.Name, "description": l.Description, "source": l.Source, "parameters": orEmpty(l.Parameters), "state": string(l.State)}
		}
		id := byKey[a.Key]
		var live apps.View
		var lives map[string]any
		if id != "" {
			if live, err = svc.Get(r.ctx, r.subject, id); err != nil {
				return failed(KindApp, a.Key, err)
			}
			lives = state(live)
		}
		action, kept := r.decide(KindApp, a.Key, want, lives, id != "")
		switch action {
		case doCreate:
			save.Key = a.Key
			created, err := svc.Create(r.ctx, r.subject, r.workspaceID, save)
			if err != nil {
				return failed(KindApp, a.Key, err)
			}
			r.change("create", KindApp, a.Key, a.Name)
			id, lives = created.ID, state(created)
		case doUpdate:
			updated, err := svc.Update(r.ctx, r.subject, id, live.Version, save)
			if err != nil {
				return failed(KindApp, a.Key, err)
			}
			detail := differences(lives, want)
			if updated.CurrentVersion != live.CurrentVersion {
				detail += fmt.Sprintf(" (version %d)", updated.CurrentVersion)
			}
			r.change("update", KindApp, a.Key, detail)
			lives = state(updated)
		}
		r.note(KindApp, a.Key, id, RoleOwns, want, lives, kept)
	}
	return nil
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// present returns the entries of want that have holds, in want's order.
func present(want, have []string) []string {
	var out []string
	for _, e := range want {
		if contains(have, e) {
			out = append(out, e)
		}
	}
	return out
}

// missing returns the entries of have that want lacks.
func missing(have, want []string) []string {
	var out []string
	for _, e := range have {
		if !contains(want, e) {
			out = append(out, e)
		}
	}
	return out
}

// differences names the properties that differ between two states.
func differences(from, to map[string]any) string {
	var names []string
	for k, v := range to {
		if hash(from[k]) != hash(v) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
