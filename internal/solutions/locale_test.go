package solutions

import (
	"strings"
	"testing"
	"testing/fstest"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// tankInSpanish is the tank with its display names in Spanish.
func tankInSpanish() fstest.MapFS {
	files := tank()
	files["locales/es.json"] = &fstest.MapFile{Data: []byte(`{
  "name": "Depósito",
  "settings": {"capacity_l": {"title": "Capacidad"}},
  "fields": {"tank_measured_at": {"label": "Medido el"}, "tank_flag": {"label": "Revisión", "description": "Si hay que mirarlo."}},
  "content_types": {"tank_reading": "Medición"},
  "collections": {"readings": {"name": "Depósito", "views": {"all": "Mediciones"}}},
  "workflows": {"tank-levels": {"name": "Niveles"}},
  "apps": {"tank-dashboard": {"name": "Panel", "description": "El depósito de un vistazo."}}
}`)}
	return files
}

// A solution installs in a language it has names in, keeps that language,
// and changes to another when asked.
func TestApplyInALanguage(t *testing.T) {
	p := newPlace(t)
	plain := p.applied(tankInSpanish(), Options{})
	if plain.Language != "" || len(plain.Languages) != 1 || plain.Languages[0] != "es" {
		t.Fatalf("an English organization installs the solution's own text: %q %v", plain.Language, plain.Languages)
	}
	has(t, p.apply(tankInSpanish(), Options{Language: "fr"}), `REFUSED solution tank: has no display names in "fr"; it has es`)

	spanish := p.applied(tankInSpanish(), Options{Language: "es-MX"})
	if spanish.Language != "es" || spanish.Name != "Depósito" {
		t.Fatalf("es-MX reads the es names: %q %q", spanish.Language, spanish.Name)
	}
	for _, want := range []string{"update field tank_measured_at label", "update field tank_flag label, options", "update content_type tank_reading name",
		"update collection readings name", "update view readings/all name", "update workflow tank-levels name", "update app tank-dashboard description, name"} {
		has(t, spanish, want)
	}
	in := p.installation("tank")
	list, err := p.dms.Get(p.ctx, admin, in.object(KindCollection, "readings").ID)
	if err != nil || list.Name != "Depósito" || in.Language != "es" || in.Manifest.Name != "Tank" || in.Locales["es"].Name != "Depósito" {
		t.Fatalf("installed in Spanish, kept as the files say: %q %+v %v", list.Name, in.Manifest.Name, err)
	}
	// The language is the installation's: the next apply need not name it.
	if again := p.applied(tankInSpanish(), Options{}); len(again.Changes) != 0 || again.Language != "es" {
		t.Fatalf("applying again:\n%s", said(again))
	}
	back := p.applied(tankInSpanish(), Options{Language: LanguageNone})
	if list, _ = p.dms.Get(p.ctx, admin, list.ID); back.Language != "" || list.Name != "Tank" {
		t.Fatalf("back to the solution's own text: %q %q", back.Language, list.Name)
	}
}

// A locale names only what the solution has, and travels in its package.
func TestLocalesAreCheckedAndPacked(t *testing.T) {
	s, problems := check(t, tankInSpanish())
	if len(problems) > 0 {
		t.Fatalf("problems:\n%s", problems)
	}
	raw, err := Pack(s)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := OpenZIP(raw)
	again, problems := Load(files)
	if len(problems) > 0 || again.Locales["es"].ContentTypes["tank_reading"] != "Medición" {
		t.Fatalf("the packed solution lost its locale: %+v %v", again.Locales, problems)
	}
	for old, want := range map[string]string{
		`"tank_flag": {"label": "Revisión"`:     `locales/es.json: fields.tank_colour: is not a field of the solution`,
		`"views": {"all": "Mediciones"}`:        `locales/es.json: collections.readings.views.todas: is not a view of the collection`,
		`"workflows": {"tank-levels"`:           `locales/es.json: workflows.tank-niveles: is not a workflow of the solution`,
		`"settings": {"capacity_l"`:             `locales/es.json: settings.capacidad: is not a setting of the solution`,
		`"content_types": {"tank_reading": "Me`: `locales/es.json: content_types.tank_medicion: is not a content type of the solution`,
	} {
		broken := tankInSpanish()
		text := string(broken["locales/es.json"].Data)
		replacement := map[string]string{
			`"tank_flag": {"label": "Revisión"`:     `"tank_colour": {"label": "Revisión"`,
			`"views": {"all": "Mediciones"}`:        `"views": {"todas": "Mediciones"}`,
			`"workflows": {"tank-levels"`:           `"workflows": {"tank-niveles"`,
			`"settings": {"capacity_l"`:             `"settings": {"capacidad"`,
			`"content_types": {"tank_reading": "Me`: `"content_types": {"tank_medicion": "Me`,
		}[old]
		if !strings.Contains(text, old) {
			t.Fatal("the locale does not hold ", old)
		}
		broken["locales/es.json"] = &fstest.MapFile{Data: []byte(strings.Replace(text, old, replacement, 1))}
		if _, problems := check(t, broken); !strings.Contains(problems.Error(), want) {
			t.Errorf("want %q, got:\n%s", want, problems)
		}
	}
	named := tankInSpanish()
	named["locales/spanish.json"] = named["locales/es.json"]
	if _, problems := check(t, named); !strings.Contains(problems.Error(), "locales/spanish.json: is named after its language") {
		t.Errorf("a locale not named after a language: %s", problems)
	}
}

// Pulling an installation in a language puts what was edited in PaperGo
// in that language's locale and leaves the solution's own text alone.
func TestPullKeepsTheLanguageApart(t *testing.T) {
	p := newPlace(t)
	p.applied(tankInSpanish(), Options{Language: "es"})
	in := p.installation("tank")
	wf := &workflow.Service{DMS: p.dms}
	w, err := wf.Get(p.ctx, admin, in.object(KindWorkflow, "tank-levels").ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wf.Update(p.ctx, admin, w.ID, w.Version, workflow.Save{Name: "Niveles del depósito", Source: w.Source, Parameters: w.Parameters, State: w.State}); err != nil {
		t.Fatal(err)
	}
	list, _ := p.dms.Get(p.ctx, admin, in.object(KindCollection, "readings").ID)
	name := "Gasóleo"
	if _, err = p.dms.Update(p.ctx, admin, list.ID, list.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}

	pulled, err := p.svc.Pull(p.ctx, admin, p.ws, "tank")
	if err != nil {
		t.Fatal(err)
	}
	es := pulled.Locales["es"]
	if pulled.Manifest.Name != "Tank" || pulled.Manifest.Collections[0].Name != "Tank" || pulled.Workflows[0].Name != "Levels" || pulled.Manifest.Fields[0].Label != "Measured at" {
		t.Fatalf("the solution's own text changed: %+v %q", pulled.Manifest.Collections[0], pulled.Workflows[0].Name)
	}
	if es.Collections["readings"].Name != "Gasóleo" || es.Workflows["tank-levels"].Name != "Niveles del depósito" || es.Fields["tank_flag"].Label != "Revisión" || es.Collections["readings"].Views["all"] != "Mediciones" || es.Settings["capacity_l"].Title != "Capacidad" {
		t.Fatalf("the Spanish names: %+v", es)
	}
	if problems := Check(p.ctx, pulled); len(problems) > 0 {
		t.Fatalf("what was pulled has problems:\n%s", problems)
	}
	if r, err := p.svc.Apply(p.ctx, admin, p.ws, pulled, Options{}); err != nil || !r.Applied || len(r.Changes) != 0 || len(r.Customized) != 0 {
		t.Fatalf("applying what was pulled: %v\n%s\n%v", err, said(r), r.Customized)
	}
}

// A first installation takes the organization's language when the solution
// has names in it.
func TestFirstInstallTakesTheOrganizationsLanguage(t *testing.T) {
	p := newPlace(t)
	spanish := "es"
	if _, err := p.dms.Directory().PatchPreferences(p.ctx, admin, true, 0, map[string]*string{"language": &spanish}); err != nil {
		t.Fatal(err)
	}
	r := p.applied(tankInSpanish(), Options{})
	if r.Language != "es" {
		t.Fatalf("language %q", r.Language)
	}
	has(t, r, "create collection readings list Depósito")
	has(t, r, "create workflow tank-levels Niveles")
	if form := r.SettingsForm["properties"].(map[string]any)["capacity_l"].(map[string]any); form["title"] != "Capacidad" {
		t.Fatalf("the settings form is asked in Spanish: %+v", form)
	}
}
