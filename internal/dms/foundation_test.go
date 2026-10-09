package dms

import (
	"encoding/json"
	"errors"
	"fmt"
	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/internal/testutil"
	"strings"
	"testing"
)

func readers(t *testing.T, s *Service, w *ent.Resource) {
	t.Helper()
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"reader", "read", "allow"}, {"editor", "write", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
}
func current(t *testing.T, s *Service, id string) *ent.Resource {
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
	tags := []string{"secret"}
	item, err = s.Update(testContext, "editor", item.ID, item.Version, UpdateResource{Name: &name, Tags: &tags})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := s.Get(testContext, "reader", item.ID)
	if err != nil || visible.Name != "Hiddenword" || len(visible.Tags) != 0 || visible.HeadRevisionID != nil {
		t.Fatalf("published surface leaked: %+v %v", visible, err)
	}
	for _, in := range []Browse{{ParentID: list.ID, Search: "Secretedit"}, {ParentID: list.ID, Tag: "secret"}} {
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
	if err != nil || len(events) != 1 || events[0].ID != pub.ID {
		t.Fatalf("events: %v %v", events, err)
	}
	if _, err = s.Revisions(testContext, "reader", item.ID, 0, 100); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader saw revisions", err)
	}
	item, err = s.Unpublish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "reader", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("unpublish ineffective", err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version-1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale publish accepted", err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	visible, err = s.Get(testContext, "reader", item.ID)
	if err != nil || visible.Name != name {
		t.Fatal("republish failed", err)
	}
	revs, err := s.Revisions(testContext, "alice", item.ID, 0, 100)
	if err != nil || len(revs) != 2 || revs[0].Name != "Hiddenword" {
		t.Fatal("revision history changed", err)
	}
	// Lifecycle transitions consume lock versions but do not manufacture revisions.
	if current(t, s, item.ID).Version != 5 {
		t.Fatal("incorrect lifecycle version")
	}
}

func TestAutomaticPublishingAndPolicyChange(t *testing.T) {
	db := testutil.Database(t)
	s := NewService(db.Client)
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
	revs, err := s.Revisions(testContext, "alice", item.ID, 0, 100)
	if err != nil || len(revs) != 2 {
		t.Fatal(err)
	}
	defs, err := s.Client.SchemaRevision.Get(testContext, revs[0].SchemaRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(defs.Definition), "Total") || !strings.Contains(string(defs.Definition), "Amount") {
		t.Fatal("schema history changed")
	}
	paired, err := s.ItemSchema(testContext, "reader", item.ID, "auto")
	if err != nil || paired.ID != revs[0].SchemaRevisionID || strings.Contains(string(paired.Definition), "Total") {
		t.Fatal("reader schema not paired with published revision", err)
	}
	if revs[0].Edges.SchemaRevision == nil || revs[0].Edges.SchemaRevision.ID != paired.ID {
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
	attach := func(key string) *ent.Blob {
		t.Helper()
		item = current(t, s, item.ID)
		b, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, BlobInput{ObjectKey: key, Filename: "file.txt", ContentType: "text/plain", Size: 1, SHA256: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
		return b
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
		link, err := s.Link(testContext, "alice", source.ID, CreateRelationship{TypeID: typ.ID, TargetID: r.ID})
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

func TestRevisionOwnershipAndTransactionRollback(t *testing.T) {
	db := testutil.Database(t)
	s := NewService(db.Client)
	w := create(t, s, "", "workspace", "Org", nil)
	list := create(t, s, w.ID, "list", "Records", nil)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	for _, q := range []string{"UPDATE resources SET head_revision_id=? WHERE id=?", "UPDATE resources SET published_revision_id=? WHERE id=?"} {
		if _, err := db.SQL.Exec(q, *b.HeadRevisionID, a.ID); err == nil {
			t.Fatal("cross item pointer accepted")
		}
	}
	if _, err := db.SQL.Exec("UPDATE item_revisions SET name='Changed' WHERE id=?", *a.HeadRevisionID); err == nil {
		t.Fatal("revision mutation accepted")
	}
	if _, err := db.SQL.Exec("DELETE FROM schema_revisions WHERE container_id=?", list.ID); err == nil {
		t.Fatal("schema deletion accepted")
	}
	bad := map[string]any{"unknown": true}
	if _, err := s.Update(testContext, "alice", a.ID, a.Version, UpdateResource{Values: &bad}); err == nil {
		t.Fatal("invalid edit accepted")
	}
	count, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(a.ID)).Count(testContext)
	if err != nil || count != 1 {
		t.Fatal("failed edit left revision", err)
	}
	count, err = s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(a.ID)).Count(testContext)
	if err != nil || count != 1 {
		t.Fatal("failed edit left projection", err)
	}
	rows, err := db.SQL.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
}

// counted reads an optional query total; -1 means the response had none.
func counted(total *int) int {
	if total == nil {
		return -1
	}
	return *total
}
