package relationships_test

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"slices"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/taxonomy"
)

func TestRelationships_SQLite(t *testing.T)   { testRelationships(t, daltest.SQLiteStore(t)) }
func TestRelationships_Postgres(t *testing.T) { testRelationships(t, daltest.PostgresStore(t)) }

func testRelationships(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	for _, id := range []string{"b", "c"} {
		if _, err := s.Resources().Create(ctx, resources.New{ID: id, WorkspaceID: f.Workspace, ParentID: f.Source, ContainerID: f.Source, ContentTypeID: "type", Kind: refs.KindItem, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	r := s.Relationships()
	predicates := daltest.TermSet(t, s, taxonomy.RelationshipTypesSetKey)
	cites, err := s.Taxonomy().AddTerm(ctx, taxonomy.Term{SetID: predicates.ID, Name: "Cites"})
	if err != nil {
		t.Fatal(err)
	}
	topics := daltest.TermSet(t, s, "topics")
	topic, err := s.Taxonomy().AddTerm(ctx, taxonomy.Term{SetID: topics.ID, Name: "Topic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateType(ctx, relationships.Type{ID: topic.ID}); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("type outside the predicates set: %v", err)
	}
	one := 1
	attributes := json.RawMessage(`{"fields": [{"key": "page", "type": "integer"}]}`)
	typ, err := r.CreateType(ctx, relationships.Type{ID: cites.ID, Directed: true, MaxOutgoing: &one, Attributes: attributes})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := r.Type(ctx, cites.ID)
	if err != nil || !stored.Same(typ) || *stored.MaxOutgoing != 1 || stored.MaxIncoming != nil {
		t.Fatalf("type: %+v %v", stored, err)
	}

	// Undirected links keep the lower id as their source, and relinking
	// returns the existing link.
	untyped, created, err := r.Link(ctx, relationships.Relationship{SourceID: "c", TargetID: "b", Metadata: map[string]any{"note": "x"}})
	if err != nil || !created || untyped.SourceID != "b" || untyped.TargetID != "c" {
		t.Fatalf("link: %+v %v %v", untyped, created, err)
	}
	if again, created, err := r.Link(ctx, relationships.Relationship{SourceID: "b", TargetID: "c"}); err != nil || created || again.ID != untyped.ID {
		t.Fatalf("relink: %+v %v %v", again, created, err)
	}
	if _, _, err = r.Link(ctx, relationships.Relationship{SourceID: "b", TargetID: "b"}); !errors.Is(err, dberr.ErrCheckViolation) {
		t.Fatalf("self link: %v", err)
	}
	if _, _, err = r.Link(ctx, relationships.Relationship{SourceID: "b", TargetID: f.Folder}); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("link to a folder: %v", err)
	}
	if _, _, err = r.Link(ctx, relationships.Relationship{SourceID: "b", TargetID: "c", TypeID: typ.ID}); !errors.Is(err, dberr.ErrCheckViolation) {
		t.Fatalf("undirected link of a directed type: %v", err)
	}
	typed, _, err := r.Link(ctx, relationships.Relationship{SourceID: "c", TargetID: "b", TypeID: typ.ID, Directed: true})
	if err != nil || typed.SourceID != "c" {
		t.Fatalf("typed link: %+v %v", typed, err)
	}
	if _, _, err = r.Link(ctx, relationships.Relationship{SourceID: "c", TargetID: f.Item, TypeID: typ.ID, Directed: true}); !errors.Is(err, dberr.ErrCardinalityExceeded) {
		t.Fatalf("second outgoing link: %v", err)
	}

	ids := func(f relationships.Filter) []string {
		t.Helper()
		rows, err := r.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, row := range rows {
			out = append(out, row.ID)
		}
		slices.Sort(out)
		return out
	}
	both := []string{typed.ID, untyped.ID}
	slices.Sort(both)
	directed := true
	for name, c := range map[string]struct {
		f    relationships.Filter
		want []string
	}{
		"endpoint":  {relationships.Filter{Endpoint: "b"}, both},
		"workspace": {relationships.Filter{Workspace: f.Workspace}, both},
		"ids":       {relationships.Filter{IDs: []string{typed.ID}}, []string{typed.ID}},
		"typed":     {relationships.Filter{TypeIDs: []string{typ.ID}}, []string{typed.ID}},
		"untyped":   {relationships.Filter{Untyped: true}, []string{untyped.ID}},
		"directed":  {relationships.Filter{Directed: &directed}, []string{typed.ID}},
		"none":      {relationships.Filter{Workspace: "elsewhere"}, []string{}},
	} {
		if got := ids(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	saved, err := r.Save(ctx, relationships.Relationship{ID: typed.ID, TypeID: typ.ID, Metadata: map[string]any{"page": float64(3)}}, typed.Version)
	if err != nil || saved.Version != 2 || saved.Metadata["page"] != float64(3) {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if _, err = r.Save(ctx, saved, typed.Version); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale save: %v", err)
	}
	if err = r.DeleteType(ctx, typ.ID); err == nil {
		t.Fatal("deleted a type links use")
	}
	if err = r.Unlink(ctx, typed.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Count(ctx, relationships.Filter{TypeIDs: []string{typ.ID}}); err != nil || n != 0 {
		t.Fatalf("unlinked: %d %v", n, err)
	}
	if err = r.DeleteType(ctx, typ.ID); err != nil {
		t.Fatal(err)
	}
}
