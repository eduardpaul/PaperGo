package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/surfaces"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestCollectionPassesSpanBatches(t *testing.T) {
	previousSurface, previousOperation := surfaceBatch, operationBatch
	surfaceBatch, operationBatch = 3, 3
	t.Cleanup(func() { surfaceBatch, operationBatch = previousSurface, previousOperation })
	s, _, list := fixture(t)
	n, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "n", Label: "N", Type: "integer"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "owner", Label: "Owner", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	items := []string{}
	for i := 0; i < 10; i++ {
		values := map[string]any{"n": num(i), "owner": "alice"}
		if i == 9 {
			delete(values, "owner")
		}
		r := create(t, s, list.ID, "item", fmt.Sprintf("Item %d", i), values)
		if i%2 == 0 {
			if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, r.ID)
	}
	surfaceCount, err := countSurfaces(s, surfaces.Filter{ContainerID: list.ID})
	if err != nil || surfaceCount != 15 {
		t.Fatalf("surfaces: %d %v", surfaceCount, err)
	}
	indexed := true
	if _, err = s.UpdateField(testContext, "alice", list.ID, n.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &indexed}); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	if rows, err := countFieldValues(s, surfaces.ValueFilter{}); err != nil || rows != surfaceCount {
		t.Fatalf("rebuild indexed %d of %d surfaces: %v", rows, surfaceCount, err)
	}
	page, err := s.Browse(testContext, "alice", Browse{ParentID: list.ID, FilterField: "n", FilterOp: "eq", FilterValue: "7"})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != items[7] {
		t.Fatalf("filter after rebuild: %+v %v", page, err)
	}
	indexed = false
	if _, err = s.UpdateField(testContext, "alice", list.ID, n.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &indexed}); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	if rows, err := countFieldValues(s, surfaces.ValueFilter{}); err != nil || rows != 0 {
		t.Fatalf("rows left after unindexing: %d %v", rows, err)
	}
	required := true
	if _, err = s.UpdateField(testContext, "alice", list.ID, owner.ID, latest(t, s, list.ID).Version, UpdateField{Required: &required}); err == nil {
		t.Fatal("required check missed an item outside the first batch")
	}
	automatic := false
	if _, err = s.Update(testContext, "alice", list.ID, latest(t, s, list.ID).Version, UpdateResource{PublishingEnabled: &automatic}); err != nil {
		t.Fatal(err)
	}
	for _, id := range items {
		r := current(t, s, id)
		if r.PublishedRevisionID == nil || *r.PublishedRevisionID != *r.HeadRevisionID {
			t.Fatalf("automatic publishing skipped %s", id)
		}
	}
	if published, err := countSurfaces(s, surfaces.Filter{ContainerID: list.ID, Surface: refs.SurfacePublished}); err != nil || published != len(items) {
		t.Fatalf("published surfaces: %d %v", published, err)
	}
}

// The candidate-driven plan and the ID-ordered scan must return identical pages.
func TestBrowseCandidateAndScanPathsAgree(t *testing.T) {
	s, w, list := fixture(t)
	readers(t, s, w)
	if _, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "n", Label: "N", Type: "integer", Indexed: true}); err != nil {
		t.Fatal(err)
	}
	other := create(t, s, w.ID, "list", "Alpha archive", nil)
	create(t, s, other.ID, "item", "alpha elsewhere", nil)
	for i := 0; i < 12; i++ {
		keyword := "even"
		if i%2 == 1 {
			keyword = "odd"
		}
		r, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: fmt.Sprintf("alpha %d", i), Keywords: []string{keyword}, Values: map[string]any{"n": num(i)}})
		if err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			continue // draft only
		}
		if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 {
			r = latest(t, s, r.ID)
			name, keywords, values := fmt.Sprintf("secret %d", i), []string{"hidden"}, map[string]any{"n": num(100 + i)}
			if _, err = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &name, Keywords: &keywords, Values: &values}); err != nil {
				t.Fatal(err)
			}
		}
	}
	queries := []Browse{
		{WorkspaceID: w.ID, Search: "alpha"},
		{WorkspaceID: w.ID, Search: "secret"},
		{WorkspaceID: w.ID, Search: "nothing"},
		{WorkspaceID: w.ID, Keyword: "even"},
		{WorkspaceID: w.ID, Keyword: "hidden"},
		{ParentID: list.ID, Search: "alpha", Keyword: "odd"},
		{ParentID: list.ID, FilterField: "n", FilterOp: "gte", FilterValue: "5"},
		{ParentID: list.ID, FilterField: "n", FilterOp: "eq", FilterValue: "104"},
		{ParentID: list.ID, FilterField: "n", FilterOp: "lt", FilterValue: "9", Search: "alpha"},
	}
	run := func(limit int, subject string, in Browse) string {
		previous := content.CandidateDriveLimit
		content.CandidateDriveLimit = limit
		defer func() { content.CandidateDriveLimit = previous }()
		out := ""
		for {
			page, err := s.Browse(testContext, subject, in)
			if err != nil {
				return out + "error: " + err.Error()
			}
			for _, r := range page.Data {
				out += r.ID + ":" + r.Name + " "
			}
			if page.NextCursor == "" {
				return out
			}
			in.After = page.NextCursor
		}
	}
	indexSearch(t, s)
	for _, subject := range []string{"alice", "reader"} {
		for _, surface := range []string{"auto", "head", "published"} {
			for i, in := range queries {
				in.Surface = surface
				in.Limit = 2
				driven, scanned := run(1000, subject, in), run(-1, subject, in)
				if !reflect.DeepEqual(driven, scanned) {
					t.Errorf("%s/%s query %d: candidate path %q, scan path %q", subject, surface, i, driven, scanned)
				}
				if i == 0 && subject == "alice" && surface == "head" && driven == "" {
					t.Fatal("fixture produced no matches; comparison would be vacuous")
				}
			}
		}
	}
	if got := run(1000, "reader", Browse{ParentID: list.ID, Search: "secret"}); got != "" {
		t.Fatalf("draft-only name leaked to reader: %s", got)
	}
}

func num(i int) json.Number { return json.Number(strconv.Itoa(i)) }

func TestWriterQueueHonorsCancellation(t *testing.T) {
	s, w, _ := fixture(t)
	_ = s.writer.acquire(testContext) // another writer holds the slot
	ctx, cancel := context.WithTimeout(testContext, 50*time.Millisecond)
	defer cancel()
	if _, err := s.Create(ctx, "alice", w.ID, CreateResource{Kind: "list", Name: "Queued"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued writer ignored its deadline", err)
	}
	s.writer.release()
	if _, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Next"}); err != nil {
		t.Fatal("writer slot not usable after a cancelled wait", err)
	}
}
