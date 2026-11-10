package solutions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"papergo/internal/apps"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/workflow"
)

const admin = "organization-admin"

// The tests run with a search index, as a deployment with search does: a
// role and the default built-in that fills it in every collection.
func init() {
	workflow.RegisterRole(&workflow.Role{Key: searchIndex, Name: "Search index"})
	workflow.RegisterBuiltIn(&workflow.BuiltIn{Key: searchIndex, Name: "Index for search", Role: searchIndex, Default: true, System: true,
		ParametersSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Source: `import { workflow } from "@papergo/workflow";
export default workflow<unknown, unknown, { collection: string }>({
  triggers: (p) => [{ type: "item.created", collection_id: p.collection }],
  concurrency: "coalesce",
  run: () => ({}),
});
`})
}

// place is a workspace to install solutions into, with the services tests
// read the result through.
type place struct {
	t   *testing.T
	ctx context.Context
	dms *dms.Service
	svc *Service
	ws  string
}

func newPlace(t *testing.T) *place {
	t.Helper()
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	ws, err := s.Create(ctx, admin, "", dms.CreateResource{Kind: "workspace", Name: "Casa"})
	if err != nil {
		t.Fatal(err)
	}
	return &place{t: t, ctx: ctx, dms: s, svc: &Service{DMS: s, PaperGo: "v0.3.0"}, ws: ws.ID}
}

func solution(t *testing.T, files fstest.MapFS) Solution {
	t.Helper()
	s, problems := Load(files)
	if len(problems) > 0 {
		t.Fatalf("problems:\n%s", problems)
	}
	return s
}

func (p *place) apply(files fstest.MapFS, o Options) Result {
	p.t.Helper()
	r, err := p.svc.Apply(p.ctx, admin, p.ws, solution(p.t, files), o)
	if err != nil {
		p.t.Fatal(err)
	}
	return r
}

// applied applies a solution that must not be refused.
func (p *place) applied(files fstest.MapFS, o Options) Result {
	p.t.Helper()
	r := p.apply(files, o)
	if !r.Applied {
		p.t.Fatalf("refused: %+v", r.Refusals)
	}
	return r
}

func (p *place) installation(key string) *Installation {
	p.t.Helper()
	r, err := readInstalled(p.ctx, p.dms, p.ws)
	if err != nil {
		p.t.Fatal(err)
	}
	in := r.Installations[key]
	return &in
}

func said(r Result) string {
	var lines []string
	for _, c := range r.Changes {
		lines = append(lines, c.Action+" "+c.Kind+" "+c.Key+" "+c.Detail)
	}
	for _, c := range r.Refusals {
		lines = append(lines, "REFUSED "+c.Kind+" "+c.Key+": "+c.Reason)
	}
	return strings.Join(lines, "\n")
}

func has(t *testing.T, r Result, want string) {
	t.Helper()
	if !strings.Contains(said(r), want) {
		t.Fatalf("want %q in:\n%s", want, said(r))
	}
}

