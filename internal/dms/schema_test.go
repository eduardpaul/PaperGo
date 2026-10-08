package dms

import (
	"encoding/json"
	"papergo/internal/model"
	"testing"
)

func TestSchemaTighteningKeepsExistingItemsEditable(t *testing.T) {
	s, _, l := fixture(t)
	d, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	r := create(t, s, l.ID, "item", "Legacy", map[string]any{"status": "b"})
	choices := []string{"a"}
	if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Choices: &choices}); e != nil {
		t.Fatal(e)
	}
	name := "Renamed"
	r, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &name})
	if e != nil {
		t.Fatal("unchanged historical value blocked an unrelated edit", e)
	}
	if r.Values["status"] != "b" {
		t.Fatal("historical value changed", r.Values)
	}
	values := map[string]any{"status": "c"}
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &values}); e == nil {
		t.Fatal("new value bypassed the current choices")
	}
}

func TestMakingFieldRequiredNeedsValuesOrDefault(t *testing.T) {
	s, _, l := fixture(t)
	d, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "owner", Label: "Owner", Type: "text"})
	if e != nil {
		t.Fatal(e)
	}
	r := create(t, s, l.ID, "item", "Unowned", nil)
	required := true
	if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Required: &required}); e == nil {
		t.Fatal("required without default on items lacking a value")
	}
	values := map[string]any{"owner": "alice"}
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &values}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Required: &required}); e != nil {
		t.Fatal("every item holds a value", e)
	}
}

func TestApplyTemplateCannotRequireMissingValues(t *testing.T) {
	s, w, l := fixture(t)
	if _, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "status", Label: "Status", Type: "text"}); e != nil {
		t.Fatal(e)
	}
	create(t, s, l.ID, "item", "Empty", nil)
	tpl, e := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "strict", Name: "Strict", Fields: []CreateField{{Key: "status", Label: "Status", Type: "text", Required: true}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, latest(t, s, l.ID).Version, tpl.Version, ""); e == nil {
		t.Fatal("template made a field required over missing values")
	}
	tpl, e = s.UpdateTemplate(testContext, "alice", tpl.ID, tpl.Version, TemplateInput{Name: "Strict", Fields: []CreateField{{Key: "status", Label: "Status", Type: "text", Required: true, Options: fieldDefault(t, "open")}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, latest(t, s, l.ID).Version, tpl.Version, ""); e != nil {
		t.Fatal("required with default", e)
	}
}

func TestTermSearchFoldsNonASCII(t *testing.T) {
	s, w, _ := fixture(t)
	set, e := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{Key: "moods", Name: "Moods"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Élan", Synonyms: []string{"ÜBERMUT"}}); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"Élan", "élan", "übermut"} {
		matches, e := s.Terms(testContext, "alice", set.ID, q, "", 50)
		if e != nil || len(matches.Data) != 1 {
			t.Fatal("non-ASCII search", q, matches, e)
		}
	}
}

func fieldDefault(t *testing.T, v any) (o model.FieldOptions) {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	o.DefaultValue = raw
	return
}
