package daltest

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/model"
	"testing"
)

// Val reads an optional identifier.
func Val(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Exists reports whether a resource is stored, deleted or not.
func Exists(ctx context.Context, s *dal.Store, id string) (bool, error) {
	return s.Resources().Exists(ctx, resources.Filter{IDs: []string{id}})
}

// Move places a resource below another parent of its collection.
func Move(ctx context.Context, s *dal.Store, id, parentID string) error {
	r, err := s.Resources().Resource(ctx, id)
	if err != nil {
		return err
	}
	return s.Resources().Change(ctx, id, r.Version, "test", resources.Changes{ParentID: &parentID})
}

// Remove moves a resource and its live descendants to the recycle bin.
func Remove(ctx context.Context, s *dal.Store, id, actor string) error {
	_, err := s.Resources().Delete(ctx, id, actor)
	return err
}

// Restore brings a deleted resource back below its parent.
func Restore(ctx context.Context, s *dal.Store, id, nameKey, actor string) error {
	r, err := s.Resources().Resource(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.Resources().Restore(ctx, id, Val(r.ParentID), nameKey, actor)
	return err
}

// Rename changes a resource's name.
func Rename(ctx context.Context, s *dal.Store, id, name, actor string) error {
	r, err := s.Resources().Resource(ctx, id)
	if err != nil {
		return err
	}
	return s.Resources().Change(ctx, id, r.Version, actor, resources.Changes{Name: &name})
}

// Tag replaces a resource's keywords.
func Tag(ctx context.Context, s *dal.Store, id string, keywords []string) error {
	r, err := s.Resources().Resource(ctx, id)
	if err != nil {
		return err
	}
	return s.Resources().Change(ctx, id, r.Version, "test", resources.Changes{Keywords: &keywords})
}

// Revise records an item's next revision in its collection with its
// content type.
func Revise(ctx context.Context, s *dal.Store, rev revisions.Revision) (revisions.Revision, error) {
	r, err := s.Resources().Resource(ctx, rev.ItemID)
	if err != nil {
		return revisions.Revision{}, err
	}
	rev.ContainerID, rev.ContentTypeID = Val(r.ContainerID), Val(r.ContentTypeID)
	values := rev.Payload
	if len(values) == 0 {
		values = json.RawMessage(`{}`)
	}
	return s.Resources().Revise(ctx, rev, values, rev.CreatedBy)
}

// Fixture is a workspace with two libraries of one content type, a folder in
// the source library and an item in it with a revision shown on both
// surfaces.
type Fixture struct {
	Store                                                          *dal.Store
	Workspace, Source, Destination, Folder, Item, Schema, Revision string
}

func NewFixture(t testing.TB, store *dal.Store) Fixture {
	t.Helper()
	ctx := context.Background()
	f := Fixture{Store: store}
	ws, err := store.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	f.Workspace = ws.ID
	for _, id := range []string{"source", "destination"} {
		_, err = store.Resources().Create(ctx, resources.New{ID: id, WorkspaceID: ws.ID, ParentID: ws.ID, Kind: refs.KindLibrary, Name: id})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.Source, f.Destination = "source", "destination"
	ct, err := store.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "type", Key: "document", Name: "Document"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.Source, f.Destination} {
		if _, err := store.Catalog().Attach(ctx, id, ct.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	folder, err := store.Resources().Create(ctx, resources.New{ID: "folder", WorkspaceID: ws.ID, ParentID: f.Source, ContainerID: f.Source, ContentTypeID: ct.ID, Kind: refs.KindFolder, Name: "Folder"})
	if err != nil {
		t.Fatal(err)
	}
	f.Folder = folder.ID
	item, err := store.Resources().Create(ctx, resources.New{ID: "item", WorkspaceID: ws.ID, ParentID: folder.ID, ContainerID: f.Source, ContentTypeID: ct.ID, Kind: refs.KindItem, Name: "Item", NameKey: "item"})
	if err != nil {
		t.Fatal(err)
	}
	f.Item = item.ID
	schema, err := store.Catalog().RecordSchema(ctx, f.Source, "alice", []byte(`{"content_types":[{"id":"type"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	f.Schema = schema.ID
	revision, err := Revise(ctx, store, revisions.Revision{ItemID: item.ID, SchemaRevisionID: schema.ID, Name: "Item", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	f.Revision = revision.ID
	Project(t, f)
	return f
}

// Project shows the fixture's revision on both surfaces of its item, with
// an open stage.
func Project(t testing.TB, f Fixture) {
	t.Helper()
	for _, surface := range []refs.Surface{refs.SurfaceHead, refs.SurfacePublished} {
		text := "open"
		_, err := f.Store.Surfaces().Project(context.Background(), surfaces.Projection{ItemID: f.Item, Surface: surface, RevisionID: f.Revision, Fields: []surfaces.FieldValue{{FieldKey: "stage", FieldType: catalog.FieldChoice, ValueText: &text}}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// AssertCounts checks a collection's counters of the fixture's choice field:
// each surface's total and its value, every one counting the item once.
func AssertCounts(t testing.TB, f Fixture, container string, want int) {
	t.Helper()
	got := 0
	for _, surface := range []refs.Surface{refs.SurfaceHead, refs.SurfacePublished} {
		counts, err := f.Store.Surfaces().Counts(context.Background(), container, surface, "stage")
		if err != nil {
			t.Fatal(err)
		}
		if counts.Total > 0 {
			got++
		}
		if counts.Total > 1 {
			t.Fatalf("incorrect total: %+v", counts)
		}
		for _, c := range counts.Values {
			if c.Count != 1 {
				t.Fatalf("incorrect count: %+v", c)
			}
			got++
		}
	}
	if got != want {
		t.Fatalf("container %s: got %d counters, want %d", container, got, want)
	}
}

func AssertOneRejected(t testing.TB, errs []error, rejected error) {
	t.Helper()
	successes, rejections := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, rejected):
			rejections++
		default:
			t.Fatalf("unexpected concurrent mutation error: %v", err)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("want one success and one %v, got %v", rejected, errs)
	}
}

// TermSet creates a term set with key in a new group.
func TermSet(t testing.TB, s *dal.Store, key string) taxonomy.Set {
	t.Helper()
	g, err := s.Taxonomy().CreateGroup(context.Background(), taxonomy.Group{Name: "Group " + key})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.Taxonomy().CreateSet(context.Background(), taxonomy.Set{GroupID: g.ID, Key: key, Name: key})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TaskFixture(t testing.TB, newStore func(testing.TB) *dal.Store) (*dal.Store, taskstore.CreateInput) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)
	for _, id := range []string{"alice", "bob", "carol"} {
		if _, err := s.Directory().CreateUser(ctx, id, id, "", true, false); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := s.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Directory().CreateGroup(ctx, "reviewers", "Reviewers", "", directory.SourceLocal); err != nil {
		t.Fatal(err)
	}
	return s, taskstore.CreateInput{WorkspaceID: ws.ID, Title: "Review", Assignees: []string{"alice", "bob"}, Source: "user", CreatedBy: "alice", Outcomes: []model.TaskOutcome{
		{Key: "approved", Label: "Approve", Style: "primary"},
		{Key: "rejected", Label: "Reject", Comment: "required", SkipForm: true, AllowStale: true},
	}}
}
