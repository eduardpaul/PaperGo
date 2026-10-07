package dms

import (
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/fieldvalue"
	"papergo/ent/relationship"
	"papergo/internal/model"
	"strings"
	"sync"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func latest(t *testing.T, s *Service, id string) *ent.Resource {
	t.Helper()
	r, e := s.Get(testContext, "alice", id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func grantReader(t *testing.T, s *Service, w *ent.Resource) {
	t.Helper()
	_, e := s.SetPermissions(testContext, "alice", w.ID, latest(t, s, w.ID).Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"reader", "read", "allow"}}})
	if e != nil {
		t.Fatal(e)
	}
}
func publishItem(t *testing.T, s *Service, r *ent.Resource) {
	t.Helper()
	if _, e := s.Publish(testContext, "alice", r.ID, latest(t, s, r.ID).Version); e != nil {
		t.Fatal(e)
	}
}
func TestRichTemplatesFreezeSchemasAndPreserveExactDefaults(t *testing.T) {
	s, w, l := fixture(t)
	fields := []CreateField{
		{Key: "serial", Label: "Serial", Type: "integer", Required: true, Indexed: true, Options: model.FieldOptions{DefaultValue: json.RawMessage("9007199254740993"), Minimum: ptr("9007199254740992")}},
		{Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"12.30"`), Maximum: ptr("100.00")}},
		{Key: "labels", Label: "Labels", Type: "choice", Required: true, Indexed: true, Choices: []string{"red", "blue"}, Options: model.FieldOptions{Multiple: true}},
		{Key: "email", Label: "Email", Type: "email", Options: model.FieldOptions{MaxLength: ptr(100)}},
		{Key: "date", Label: "Date", Type: "date"},
	}
	tpl, e := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "invoice", Name: "Invoice", Fields: fields})
	if e != nil {
		t.Fatal(e)
	}
	l, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, l.Version, tpl.Version)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := s.Schemas(testContext, "alice", l.ID, 0, 100)
	if e != nil || len(rows) != 2 {
		t.Fatal("template must freeze one effective schema", len(rows), e)
	}
	r := create(t, s, l.ID, "item", "Invoice", map[string]any{"labels": []any{"red", "red", "blue"}, "email": "a@example.com", "date": "2026-10-07"})
	if r.Values["serial"] != json.Number("9007199254740993") || r.Values["amount"] != "12.30" || len(r.Values["labels"].([]any)) != 2 {
		t.Fatal("normalization/default precision", r.Values)
	}
	publishItem(t, s, r)
	oldSchema, e := s.ItemSchema(testContext, "alice", r.ID, "published")
	if e != nil {
		t.Fatal(e)
	}
	fields[0].Label = "Serial v2"
	tpl, e = s.UpdateTemplate(testContext, "alice", tpl.ID, tpl.Version, TemplateInput{Key: "invoice", Name: "Invoice v2", Fields: fields})
	if e != nil {
		t.Fatal(e)
	}
	c := latest(t, s, l.ID)
	if *c.SchemaHeadID != *l.SchemaHeadID {
		t.Fatal("editing a template changed its consumers")
	}
	if _, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, c.Version, tpl.Version-1); !errors.Is(e, ErrConflict) {
		t.Fatal("template version ignored", e)
	}
	c, e = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, c.Version, tpl.Version)
	if e != nil {
		t.Fatal(e)
	}
	visible, e := s.ItemSchema(testContext, "alice", r.ID, "published")
	if e != nil || visible.ID != oldSchema.ID {
		t.Fatal("published schema changed", e)
	}
	name := "Updated"
	if _, e = s.Update(testContext, "alice", r.ID, latest(t, s, r.ID).Version, UpdateResource{Name: &name}); e != nil {
		t.Fatal(e)
	}
	head, e := s.ItemSchema(testContext, "alice", r.ID, "head")
	if e != nil || head.ID != *c.SchemaHeadID {
		t.Fatal("new revision did not adopt effective schema", e)
	}
	for _, values := range []map[string]any{
		{"labels": []any{}}, {"labels": "red"}, {"labels": []any{"green"}}, {"labels": []any{"red"}, "email": "invalid"},
		{"labels": []any{"red"}, "date": "2026-02-30"}, {"labels": []any{"red"}, "serial": json.Number("9007199254740991")},
		{"labels": []any{"red"}, "amount": "100.01"}, {"labels": []any{"red"}, "unknown": "x"},
	} {
		if _, e = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Invalid", Values: values}); e == nil {
			t.Fatal("invalid values accepted", values)
		}
	}
	defs, e := s.Fields(testContext, "alice", l.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range defs {
		if d.Key == "labels" {
			options := d.Options
			options.Multiple = false
			if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, c.Version, UpdateField{Options: &options}); e == nil {
				t.Fatal("mutable field cardinality")
			}
		}
	}
	q, e := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "labels", Value: json.RawMessage(`"blue"`)}}})
	if e != nil || q.Total != 1 {
		t.Fatal("multi-value query", q, e)
	}
	q, e = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "labels", Op: "ne", Value: json.RawMessage(`"blue"`)}}})
	if e != nil || q.Total != 0 {
		t.Fatal("multi-value ne must match no member", q, e)
	}
}
func TestTaxonomyAndLookupPreserveIDsAndValidateNewAssignments(t *testing.T) {
	s, w, l := fixture(t)
	set, e := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{Key: "topics", Name: "Topics"})
	if e != nil {
		t.Fatal(e)
	}
	root, e := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Finance", Labels: map[string]string{"es": "Finanzas"}, Synonyms: []string{"Money"}})
	if e != nil {
		t.Fatal(e)
	}
	child, e := s.CreateTerm(testContext, "alice", set.ID, TermInput{Name: "Invoices", ParentID: &root.ID})
	if e != nil {
		t.Fatal(e)
	}
	lookup, e := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Contacts"})
	if e != nil {
		t.Fatal(e)
	}
	contact, e := s.Create(testContext, "alice", lookup.ID, CreateResource{Kind: "item", Name: "Vendor"})
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range []CreateField{
		{Key: "topics", Label: "Topics", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}},
		{Key: "contact", Label: "Contact", Type: "lookup", Options: model.FieldOptions{LookupContainerID: lookup.ID}},
	} {
		if _, e = s.CreateField(testContext, "alice", l.ID, f); e != nil {
			t.Fatal(e)
		}
	}
	r := create(t, s, l.ID, "item", "Tagged", map[string]any{"topics": []any{child.ID}, "contact": contact.ID})
	renamed, e := s.UpdateTerm(testContext, "alice", child.ID, child.Version, TermInput{Name: "Bills", Deprecated: true})
	if e != nil || renamed.ID != child.ID {
		t.Fatal("term identity changed", e)
	}
	name := "Edited"
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &name}); e != nil {
		t.Fatal("unchanged deprecated term should remain valid", e)
	}
	if _, e = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "New", Values: map[string]any{"topics": []any{child.ID}}}); e == nil {
		t.Fatal("new deprecated term assignment")
	}
	if _, e = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Wrong lookup", Values: map[string]any{"contact": r.ID}}); e == nil {
		t.Fatal("foreign lookup target")
	}
	matches, e := s.Terms(testContext, "alice", set.ID, "finanzas", "", 50)
	if e != nil || len(matches.Data) != 1 || matches.Data[0].ID != root.ID {
		t.Fatal("localized search", matches, e)
	}
	matches, e = s.Terms(testContext, "alice", set.ID, "money", "", 50)
	if e != nil || len(matches.Data) != 1 {
		t.Fatal("synonym search", matches, e)
	}
	another, e := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{Key: "other", Name: "Other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateTerm(testContext, "alice", another.ID, TermInput{Name: "Bad parent", ParentID: &root.ID}); e == nil {
		t.Fatal("cross-set parent")
	}
	if _, e = s.CreateTermSet(testContext, "reader", w.ID, TermSetInput{Key: "forbidden", Name: "Forbidden"}); !errors.Is(e, ErrForbidden) {
		t.Fatal("taxonomy management authorization", e)
	}
}
func TestQueriesViewsAndCountsUseVisibleContentBeforePagination(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	for _, f := range []CreateField{{Key: "serial", Label: "Serial", Type: "integer", Indexed: true}, {Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true}} {
		if _, e := s.CreateField(testContext, "alice", l.ID, f); e != nil {
			t.Fatal(e)
		}
	}
	a := create(t, s, l.ID, "item", "Published A", map[string]any{"serial": json.Number("9007199254740993"), "amount": "1.10"})
	b := create(t, s, l.ID, "item", "Published B", map[string]any{"serial": json.Number("9007199254740994"), "amount": "1.10"})
	c := create(t, s, l.ID, "item", "Published C", map[string]any{"serial": json.Number("9007199254740994"), "amount": "2.20"})
	missing := create(t, s, l.ID, "item", "No number", nil)
	hidden := create(t, s, l.ID, "item", "Hidden", map[string]any{"serial": json.Number("1")})
	draft := create(t, s, l.ID, "item", "Never published", map[string]any{"serial": json.Number("2")})
	for _, r := range []*ent.Resource{a, b, c, missing, hidden} {
		publishItem(t, s, r)
	}
	if _, e := s.SetPermissions(testContext, "alice", hidden.ID, latest(t, s, hidden.ID).Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}}}); e != nil {
		t.Fatal(e)
	}
	values := map[string]any{"serial": json.Number("3"), "amount": "99.99"}
	name := "Private draft"
	if _, e := s.Update(testContext, "alice", a.ID, latest(t, s, a.ID).Version, UpdateResource{Values: &values, Name: &name}); e != nil {
		t.Fatal(e)
	}
	spec := QuerySpec{Sort: SortSpec{Field: "serial"}, GroupBy: "amount", Filter: &FilterExpr{Or: []FilterExpr{{Field: "serial", Op: "gte", Value: json.RawMessage("9007199254740993")}, {Field: "serial", Op: "missing"}}}}
	query := QueryRequest{Query: spec, Limit: 1}
	ids := []string{}
	for {
		page, e := s.Query(testContext, "reader", l.ID, query)
		if e != nil {
			t.Fatal(e)
		}
		if page.Total != 4 {
			t.Fatal("unauthorized/draft count", page.Total)
		}
		for _, r := range page.Data {
			ids = append(ids, r.ID)
			if r.ID == a.ID && r.Name != "Published A" {
				t.Fatal("draft name leaked")
			}
		}
		if page.NextCursor == "" {
			break
		}
		query.After = page.NextCursor
	}
	if len(ids) != 4 || ids[0] != a.ID || ids[3] != missing.ID {
		t.Fatal("exact sort/null ordering", ids)
	}
	if ids[1] == ids[2] {
		t.Fatal("cursor duplicate")
	}
	groups, e := s.QueryGroups(testContext, "reader", l.ID, QueryRequest{Query: spec, Limit: 1})
	if e != nil || len(groups.Data) != 1 || groups.Data[0].Value != "1.10" || groups.Data[0].Count != 2 {
		t.Fatal("published grouped counts", groups, e)
	}
	next, e := s.QueryGroups(testContext, "reader", l.ID, QueryRequest{Query: spec, Limit: 1, After: groups.NextCursor})
	if e != nil || next.Data[0].Value != "2.20" || next.Data[0].Count != 1 {
		t.Fatal("group pagination", next, e)
	}
	first, e := s.Query(testContext, "reader", l.ID, QueryRequest{Query: spec, Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Query(testContext, "alice", l.ID, QueryRequest{Query: spec, After: first.NextCursor}); e == nil {
		t.Fatal("cursor reused across caller")
	}
	changed := spec
	changed.Sort.Direction = "desc"
	if _, e = s.Query(testContext, "reader", l.ID, QueryRequest{Query: changed, After: first.NextCursor}); e == nil {
		t.Fatal("cursor reused across query")
	}
	view, e := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Amounts", Columns: []string{"$name", "amount"}, Query: spec, IsDefault: true})
	if e != nil {
		t.Fatal(e)
	}
	data, e := s.QueryView(testContext, "reader", view.ID, ViewQueryRequest{})
	if e != nil || data.Total != 4 {
		t.Fatal("view query", data, e)
	}
	for _, r := range data.Data {
		if _, ok := r.Values["serial"]; ok {
			t.Fatal("unselected field returned")
		}
		if r.ID == hidden.ID || r.ID == draft.ID {
			t.Fatal("hidden item in view")
		}
	}
	other, e := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Other", Query: QuerySpec{}, IsDefault: true})
	if e != nil {
		t.Fatal(e)
	}
	_ = other
	current, e := s.View(testContext, "alice", view.ID)
	if e != nil || current.IsDefault || current.Version != view.Version+1 {
		t.Fatal("default view concurrency", current, e)
	}
	if _, e = s.UpdateView(testContext, "alice", view.ID, view.Version, ViewInput{Name: "Old"}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale view update")
	}
	if _, e = s.CreateView(testContext, "reader", l.ID, ViewInput{Name: "Forbidden"}); !errors.Is(e, ErrForbidden) {
		t.Fatal("reader managed view", e)
	}
	if _, e = s.Query(testContext, "reader", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "serial); DROP TABLE resources;--", Value: json.RawMessage("1")}}}); e == nil {
		t.Fatal("arbitrary SQL accepted")
	}
	nested := &FilterExpr{Field: "serial", Value: json.RawMessage("1")}
	for i := 0; i < 8; i++ {
		nested = &FilterExpr{Not: nested}
	}
	if _, e = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: nested}}); e == nil {
		t.Fatal("unbounded filter depth")
	}
}
func TestExclusiveScopesInheritanceCopyAndReset(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	folder := create(t, s, l.ID, "folder", "Private", nil)
	nested := create(t, s, folder.ID, "folder", "Nested", nil)
	item := create(t, s, nested.ID, "item", "Document", nil)
	publishItem(t, s, item)
	permissions, e := s.Permissions(testContext, "alice", item.ID)
	if e != nil || permissions.ScopeID != w.ID || !permissions.Inherit || len(permissions.EffectiveGrants) != 2 {
		t.Fatal("effective inherited scope", permissions, e)
	}
	if _, e = s.SetPermissions(testContext, "alice", folder.ID, folder.Version, Permissions{Inherit: true, Grants: []Permission{{"alice", "manage", "allow"}}}); e == nil {
		t.Fatal("augmented inheritance accepted")
	}
	folder, e = s.SetPermissions(testContext, "alice", folder.ID, folder.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(testContext, "reader", item.ID); !errors.Is(e, ErrForbidden) {
		t.Fatal("ancestor grant crossed an exclusive scope", e)
	}
	page, e := s.Browse(testContext, "reader", Browse{WorkspaceID: w.ID})
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range page.Data {
		if r.ID == item.ID || r.ID == folder.ID || r.ID == nested.ID {
			t.Fatal("private subtree leaked")
		}
	}
	folder, e = s.SetPermissions(testContext, "alice", folder.ID, folder.Version, Permissions{Inherit: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(testContext, "reader", item.ID); e != nil {
		t.Fatal("reset did not restore inheritance", e)
	}
	folder, e = s.SetPermissions(testContext, "alice", folder.ID, folder.Version, Permissions{CopyInherited: true})
	if e != nil {
		t.Fatal(e)
	}
	permissions, e = s.Permissions(testContext, "alice", item.ID)
	if e != nil || permissions.ScopeID != folder.ID || len(permissions.EffectiveGrants) != 2 {
		t.Fatal("copy inherited scope", permissions, e)
	}
	_, e = s.SetPermissions(testContext, "alice", w.ID, latest(t, s, w.ID).Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(testContext, "reader", item.ID); e != nil {
		t.Fatal("exclusive copy should retain copied grant", e)
	}
	inherited := create(t, s, l.ID, "folder", "Inherited", nil)
	if _, e = s.Client.Grant.Create().SetResourceID(inherited.ID).SetSubject("reader").SetAction("read").SetEffect("allow").Save(testContext); e == nil {
		t.Fatal("database accepted a grant on an inherited resource")
	}
}
func TestTypedRelationshipsConcurrencySymmetryAndCardinality(t *testing.T) {
	s, w, l := fixture(t)
	a := create(t, s, l.ID, "item", "A", nil)
	b := create(t, s, l.ID, "item", "B", nil)
	c := create(t, s, l.ID, "item", "C", nil)
	typ, e := s.CreateRelationshipType(testContext, "alice", w.ID, RelationshipTypeInput{Key: "owns", Label: "Owns", InverseLabel: "Owned by", Directed: true, MaxOutgoing: ptr(1), Attributes: []CreateField{{Key: "weight", Label: "Weight", Type: "decimal", Scale: 2, Required: true, Options: model.FieldOptions{Maximum: ptr("10.00")}}}})
	if e != nil {
		t.Fatal(e)
	}
	edge, e := s.Link(testContext, "alice", a.ID, CreateRelationship{TypeID: typ.ID, TargetID: b.ID, Metadata: map[string]any{"weight": "1.20"}})
	if e != nil {
		t.Fatal(e)
	}
	if edge.TypeID == nil || edge.Name != "owns" || edge.Metadata["weight"] != "1.20" {
		t.Fatal("typed edge normalization", edge)
	}
	if _, e = s.Link(testContext, "alice", a.ID, CreateRelationship{TypeID: typ.ID, TargetID: c.ID, Metadata: map[string]any{"weight": "1.20"}}); !errors.Is(e, ErrConflict) {
		t.Fatal("cardinality ignored", e)
	}
	if _, e = s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"weight": "11.00"}}); e == nil {
		t.Fatal("attribute bounds ignored")
	}
	updated, e := s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"weight": "2.30"}})
	if e != nil || updated.Version != 2 {
		t.Fatal("edge update", updated, e)
	}
	if _, e = s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"weight": "3.00"}}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale edge update")
	}
	if e = s.UnlinkVersion(testContext, "alice", a.ID, edge.ID, edge.Version); !errors.Is(e, ErrConflict) {
		t.Fatal("stale edge delete")
	}
	if e = s.UnlinkVersion(testContext, "alice", a.ID, edge.ID, updated.Version); e != nil {
		t.Fatal(e)
	}
	sym, e := s.CreateRelationshipType(testContext, "alice", w.ID, RelationshipTypeInput{Key: "related", Label: "Related"})
	if e != nil {
		t.Fatal(e)
	}
	edge, e = s.Link(testContext, "alice", b.ID, CreateRelationship{TypeID: sym.ID, TargetID: a.ID})
	if e != nil {
		t.Fatal(e)
	}
	if edge.Directed || edge.SourceID > edge.TargetID {
		t.Fatal("noncanonical symmetric edge")
	}
	if _, e = s.Link(testContext, "alice", a.ID, CreateRelationship{TypeID: sym.ID, TargetID: b.ID}); !errors.Is(e, ErrConflict) {
		t.Fatal("symmetric duplicate", e)
	}
	for _, id := range []string{a.ID, b.ID} {
		for _, direction := range []string{"incoming", "outgoing"} {
			p, e := s.Relationships(testContext, "alice", id, direction, "related", "", 50)
			if e != nil || len(p.Data) != 1 {
				t.Fatal("symmetric query", id, direction, p, e)
			}
		}
	}
	if _, e = s.CreateRelationshipType(testContext, "alice", w.ID, RelationshipTypeInput{Key: "bad", Label: "Bad", MaxIncoming: ptr(1)}); e == nil {
		t.Fatal("directional cardinality on symmetric policy")
	}
	// Competing links allocate the single outgoing slot atomically.
	single, e := s.CreateRelationshipType(testContext, "alice", w.ID, RelationshipTypeInput{Key: "single", Label: "Single", Directed: true, MaxOutgoing: ptr(1)})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{b.ID, c.ID} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			_, e := s.Link(testContext, "alice", a.ID, CreateRelationship{TypeID: single.ID, TargetID: target})
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("concurrent cardinality", success)
	}
	n, e := s.Client.Relationship.Query().Where(relationship.TypeIDEQ(single.ID)).Count(testContext)
	if e != nil || n != 1 {
		t.Fatal("cardinality persisted", n, e)
	}
}
func TestQueriesCanMatchHistoricalChoicesAfterValidationChanges(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	d, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"old", "new"}, Indexed: true})
	if e != nil {
		t.Fatal(e)
	}
	r := create(t, s, l.ID, "item", "Released", map[string]any{"status": "old"})
	publishItem(t, s, r)
	choices := []string{"new"}
	if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Choices: &choices}); e != nil {
		t.Fatal(e)
	}
	result, e := s.Query(testContext, "reader", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "status", Value: json.RawMessage(`"old"`)}}})
	if e != nil || result.Total != 1 {
		t.Fatal("historical choice became unqueryable", result, e)
	}
}
func TestExpandedFieldIndexesSurviveIndexRebuild(t *testing.T) {
	s, _, l := fixture(t)
	d, e := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "labels", Label: "Labels", Type: "text", Indexed: true, Options: model.FieldOptions{Multiple: true}})
	if e != nil {
		t.Fatal(e)
	}
	r := create(t, s, l.ID, "item", "Multi", map[string]any{"labels": []any{"one", "two"}})
	publishItem(t, s, r)
	n, e := s.Client.FieldValue.Query().Where(fieldvalue.ItemIDEQ(r.ID)).Count(testContext)
	if e != nil || n != 4 {
		t.Fatal("multi-value index rows", n, e)
	}
	for _, flag := range []bool{false, true} {
		if _, e = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Indexed: &flag}); e != nil {
			t.Fatal(e)
		}
		n, e = s.Client.FieldValue.Query().Where(fieldvalue.ItemIDEQ(r.ID)).Count(testContext)
		if e != nil {
			t.Fatal(e)
		}
		expected := 0
		if flag {
			expected = 4
		}
		if n != expected {
			t.Fatal("rebuild", flag, n)
		}
	}
	// A malformed definition should fail before any persisted schema changes.
	before := latest(t, s, l.ID)
	for _, f := range []CreateField{
		{Key: "bad", Label: "Bad", Type: "integer", Options: model.FieldOptions{Minimum: ptr("1.5")}},
		{Key: "bad", Label: "Bad", Type: "decimal", Scale: 2, Options: model.FieldOptions{Minimum: ptr("10.00"), Maximum: ptr("1.00")}},
		{Key: "bad", Label: "Bad", Type: "text", Options: model.FieldOptions{DefaultValue: json.RawMessage(`"` + strings.Repeat("x", 10) + `"`), MaxLength: ptr(5)}},
	} {
		if _, e = s.CreateField(testContext, "alice", l.ID, f); e == nil {
			t.Fatal("invalid definition accepted")
		}
	}
	after := latest(t, s, l.ID)
	if after.Version != before.Version {
		t.Fatal("invalid definition changed schema")
	}
	if _, e = s.Client.FieldDefinition.UpdateOne(d).SetOptions(model.FieldOptions{}).Save(testContext); e == nil {
		t.Fatal("database changed multiple field identity")
	}
}