// Applying a solution installs everything it declares; applying it again
// changes nothing; a plan changes nothing at all.
func TestApplyInstallsOnceAndIsThenUnchanged(t *testing.T) {
	p := newPlace(t)
	plan, err := p.svc.Plan(p.ctx, admin, p.ws, solution(t, tank()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applied || len(plan.Changes) == 0 || len(p.installation("tank").Objects) != 0 {
		t.Fatalf("a plan lists changes and makes none: %+v", plan)
	}
	fields, _ := p.dms.CatalogFields(p.ctx, admin)
	for _, f := range fields {
		if strings.HasPrefix(f.Key, "tank_") {
			t.Fatalf("the plan created %s", f.Key)
		}
	}

	first := p.applied(tank(), Options{})
	for _, want := range []string{"create field tank_measured_at", "create field tank_flag", "create content_type tank_reading", "create collection readings list Tank",
		"create view readings/all", "create workflow tank-levels", "create app tank-dashboard"} {
		has(t, first, want)
	}
	in := p.installation("tank")
	if in.Version != "1.0.0" || in.AppliedBy != admin || in.Settings["time_zone"] != "UTC" || in.Settings["capacity_l"] == nil {
		t.Fatalf("installation: %+v", in)
	}
	list := in.object(KindCollection, "readings")
	if list == nil || list.Role != RoleOwns {
		t.Fatalf("the list is not recorded as owned: %+v", in.Objects)
	}
	// References became IDs, settings their values.
	w, err := (&workflow.Service{DMS: p.dms}).Get(p.ctx, admin, in.object(KindWorkflow, "tank-levels").ID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Parameters["list"] != list.ID || w.Parameters["reading_type"] != in.object(KindContentType, "tank_reading").ID || w.Parameters["time_zone"] != "UTC" || w.State != "published" {
		t.Fatalf("workflow: %+v", w.Parameters)
	}
	a, err := (&apps.Service{DMS: p.dms}).Get(p.ctx, admin, in.object(KindApp, "tank-dashboard").ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Parameters["list"] != list.ID || a.State != "published" {
		t.Fatalf("app: %+v", a.Parameters)
	}

	// A plan says what stands for each collection and which settings it uses.
	if len(plan.Slots) != 1 || plan.Slots[0].State != SlotCreate || plan.Slots[0].Name != "Tank" || plan.Settings["time_zone"] != "UTC" || plan.SettingsForm == nil {
		t.Fatalf("plan slots %+v settings %+v", plan.Slots, plan.Settings)
	}
	again := p.applied(tank(), Options{})
	if len(again.Changes) != 0 || len(again.Customized) != 0 || again.From != "1.0.0" || again.Slots[0].State != SlotInstalled || again.Slots[0].CollectionID != list.ID {
		t.Fatalf("applying the same solution again:\n%s", said(again))
	}
}

// A newer version updates what it changed, as new versions of the sources,
// adds what is new, and leaves stored items as they were.
func TestApplyUpgradesInPlace(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	in := p.installation("tank")
	item, err := p.dms.Create(p.ctx, admin, in.object(KindCollection, "readings").ID, dms.CreateResource{Kind: "item", Name: "First",
		ContentTypeID: in.object(KindContentType, "tank_reading").ID, Values: map[string]any{"tank_measured_at": "2026-11-10T12:00:00Z", "tank_flag": "ok"}})
	if err != nil {
		t.Fatal(err)
	}

	next := edit("solution.json", `"version": "1.0.0"`, `"version": "1.1.0"`)
	next["solution.json"] = &fstest.MapFile{Data: []byte(strings.NewReplacer(
		`{"key": "tank_flag", "label": "Check", "type": "choice", "choices": ["ok", "check"]}`,
		`{"key": "tank_flag", "label": "Check", "type": "choice", "choices": ["ok", "check", "reviewed"]}, {"key": "tank_notes", "label": "Notes", "type": "text"}`,
		`["tank_measured_at", "tank_flag"]`, `["tank_measured_at", "tank_flag", "tank_notes"]`,
		`["$name", "tank_measured_at"]`, `["$name", "tank_measured_at", "tank_notes"]`,
	).Replace(string(next["solution.json"].Data)))}
	next["apps/dashboard.tsx"] = &fstest.MapFile{Data: []byte(strings.Replace(tankApp, `title: "Capacity"`, `title: "Tank capacity"`, 1))}

	r := p.applied(next, Options{})
	for _, want := range []string{"create field tank_notes", "update field tank_flag choices", "update content_type tank_reading fields", "update view readings/all columns", "update app tank-dashboard source (version 2)"} {
		has(t, r, want)
	}
	if len(r.Changes) != 5 || r.From != "1.0.0" || p.installation("tank").Version != "1.1.0" {
		t.Fatalf("upgrade:\n%s", said(r))
	}
	kept, err := p.dms.Get(p.ctx, admin, item.ID)
	if err != nil || kept.Values["tank_flag"] != "ok" {
		t.Fatalf("the stored item changed: %+v %v", kept, err)
	}
	if again := p.applied(next, Options{}); len(again.Changes) != 0 {
		t.Fatalf("applying the upgrade again:\n%s", said(again))
	}
}

// What would break stored values is refused, and then nothing is applied.
func TestApplyRefusesWhatBreaksData(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	cases := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"a field's type", edit("solution.json", `"type": "datetime"`, `"type": "date"`), "REFUSED field tank_measured_at: is a datetime field"},
		{"a choice removed", edit("solution.json", `["ok", "check"]`, `["ok"]`), "REFUSED field tank_flag: would lose the choices check"},
		{"a field leaving its type", edit("solution.json", `"fields": ["tank_measured_at", "tank_flag"]`, `"fields": ["tank_measured_at"]`), "REFUSED content_type tank_reading: would lose the fields tank_flag"},
		{"another PaperGo", edit("solution.json", `">=0.1.0"`, `">=9.0.0"`), "REFUSED solution tank: is written for PaperGo >=9.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := c.files
			files["apps/dashboard.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(tank()["apps/dashboard.json"].Data), `"Dashboard"`, `"Tank dashboard"`, 1))}
			r := p.apply(files, Options{})
			if r.Applied {
				t.Fatalf("applied:\n%s", said(r))
			}
			has(t, r, c.want)
			// The rename that came with the refused change was not applied either.
			a, err := (&apps.Service{DMS: p.dms}).Get(p.ctx, admin, p.installation("tank").object(KindApp, "tank-dashboard").ID)
			if err != nil || a.Name != "Dashboard" {
				t.Fatalf("a refused apply changed the app: %q %v", a.Name, err)
			}
		})
	}
}

// A choice or a field added in PaperGo is no change of the solution's: it
// stays through every later apply, and a pull brings it to the files.
func TestApplyKeepsWhatWasAddedInPaperGo(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	fields, err := p.dms.CatalogFields(p.ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.Key == "tank_flag" {
			if _, err := p.dms.UpdateCatalogField(p.ctx, admin, f.ID, f.Version, dms.UpdateField{Choices: &[]string{"ok", "urgent", "check"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	extra, err := p.dms.CreateCatalogField(p.ctx, admin, dms.CreateField{Key: "tank_extra", Label: "Extra", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	types, _ := p.dms.CatalogContentTypes(p.ctx, admin)
	for _, typ := range types {
		if typ.Key == "tank_reading" {
			if _, err := p.dms.UpdateContentType(p.ctx, admin, typ.ID, typ.Version, dms.ContentTypeInput{Key: typ.Key, Name: typ.Name, FieldIds: append(typ.FieldIds, extra.ID)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	if same := p.applied(tank(), Options{}); len(same.Changes) != 0 || len(same.Customized) != 0 {
		t.Fatalf("an added choice and field are left alone:\n%s\n%v", said(same), same.Customized)
	}
	next := edit("solution.json", `["ok", "check"]`, `["ok", "check", "reviewed"]`)
	has(t, p.applied(next, Options{}), "update field tank_flag choices")
	fields, _ = p.dms.CatalogFields(p.ctx, admin)
	for _, f := range fields {
		if f.Key == "tank_flag" && strings.Join(f.Choices, ",") != "ok,check,reviewed,urgent" {
			t.Fatalf("choices: %v", f.Choices)
		}
	}
	types, _ = p.dms.CatalogContentTypes(p.ctx, admin)
	for _, typ := range types {
		if typ.Key == "tank_reading" && !contains(typ.FieldIds, extra.ID) {
			t.Fatalf("the added field left the type: %v", typ.FieldIds)
		}
	}
	// A choice the solution itself drops is still refused.
	has(t, p.apply(edit("solution.json", `["ok", "check"]`, `["ok", "reviewed"]`), Options{}), "REFUSED field tank_flag: would lose the choices check")
}

// A collection's search setting is the solution's like its name: turned
// on in PaperGo, it stays, and a pull brings it to the files.
func TestApplyKeepsAnEditedSearchSetting(t *testing.T) {
	p := newPlace(t)
	has(t, p.applied(tank(), Options{}), "update collection readings search")
	list := p.installation("tank").object(KindCollection, "readings").ID
	indexed := func() (bool, int) {
		t.Helper()
		on, version, ok, err := indexedForSearch(p.ctx, p.dms, admin, p.ws, list)
		if err != nil || !ok {
			t.Fatalf("search: %v %v", ok, err)
		}
		return on, version
	}
	on, version := indexed()
	if on {
		t.Fatal("the solution's list is indexed, and it says search: false")
	}
	if _, err := (&workflow.Service{DMS: p.dms}).UpdateBuiltIn(p.ctx, admin, p.ws, searchIndex, workflow.SetBuiltIn{CollectionID: list, AutomaticEnabled: true, Version: version}); err != nil {
		t.Fatal(err)
	}
	same := p.applied(tank(), Options{})
	if on, _ = indexed(); !on || len(same.Changes) != 0 || len(same.Customized) != 1 || same.Customized[0] != "collection:readings" {
		t.Fatalf("search turned on in PaperGo was turned off again: %v\n%s\n%v", on, said(same), same.Customized)
	}
	pulled, err := p.svc.Pull(p.ctx, admin, p.ws, "tank")
	if err != nil || pulled.Manifest.Collections[0].Search == nil || !*pulled.Manifest.Collections[0].Search {
		t.Fatalf("pull did not bring the search setting: %+v %v", pulled.Manifest.Collections[0], err)
	}
}

// A lookup field may look up the solution's own collection, or one it
// uses: it is made once the collection is there, and its type completed.
func TestApplyInstallsLookupFields(t *testing.T) {
	p := newPlace(t)
	withLookup := func() fstest.MapFS {
		files := tank()
		files["solution.json"] = &fstest.MapFile{Data: []byte(strings.NewReplacer(
			`{"key": "tank_flag", "label": "Check", "type": "choice", "choices": ["ok", "check"]}`,
			`{"key": "tank_flag", "label": "Check", "type": "choice", "choices": ["ok", "check"]}, {"key": "tank_previous", "label": "Previous reading", "type": "lookup", "options": {"lookup_container_id": "$collection:readings"}}`,
			`["tank_measured_at", "tank_flag"]`, `["tank_measured_at", "tank_flag", "tank_previous"]`,
			`["$name", "tank_measured_at"]`, `["$name", "tank_measured_at", "tank_previous"]`,
		).Replace(string(files["solution.json"].Data)))}
		return files
	}
	first := p.applied(withLookup(), Options{})
	for _, want := range []string{"create content_type tank_reading 2 field(s)", "create collection readings list Tank", "create field tank_previous lookup", "update content_type tank_reading fields", "create view readings/all"} {
		has(t, first, want)
	}
	in := p.installation("tank")
	list := in.object(KindCollection, "readings").ID
	fields, err := p.dms.Fields(p.ctx, admin, list)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fields {
		if f.Key == "tank_previous" {
			found = f.Options.LookupContainerID == list
		}
	}
	if !found {
		t.Fatalf("the list has no lookup field that looks it up: %+v", fields)
	}
	if again := p.applied(withLookup(), Options{}); len(again.Changes) != 0 || len(again.Customized) != 0 {
		t.Fatalf("applying again:\n%s\n%v", said(again), again.Customized)
	}

	// Another solution's field looks up the list it uses.
	uses := bills()
	uses["solution.json"] = &fstest.MapFile{Data: []byte(strings.NewReplacer(
		`{"key": "bills_invoice", "label": "Invoice number", "type": "text"}`,
		`{"key": "bills_invoice", "label": "Invoice number", "type": "text"}, {"key": "bills_reading", "label": "Reading", "type": "lookup", "options": {"lookup_container_id": "$collection:readings"}}`,
		`"extends": {"tank_reading": ["bills_invoice"]}`, `"extends": {"tank_reading": ["bills_invoice", "bills_reading"]}`,
	).Replace(string(uses["solution.json"].Data)))}
	r := p.applied(uses, Options{})
	has(t, r, "create field bills_reading lookup")
	has(t, r, "extend extension readings/tank_reading bills_invoice, bills_reading")
	if again := p.applied(uses, Options{}); len(again.Changes) != 0 {
		t.Fatalf("applying the second solution again:\n%s", said(again))
	}
}

// What was edited in PaperGo stays, and is asked about when the solution
// changes the same thing.
func TestApplyKeepsWhatWasEdited(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	wf := &workflow.Service{DMS: p.dms}
	id := p.installation("tank").object(KindWorkflow, "tank-levels").ID
	w, err := wf.Get(p.ctx, admin, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wf.Update(p.ctx, admin, id, w.Version, workflow.Save{Name: "Niveles", Source: w.Source, Parameters: w.Parameters, State: w.State}); err != nil {
		t.Fatal(err)
	}

	same := p.applied(tank(), Options{})
	if len(same.Changes) != 0 || len(same.Customized) != 1 || same.Customized[0] != "workflow:tank-levels" {
		t.Fatalf("an edited workflow is left and reported:\n%s\n%v", said(same), same.Customized)
	}

	next := edit("workflows/levels.json", `"name": "Levels"`, `"name": "Tank levels"`)
	conflict := p.apply(next, Options{})
	if conflict.Applied {
		t.Fatalf("a conflict was applied:\n%s", said(conflict))
	}
	has(t, conflict, `REFUSED workflow tank-levels: was edited in PaperGo and the solution changes it too: decide "workflow:tank-levels"`)

	kept := p.applied(next, Options{Decisions: map[string]string{"workflow:tank-levels": DecisionKeep}})
	if w, _ = wf.Get(p.ctx, admin, id); w.Name != "Niveles" || len(kept.Changes) != 0 {
		t.Fatalf("keep: %q\n%s", w.Name, said(kept))
	}
	// Kept once, it is not asked about again.
	if again := p.applied(next, Options{}); len(again.Refusals) != 0 || len(again.Customized) != 1 {
		t.Fatalf("a kept edit was asked about again:\n%s", said(again))
	}

	newer := edit("workflows/levels.json", `"name": "Levels"`, `"name": "Tank level check"`)
	replaced := p.applied(newer, Options{Decisions: map[string]string{"workflow:tank-levels": DecisionReplace}})
	if w, _ = wf.Get(p.ctx, admin, id); w.Name != "Tank level check" {
		t.Fatalf("replace: %q\n%s", w.Name, said(replaced))
	}
}

// bills is a second solution that works on the tank's list.
func bills() fstest.MapFS {
	return fstest.MapFS{
		"solution.json": {Data: []byte(`{
  "key": "bills", "name": "Bills", "version": "1.0.0",
  "fields": [{"key": "bills_invoice", "label": "Invoice number", "type": "text"}],
  "collections": [{"key": "readings", "kind": "list",
    "use": {"solution": "tank", "collection": "readings", "version": ">=1.0.0"},
    "expects": {"tank_reading": ["tank_measured_at"]},
    "extends": {"tank_reading": ["bills_invoice"]},
    "views": [{"key": "invoiced", "name": "Invoiced", "columns": ["$name", "bills_invoice", "tank_measured_at"], "query": {"content_type_id": "$type:tank_reading"}}]}]
}`)},
		"workflows/file.json": {Data: []byte(`{"key": "bills-file", "name": "File bills", "script": "file.ts", "state": "published",
  "parameters": {"list": "$collection:readings", "reading_type": "$type:tank_reading"}}`)},
		"workflows/file.ts": {Data: []byte(tankWorkflow)},
	}
}

// Two solutions share a list: the second uses the first one's, relies on
// what it expects there and adds its own field to the list's type.
func TestSolutionsShareACollection(t *testing.T) {
	p := newPlace(t)
	alone := p.apply(bills(), Options{})
	if alone.Applied {
		t.Fatal("a solution was installed without the one it uses")
	}
	has(t, alone, `REFUSED collection readings: uses a collection of the solution "tank", which is not installed in this workspace`)

	p.applied(tank(), Options{})
	r := p.applied(bills(), Options{})
	for _, want := range []string{"create field bills_invoice", "extend extension readings/tank_reading bills_invoice", "create view readings/invoiced", "create workflow bills-file"} {
		has(t, r, want)
	}
	tankList, used := p.installation("tank").object(KindCollection, "readings"), p.installation("bills").object(KindCollection, "readings")
	if used == nil || used.ID != tankList.ID || used.Role != RoleUses {
		t.Fatalf("the list is the tank's, used: %+v", used)
	}
	// An item of the shared list takes both solutions' fields.
	if _, err := p.dms.Create(p.ctx, admin, tankList.ID, dms.CreateResource{Kind: "item", Name: "Fill", ContentTypeID: p.installation("tank").object(KindContentType, "tank_reading").ID,
		Values: map[string]any{"tank_measured_at": "2026-11-10T12:00:00Z", "bills_invoice": "F-2026-17"}}); err != nil {
		t.Fatal(err)
	}
	if again := p.applied(bills(), Options{}); len(again.Changes) != 0 {
		t.Fatalf("applying again:\n%s", said(again))
	}
	// The catalog type is untouched: the field is the list's own.
	catalogType, err := p.dms.GetContentType(p.ctx, admin, p.installation("tank").object(KindContentType, "tank_reading").ID)
	if err != nil || len(catalogType.FieldIds) != 2 {
		t.Fatalf("the catalog type changed: %+v %v", catalogType, err)
	}

	lacking := bills()
	lacking["solution.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(lacking["solution.json"].Data), `"expects": {"tank_reading": ["tank_measured_at"]}`, `"expects": {"tank_reading": ["tank_measured_at", "tank_level"]}`, 1))}
	has(t, p.apply(lacking, Options{}), "REFUSED collection readings: expects the field tank_level on tank_reading, which lacks it")
	old := bills()
	old["solution.json"] = &fstest.MapFile{Data: []byte(strings.Replace(string(old["solution.json"].Data), `">=1.0.0"`, `">=2.0.0"`, 1))}
	has(t, p.apply(old, Options{}), "REFUSED collection readings: needs tank >=2.0.0; version 1.0.0 is installed")
}

// A slot bound to a list that exists keeps the list's name; the solution
// adds its types and views there.
func TestApplyBindsAnExistingCollection(t *testing.T) {
	p := newPlace(t)
	mine, err := p.dms.Create(p.ctx, admin, p.ws, dms.CreateResource{Kind: "list", Name: "Depósito"})
	if err != nil {
		t.Fatal(err)
	}
	library, err := p.dms.Create(p.ctx, admin, p.ws, dms.CreateResource{Kind: "library", Name: "Papeles"})
	if err != nil {
		t.Fatal(err)
	}
	has(t, p.apply(tank(), Options{Bindings: map[string]string{"readings": library.ID}}), `REFUSED collection readings: is bound to "Papeles", a library; the solution needs a list`)

	r := p.applied(tank(), Options{Bindings: map[string]string{"readings": mine.ID}})
	for _, want := range []string{"bind collection readings Depósito", "attach collection readings tank_reading", "create view readings/all"} {
		has(t, r, want)
	}
	list, err := p.dms.Get(p.ctx, admin, mine.ID)
	if err != nil || list.Name != "Depósito" {
		t.Fatalf("the bound list was renamed: %q %v", list.Name, err)
	}
	if in := p.installation("tank"); in.Bindings["readings"] != mine.ID || in.object(KindCollection, "readings").Role != RoleUses {
		t.Fatalf("the binding is not kept: %+v", in)
	}
	// Later applies need not name the binding again.
	if again := p.applied(tank(), Options{}); len(again.Changes) != 0 {
		t.Fatalf("applying again:\n%s", said(again))
	}
	// Unbound, the solution makes its own list, and the bound one stays as it is.
	unbound := p.applied(tank(), Options{Bindings: map[string]string{"readings": ""}})
	has(t, unbound, "create collection readings list Tank")
	in := p.installation("tank")
	if o := in.object(KindCollection, "readings"); len(in.Bindings) != 0 || o.ID == mine.ID || o.Role != RoleOwns || unbound.Slots[0].State != SlotCreate {
		t.Fatalf("the slot is still bound: %+v %+v", in.Bindings, unbound.Slots)
	}
	if !strings.Contains(strings.Join(unbound.Warnings, "\n"), "readings now stands for another collection") {
		t.Fatalf("warnings: %v", unbound.Warnings)
	}
	if again := p.applied(tank(), Options{}); len(again.Changes) != 0 || again.Slots[0].State != SlotInstalled {
		t.Fatalf("applying after unbinding:\n%s\n%+v", said(again), again.Slots)
	}
}

// A view someone made is not the solution's for having its name: the
// solution does not take it over, and removing the solution leaves it.
func TestApplyLeavesAViewOfTheSameName(t *testing.T) {
	p := newPlace(t)
	mine, err := p.dms.Create(p.ctx, admin, p.ws, dms.CreateResource{Kind: "list", Name: "Depósito"})
	if err != nil {
		t.Fatal(err)
	}
	var in dms.ViewInput
	if err := json.Unmarshal([]byte(`{"name": "Readings", "layout": "table", "columns": ["$name"]}`), &in); err != nil {
		t.Fatal(err)
	}
	view, err := p.dms.CreateView(p.ctx, admin, mine.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	bound := Options{Bindings: map[string]string{"readings": mine.ID}}
	has(t, p.apply(tank(), bound), `REFUSED view readings/all: the collection has a view named "Readings" that is not the solution's`)

	in.Name = "My readings"
	if _, err := p.dms.UpdateView(p.ctx, admin, view.ID, view.Version, in); err != nil {
		t.Fatal(err)
	}
	has(t, p.applied(tank(), bound), "create view readings/all")
	if _, err := p.svc.Remove(p.ctx, admin, p.ws, "tank"); err != nil {
		t.Fatal(err)
	}
	if kept, err := p.dms.View(p.ctx, admin, view.ID); err != nil || strings.Join(kept.Columns, ",") != "$name" {
		t.Fatalf("the view someone made did not stay as it was: %+v %v", kept, err)
	}
}

// Something PaperGo already has can stand in the way of what a solution
// brings, such as a workflow of the same name: the plan names the object,
// and nothing is applied.
func TestApplyNamesWhatStandsInTheWay(t *testing.T) {
	p := newPlace(t)
	manual := `import { workflow } from "@papergo/workflow";
export default workflow({ triggers: [{ type: "manual" }], async run() {} });
`
	if _, err := (&workflow.Service{DMS: p.dms}).Create(p.ctx, admin, p.ws, workflow.Save{Key: "mine", Name: "Levels", Source: manual}); err != nil {
		t.Fatal(err)
	}
	r := p.apply(tank(), Options{})
	if r.Applied {
		t.Fatalf("applied:\n%s", said(r))
	}
	has(t, r, "REFUSED workflow tank-levels: conflicts with something PaperGo already has")
	fields, _ := p.dms.CatalogFields(p.ctx, admin)
	for _, f := range fields {
		if strings.HasPrefix(f.Key, "tank_") {
			t.Fatalf("a refused apply created %s", f.Key)
		}
	}
}

// Settings given once are kept; a default reads the organization's
// preference; pausing keeps workflows from starting on their own.
func TestApplySettingsAndPause(t *testing.T) {
	p := newPlace(t)
	paused := true
	p.applied(tank(), Options{Settings: map[string]any{"capacity_l": 1000, "time_zone": "Europe/Madrid"}, Paused: &paused})
	in := p.installation("tank")
	w, err := (&workflow.Service{DMS: p.dms}).Get(p.ctx, admin, in.object(KindWorkflow, "tank-levels").ID)
	if err != nil {
		t.Fatal(err)
	}
	if !in.Paused || w.AutomaticEnabled || w.Parameters["time_zone"] != "Europe/Madrid" {
		t.Fatalf("paused %v, automatic %v, parameters %+v", in.Paused, w.AutomaticEnabled, w.Parameters)
	}
	again := p.applied(tank(), Options{})
	if len(again.Changes) != 0 || p.installation("tank").Settings["time_zone"] != "Europe/Madrid" {
		t.Fatalf("settings were not kept:\n%s", said(again))
	}
	// An apply that does not say stays paused, and says so.
	if !again.Paused || !p.installation("tank").Paused {
		t.Fatalf("an apply resumed a paused installation: %+v", again)
	}
	refused := p.apply(tank(), Options{Settings: map[string]any{"capacity_l": "a lot"}})
	if has(t, refused, "REFUSED settings tank"); len(refused.Changes) != 0 {
		t.Fatalf("a refused setting is shown with changes it would not make:\n%s", said(refused))
	}
	has(t, p.apply(tank(), Options{Settings: map[string]any{"capacity": 900}}), `REFUSED settings tank: the solution has no setting "capacity"; it has capacity_l, time_zone`)
}

// A setting nobody gave follows its default, read again at each apply; one
// given by hand stays until it is returned to the default.
func TestSettingsFollowTheirDefaults(t *testing.T) {
	p := newPlace(t)
	first := p.applied(tank(), Options{Settings: map[string]any{"capacity_l": 1000}})
	if first.Settings["time_zone"] != "UTC" || strings.Join(first.SettingsGiven, ",") != "capacity_l" {
		t.Fatalf("first: %+v given %v", first.Settings, first.SettingsGiven)
	}
	madrid := "Europe/Madrid"
	if _, err := p.dms.Directory().PatchPreferences(p.ctx, admin, true, 0, map[string]*string{"time_zone": &madrid}); err != nil {
		t.Fatal(err)
	}
	moved := p.applied(tank(), Options{})
	has(t, moved, "update workflow tank-levels parameters")
	if in := p.installation("tank"); in.Settings["time_zone"] != "Europe/Madrid" || in.Settings["capacity_l"] != float64(1000) || len(in.SettingsGiven) != 1 {
		t.Fatalf("the default was not read again, or the given value was lost: %+v given %+v", in.Settings, in.SettingsGiven)
	}
	// A newer version's default reaches a setting nobody gave, and not one given.
	next := edit("solution.json", `"default": 750`, `"default": 900`)
	if r := p.applied(next, Options{}); fmt.Sprint(r.Settings["capacity_l"]) != "1000" {
		t.Fatalf("a given value followed the default: %+v", r.Settings)
	}
	back := p.applied(next, Options{Settings: map[string]any{"capacity_l": nil}})
	if fmt.Sprint(back.Settings["capacity_l"]) != "900" || len(back.SettingsGiven) != 0 {
		t.Fatalf("null returns a setting to its default: %+v given %v", back.Settings, back.SettingsGiven)
	}
	has(t, back, "update app tank-dashboard parameters")
}

// Installing needs solution.manage and manage access to the workspace.
func TestApplyNeedsTheScope(t *testing.T) {
	p := newPlace(t)
	if err := p.dms.SetWorkspaceMember(p.ctx, admin, p.ws, dms.WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.svc.Apply(p.ctx, "bob", p.ws, solution(t, tank()), Options{}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("an owner without solution.manage installed a solution: %v", err)
	}
	directory := identity.Directory{DB: p.dms.Directory().DB}
	role, err := directory.SaveRole(p.ctx, admin, "", 0, identity.RoleInput{Name: "Installers", Scopes: []string{Scope}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(p.ctx, admin, role.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	// The scope lets bob install; what the solution brings still needs its own.
	if _, err := p.svc.Apply(p.ctx, "bob", p.ws, solution(t, tank()), Options{}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("bob has no right to write catalog fields or apps: %v", err)
	}
}
