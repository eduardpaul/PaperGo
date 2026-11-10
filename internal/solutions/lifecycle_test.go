package solutions

import (
	"strings"
	"testing"

	"papergo/internal/apps"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// Removing a solution deletes what it runs and shows, and keeps its list,
// types, fields and items. A solution another one uses is not removed.
func TestRemoveKeepsWhatHoldsItems(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	p.applied(bills(), Options{})
	in := p.installation("tank")
	list, typ := in.object(KindCollection, "readings"), in.object(KindContentType, "tank_reading")
	item, err := p.dms.Create(p.ctx, admin, list.ID, dms.CreateResource{Kind: "item", Name: "First", ContentTypeID: typ.ID, Values: map[string]any{"tank_measured_at": "2026-11-10T12:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.svc.Remove(p.ctx, admin, p.ws, "tank"); err == nil || !strings.Contains(err.Error(), `the solution bills uses the collection "readings" of tank; remove bills first`) {
		t.Fatalf("a solution in use was removed: %v", err)
	}
	if _, err := p.svc.Remove(p.ctx, admin, p.ws, "bills"); err != nil {
		t.Fatal(err)
	}
	r, err := p.svc.Remove(p.ctx, admin, p.ws, "tank")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete workflow tank-levels", "delete app tank-dashboard", "delete view readings/all", "keep collection readings", "keep content_type tank_reading", "keep field tank_flag"} {
		has(t, r, want)
	}
	if _, err := (&workflow.Service{DMS: p.dms}).Get(p.ctx, admin, in.object(KindWorkflow, "tank-levels").ID); err == nil {
		t.Fatal("the workflow is still there")
	}
	if _, err := (&apps.Service{DMS: p.dms}).Get(p.ctx, admin, in.object(KindApp, "tank-dashboard").ID); err == nil {
		t.Fatal("the app is still there")
	}
	if kept, err := p.dms.Get(p.ctx, admin, item.ID); err != nil || kept.Values["tank_measured_at"] == nil {
		t.Fatalf("the item did not stay: %v", err)
	}
	if list, err := p.svc.List(p.ctx, admin, p.ws); err != nil || len(list) != 0 {
		t.Fatalf("installations left: %+v %v", list, err)
	}
	// Installing again finds the fields and the type, and makes a new list.
	again := p.applied(tank(), Options{})
	has(t, again, "create collection readings")
	if strings.Contains(said(again), "create field") || strings.Contains(said(again), "create content_type") {
		t.Fatalf("the catalog entries were not found again:\n%s", said(again))
	}
}

// What a version no longer declares stays, marked, until it is pruned.
func TestApplyRetiresAndPrunes(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{})
	id := p.installation("tank").object(KindApp, "tank-dashboard").ID
	without := tank()
	delete(without, "apps/dashboard.json")
	delete(without, "apps/dashboard.tsx")

	r := p.applied(without, Options{})
	if len(r.Changes) != 0 || !strings.Contains(strings.Join(r.Warnings, "\n"), "app tank-dashboard is no longer part of the solution; it was kept") {
		t.Fatalf("retire:\n%s\n%v", said(r), r.Warnings)
	}
	if o := p.installation("tank").object(KindApp, "tank-dashboard"); o == nil || !o.Retired {
		t.Fatalf("the app is not marked: %+v", o)
	}
	if again := p.applied(without, Options{}); strings.Contains(strings.Join(again.Warnings, "\n"), "tank-dashboard") {
		t.Fatalf("said again: %v", again.Warnings)
	}
	pruned := p.applied(without, Options{Prune: true})
	has(t, pruned, "delete app tank-dashboard no longer part of the solution")
	if _, err := (&apps.Service{DMS: p.dms}).Get(p.ctx, admin, id); err == nil || p.installation("tank").object(KindApp, "tank-dashboard") != nil {
		t.Fatal("the app was not pruned")
	}
}

// Pulling returns the solution as it is in the workspace, with what was
// edited there; applying what was pulled changes nothing.
func TestPullReturnsWhatIsLive(t *testing.T) {
	p := newPlace(t)
	p.applied(tank(), Options{Settings: map[string]any{"time_zone": "Europe/Madrid"}})
	in := p.installation("tank")
	wf := &workflow.Service{DMS: p.dms}
	w, err := wf.Get(p.ctx, admin, in.object(KindWorkflow, "tank-levels").ID)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(w.Source, "async run() {}", "async run() { /* edited in PaperGo */ }", 1)
	if _, err = wf.Update(p.ctx, admin, w.ID, w.Version, workflow.Save{Name: "Niveles", Source: source, Parameters: w.Parameters, State: w.State}); err != nil {
		t.Fatal(err)
	}
	list, err := p.dms.Get(p.ctx, admin, in.object(KindCollection, "readings").ID)
	if err != nil {
		t.Fatal(err)
	}
	name := "Depósito"
	if _, err = p.dms.Update(p.ctx, admin, list.ID, list.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}

	pulled, err := p.svc.Pull(p.ctx, admin, p.ws, "tank")
	if err != nil {
		t.Fatal(err)
	}
	if problems := Check(p.ctx, pulled); len(problems) > 0 {
		t.Fatalf("what was pulled has problems:\n%s", problems)
	}
	if pulled.Manifest.Collections[0].Name != "Depósito" || len(pulled.Workflows) != 1 || len(pulled.Apps) != 1 {
		t.Fatalf("pulled: %+v", pulled.Manifest.Collections[0])
	}
	pw := pulled.Workflows[0]
	if pw.Name != "Niveles" || !strings.Contains(pw.Source, "edited in PaperGo") || pw.File != "workflows/levels.json" || pw.Script != "levels.ts" {
		t.Fatalf("pulled workflow: %+v", pw)
	}
	// IDs are references again, and a setting's value its reference.
	if pw.Parameters["list"] != "$collection:readings" || pw.Parameters["reading_type"] != "$type:tank_reading" || pw.Parameters["time_zone"] != "$setting:time_zone" {
		t.Fatalf("pulled parameters: %+v", pw.Parameters)
	}
	if q := pulled.Manifest.Collections[0].Views[0].Query; q["content_type_id"] != "$type:tank_reading" {
		t.Fatalf("pulled view: %+v", q)
	}

	raw, err := Pack(pulled)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := OpenZIP(raw)
	again, problems := Load(files)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	r, err := p.svc.Apply(p.ctx, admin, p.ws, again, Options{})
	if err != nil || !r.Applied || len(r.Changes) != 0 || len(r.Refusals) != 0 {
		t.Fatalf("applying what was pulled: %v\n%s\ncustomized %v", err, said(r), r.Customized)
	}
	// What was edited is now what the solution says, so nothing is "kept" any more.
	if r2, _ := p.svc.Apply(p.ctx, admin, p.ws, again, Options{}); len(r2.Customized) != 0 {
		t.Fatalf("still reported as edited after pulling: %v", r2.Customized)
	}
}

// A solution says what it needs of the deployment: without what it cannot
// work without it is refused, and status says what it works in part without.
func TestRequiredCapabilities(t *testing.T) {
	p := newPlace(t)
	p.svc.Capabilities = map[string]bool{"ocr": true}
	needs := edit("solution.json", `"requires": {"ocr": false}`, `"requires": {"ocr": true, "ai.batch": true}`)
	has(t, p.apply(needs, Options{}), "REFUSED solution tank: needs ai.batch, which this PaperGo does not provide")

	partly := edit("solution.json", `"requires": {"ocr": false}`, `"requires": {"ocr": true, "ai.batch": false}`)
	r := p.applied(partly, Options{})
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "this PaperGo does not provide ai.batch: the solution works in part") {
		t.Fatalf("warnings: %v", r.Warnings)
	}
	status, err := p.svc.Status(p.ctx, admin, p.ws, "tank")
	if err != nil || status.OK || len(status.Problems) != 1 || status.Problems[0] != "works in part: this PaperGo does not provide ai.batch" {
		t.Fatalf("status: %+v %v", status, err)
	}
	p.svc.Capabilities["ai.batch"] = true
	if status, err = p.svc.Status(p.ctx, admin, p.ws, "tank"); err != nil || !status.OK {
		t.Fatalf("status with the capability: %+v %v", status, err)
	}
}
