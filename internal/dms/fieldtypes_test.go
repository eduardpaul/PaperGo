package dms

import (
	"encoding/json"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/internal/model"
	"testing"
)

func TestApproximateNumericBoundsDoNotExpandScientificExponents(t *testing.T) {
	d := &ent.FieldDefinition{Key: "estimate", Label: "Estimate", Type: fielddefinition.TypeNumber, Options: model.FieldOptions{Minimum: ptr("1e-999999999"), Maximum: ptr("1")}}
	if e := validateFieldDefinition(d); e != nil {
		t.Fatal(e)
	}
	if e := normalizeValues([]*ent.FieldDefinition{d}, map[string]any{"estimate": json.Number("0.5")}); e != nil {
		t.Fatal(e)
	}
	if e := normalizeValues([]*ent.FieldDefinition{d}, map[string]any{"estimate": json.Number("1.5")}); e == nil {
		t.Fatal("maximum ignored")
	}
	d.Options.Minimum = ptr("2")
	if e := validateFieldDefinition(d); e == nil {
		t.Fatal("reversed bounds accepted")
	}
}
func TestTemplateAdoptionRevalidatesReferenceDefaults(t *testing.T) {
	s, w, l := fixture(t)
	set, e := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: testTermGroup(s, w.ID), Key: "topics", Name: "Topics"})
	if e != nil {
		t.Fatal(e)
	}
	term, e := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Active"})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(term.ID)
	tpl, e := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "topic", Name: "Topic", Fields: []CreateField{{Key: "topic", Label: "Topic", Type: "term", Options: model.FieldOptions{TermSetID: set.ID, DefaultValue: raw}}}})
	if e != nil {
		t.Fatal(e)
	}
	l, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, l.Version, tpl.Version, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateTerm(testContext, "alice", term.ID, term.Version, TermInput{Name: "Retired", Deprecated: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, l.Version, tpl.Version, ""); e == nil {
		t.Fatal("deprecated template default was adopted")
	}
	current := latest(t, s, l.ID)
	if current.Version != l.Version || *current.SchemaHeadID != *l.SchemaHeadID {
		t.Fatal("failed adoption mutated schema")
	}
}
