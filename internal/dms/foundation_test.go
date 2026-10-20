package dms

import (
	"encoding/json"
	"errors"
	"fmt"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"strings"
	"testing"
)

func readers(t *testing.T, s *Service, w *resources.Resource) {
	t.Helper()
	err := setWorkspaceMembers(testContext, s, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}, WorkspaceMember{Subject: "editor", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
}
func current(t *testing.T, s *Service, id string) *resources.Resource {
	t.Helper()
	r, err := s.Get(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPublishingSurfacesAndLifecycle(t *testing.T) {
	s, w, list := fixture(t)
	readers(t, s, w)
	item := create(t, s, list.ID, "item", "Hiddenword", nil)
	if _, err := s.Get(testContext, "reader", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft visible: %v", err)
	}
	if _, err := s.GetSurface(testContext, "reader", item.ID, "head"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("draft access: %v", err)
	}
	p, err := s.Browse(testContext, "reader", Browse{ParentID: list.ID, Search: "Hiddenword"})
	if err != nil || len(p.Data) != 0 {
		t.Fatalf("draft search: %+v %v", p, err)
	}
	pub, err := s.Publish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	item = current(t, s, item.ID)
	name := "Secretedit"
	keywords := []string{"secret"}
	item, err = s.Update(testContext, "editor", item.ID, item.Version, UpdateResource{Name: &name, Keywords: &keywords})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.Get(testContext, "reader", item.ID)
	if err != nil || visible.Name != "Hiddenword" || len(visible.Keywords) != 0 || visible.HeadRevisionID != nil {
		t.Fatalf("published surface leaked: %+v %v", visible, err)
	}
	for _, in := range []Browse{{ParentID: list.ID, Search: "Secretedit"}, {ParentID: list.ID, Keyword: "secret"}} {
		p, err = s.Browse(testContext, "reader", in)
		if err != nil || len(p.Data) != 0 {
			t.Fatalf("draft query leaked: %+v %v", p, err)
		}
	}
	p, err = s.Browse(testContext, "reader", Browse{ParentID: list.ID, Search: "Hiddenword"})
	if err != nil || len(p.Data) != 1 || p.Data[0].Name != "Hiddenword" {
		t.Fatalf("published search lost: %+v %v", p, err)
	}
	events, err := s.Publications(testContext, "reader", item.ID, 0, 100)
	if err != nil || len(events.Data) != 1 || events.Data[0].ID != pub.ID {
		t.Fatalf("events: %v %v", events, err)
	}
	if history, e := s.RevisionHistory(testContext, "reader", item.ID, 0, 100); e != nil || len(history.Data) != 2 || history.Data[1].Name != "Hiddenword" || history.Data[0].Name != name || history.Data[1].SchemaRevision == nil {
		t.Fatal("ordinary reader lost metadata history", history, e)
	}
	item, err = s.Unpublish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "reader", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("unpublish ineffective", err)
	}
	if history, e := s.RevisionHistory(testContext, "reader", item.ID, 0, 100); e != nil || len(history.Data) != 2 {
		t.Fatal("unpublishing changed metadata history access", history, e)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version-1); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale publish accepted", err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	visible, err = s.Get(testContext, "reader", item.ID)
	if err != nil || visible.Name != name {
		t.Fatal("republish failed", err)
	}
	revs, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 100)
	if err != nil || len(revs.Data) != 2 || revs.Data[1].Name != "Hiddenword" {
		t.Fatal("revision history changed", err)
	}
	// Lifecycle transitions consume lock versions but do not manufacture revisions.
	if current(t, s, item.ID).Version != 5 {
		t.Fatal("incorrect lifecycle version")
	}
}

func TestAutomaticPublishingAndPolicyChange(t *testing.T) {
	db := daltest.Database(t)
	s := NewService(db.SQL)
	w := create(t, s, "", "workspace", "Organization", nil)
	readers(t, s, w)
	list, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Auto"})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "First", nil)
	if item.HeadRevisionID == nil || item.PublishedRevisionID == nil || *item.HeadRevisionID != *item.PublishedRevisionID {
		t.Fatal("item not automatically published")
	}
	name := "Second"
	item, err = s.Update(testContext, "editor", item.ID, item.Version, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(testContext, "reader", item.ID)
	if err != nil || r.Name != name {
		t.Fatal("automatic edit not visible", err)
	}
	if _, err = s.Unpublish(testContext, "alice", item.ID, item.Version); err == nil {
		t.Fatal("automatic item unpublished")
	}
	enabled := true
	list, err = s.Update(testContext, "alice", list.ID, list.Version, UpdateResource{PublishingEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	hidden := create(t, s, list.ID, "item", "New draft", nil)
	if _, err = s.Get(testContext, "reader", hidden.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("enabled policy ignored", err)
	}
	enabled = false
	if _, err = s.Update(testContext, "alice", list.ID, list.Version, UpdateResource{PublishingEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "reader", hidden.ID); err != nil {
		t.Fatal("policy did not publish existing head", err)
	}
	if current(t, s, hidden.ID).Version != 2 {
		t.Fatal("policy publication did not advance item lock")
	}
}

func TestExactFieldsFrozenSchemasAndIndexRebuild(t *testing.T) {
	s, w, list := fixture(t)
	readers(t, s, w)
	_, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "serial", Label: "Serial", Type: "integer", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	amount, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "amount", Label: "Amount", Type: "decimal", Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "Invoice", map[string]any{"serial": json.Number("9007199254740993"), "amount": json.Number("123.4")})
	if item.Values["serial"] != json.Number("9007199254740993") || item.Values["amount"] != "123.40" {
		t.Fatalf("precision lost on create: %+v", item.Values)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item = current(t, s, item.ID)
	vals := map[string]any{"serial": json.Number("9007199254740994"), "amount": "500.00"}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &vals})
	if err != nil {
		t.Fatal(err)
	}
	filter := Browse{ParentID: list.ID, FilterField: "serial", FilterOp: "eq", FilterValue: "9007199254740993"}
	p, err := s.Browse(testContext, "reader", filter)
	if err != nil || len(p.Data) != 1 || p.Data[0].Values["serial"] != json.Number(filter.FilterValue) {
		t.Fatalf("published exact filter: %+v %v", p, err)
	}
	p, err = s.Browse(testContext, "editor", filter)
	if err != nil || len(p.Data) != 0 {
		t.Fatalf("head exact filter: %+v %v", p, err)
	}
	filter.FilterField = "amount"
	filter.FilterOp = "gte"
	filter.FilterValue = "120.00"
	if _, err = s.Browse(testContext, "reader", filter); err == nil {
		t.Fatal("unindexed query accepted")
	}
	enabled := true
	label := "Total"
	list = current(t, s, list.ID)
	if _, err = s.UpdateField(testContext, "alice", list.ID, amount.ID, list.Version, UpdateField{Label: &label, Indexed: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Browse(testContext, "reader", filter); !isValidation(err) || !strings.Contains(err.Error(), "building") {
		t.Fatal("query used an index still building", err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	p, err = s.Browse(testContext, "reader", filter)
	if err != nil || len(p.Data) != 1 {
		t.Fatalf("index backfill: %+v %v", p, err)
	}
	filter.FilterValue = "400.00"
	p, err = s.Browse(testContext, "reader", filter)
	if err != nil || len(p.Data) != 0 {
		t.Fatal("draft decimal index leaked", err)
	}
	p, err = s.Browse(testContext, "editor", filter)
	if err != nil || len(p.Data) != 1 {
		t.Fatal("head decimal index missing", err)
	}
	revs, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 100)
	if err != nil || len(revs.Data) != 2 {
		t.Fatal(err)
	}
	defs, err := s.DAL.Catalog().Schema(testContext, revs.Data[1].SchemaRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(defs.Definition), "Total") || !strings.Contains(string(defs.Definition), "Amount") {
		t.Fatal("schema history changed")
	}
	paired, err := s.ItemSchema(testContext, "reader", item.ID, "auto")
	if err != nil || paired.ID != revs.Data[1].SchemaRevisionID || strings.Contains(string(paired.Definition), "Total") {
		t.Fatal("reader schema not paired with published revision", err)
	}
	if revs.Data[1].SchemaRevision == nil || revs.Data[1].SchemaRevision.ID != paired.ID {
		t.Fatal("history lacks its immutable schema")
	}
	for _, v := range []any{"1.234", "92233720368547758.08", float64(1.2)} {
		if _, err = s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Bad", Values: map[string]any{"amount": v}}); err == nil {
			t.Fatal("invalid decimal accepted", v)
		}
	}
	enabled = false
	list = current(t, s, list.ID)
	if _, err = s.UpdateField(testContext, "alice", list.ID, amount.ID, list.Version, UpdateField{Indexed: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Browse(testContext, "reader", filter); err == nil {
		t.Fatal("disabled index still queryable")
	}
}

func TestBlobSelectionFollowsPublishedRevision(t *testing.T) {
	s, w, _ := fixture(t)
	readers(t, s, w)
	lib := create(t, s, w.ID, "library", "Documents", nil)
	item := create(t, s, lib.ID, "item", "Contract", nil)
	attach := func(key string) *blobs.Blob {
		t.Helper()
		item = current(t, s, item.ID)
		b, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, BlobInput{ObjectKey: key, Filename: key + ".pdf", ContentType: "application/pdf", Size: int64(len(key)), SHA256: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		return b.Blob
	}
	old := attach("old")
	item = current(t, s, item.ID)
	if _, err := s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	draft := attach("draft")
	b, err := s.GetBlob(testContext, "reader", item.ID, "")
	if err != nil || b.ID != old.ID {
		t.Fatal("reader got draft blob", err)
	}
	if _, err = s.GetBlob(testContext, "reader", item.ID, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("explicit draft blob leaked", err)
	}
	for _, subject := range []string{"reader", "editor"} {
		schema, err := s.ItemSchema(testContext, subject, item.ID, "auto")
		want := old
		if subject == "editor" {
			want = draft
		}
		if err != nil || schema.File == nil || schema.File.Filename != want.Filename || schema.File.Size != want.Size {
			t.Fatal("schema system properties do not match visible file", subject, schema, err)
		}
	}
	if _, err = s.ItemSchema(testContext, "reader", item.ID, "head"); !errors.Is(err, ErrForbidden) {
		t.Fatal("schema inspector exposed draft metadata", err)
	}
	b, err = s.GetBlob(testContext, "editor", item.ID, "")
	if err != nil || b.ID != draft.ID {
		t.Fatal("editor lost head blob", err)
	}
}

func TestAuthorizedPaginationAndRelationships(t *testing.T) {
	s, w, list := fixture(t)
	readers(t, s, w)
	source := create(t, s, list.ID, "item", "Source", nil)
	if _, err := s.Publish(testContext, "alice", source.ID, source.Version); err != nil {
		t.Fatal(err)
	}
	visible := map[string]bool{source.ID: true}
	links := map[string]bool{}
	typ := referencesType(t, s, w.ID)
	for i := 0; i < 16; i++ {
		r := create(t, s, list.ID, "item", fmt.Sprintf("Item %02d", i), nil)
		if i%3 == 0 {
			if _, err := s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
			visible[r.ID] = true
		}
		link, err := s.Link(testContext, "alice", source.ID, CreateRelationship{Type: typ.ID, TargetID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		if visible[r.ID] {
			links[link.ID] = true
		}
	}
	seen := map[string]bool{}
	after := ""
	for {
		p, err := s.Browse(testContext, "reader", Browse{ParentID: list.ID, Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		if p.NextCursor != "" && len(p.Data) != 2 {
			t.Fatal("short authorized page")
		}
		for _, r := range p.Data {
			if !visible[r.ID] || seen[r.ID] {
				t.Fatal("hidden or repeated resource")
			}
			seen[r.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		after = p.NextCursor
	}
	if len(seen) != len(visible) {
		t.Fatal("pagination lost visible resources")
	}
	seen = map[string]bool{}
	after = ""
	for {
		p, err := s.Relationships(testContext, "reader", source.ID, "outgoing", "references", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if p.NextCursor != "" && len(p.Data) != 2 {
			t.Fatal("short relationship page")
		}
		for _, l := range p.Data {
			if !links[l.ID] || seen[l.ID] {
				t.Fatal("relationship leaked or repeated")
			}
			seen[l.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		after = p.NextCursor
	}
	if len(seen) != len(links) {
		t.Fatal("pagination lost visible relationships")
	}
}

func TestInvalidEditRollsBackRevisionAndSurface(t *testing.T) {
	s, _, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	bad := map[string]any{"unknown": true}
	if _, err := s.Update(testContext, "alice", a.ID, a.Version, UpdateResource{Values: &bad}); err == nil {
		t.Fatal("invalid edit accepted")
	}
	count, err := countRevisions(s, a.ID)
	if err != nil || count != 1 {
		t.Fatal("failed edit left revision", err)
	}
	count, err = countSurfaces(s, surfaces.Filter{ItemID: a.ID})
	if err != nil || count != 1 {
		t.Fatal("failed edit left projection", err)
	}
}

// counted reads an optional query total; -1 means the response had none.
func counted(total *int) int {
	if total == nil {
		return -1
	}
	return *total
}

// testTermGroup returns the id of a "Test" organization term group.
func testTermGroup(s *Service) string {
	page, err := s.TermGroups(testContext, "alice", "", 100)
	if err != nil {
		panic(err)
	}
	for _, g := range page.Data {
		if g.Name == "Test" {
			return g.ID
		}
	}
	g, err := s.CreateTermGroup(testContext, "alice", TermGroupInput{Name: "Test"})
	if err != nil {
		panic(err)
	}
	return g.ID
}
