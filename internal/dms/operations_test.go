package dms

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"papergo/ent/fielddefinition"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemsurface"
	"papergo/ent/operation"
	"testing"
	"time"
)

// An index build runs in short batches; writes between batches index the
// building field themselves, so the finished index is exact.
func TestFieldIndexBuildsInBackgroundBatches(t *testing.T) {
	previous := operationBatch
	operationBatch = 3
	t.Cleanup(func() { operationBatch = previous })
	s, _, list := fixture(t)
	d, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "n", Label: "N", Type: "integer"})
	if err != nil {
		t.Fatal(err)
	}
	items := []string{}
	for i := 0; i < 8; i++ {
		r := create(t, s, list.ID, "item", fmt.Sprintf("Item %d", i), map[string]any{"n": num(i)})
		if i%2 == 0 {
			if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, r.ID)
	}
	indexed := true
	field, err := s.UpdateField(testContext, "alice", list.ID, d.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &indexed})
	if err != nil || field.IndexStatus != fielddefinition.IndexStatusBuilding {
		t.Fatalf("toggle: %+v %v", field, err)
	}
	ops, err := s.Operations(testContext, "alice", list.ID, "", 0)
	if err != nil || len(ops.Data) != 1 || ops.Data[0].Status != operation.StatusPending {
		t.Fatalf("queued operation: %+v %v", ops, err)
	}
	if worked, err := s.operationStep(testContext); !worked || err != nil {
		t.Fatal("first batch", worked, err)
	}
	// The writer is free between batches: edit an indexed item and add one.
	r := latest(t, s, items[0])
	values := map[string]any{"n": num(100)}
	if _, err = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &values}); err != nil {
		t.Fatal(err)
	}
	r = latest(t, s, items[7])
	values = map[string]any{"n": num(107)}
	if _, err = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &values}); err != nil {
		t.Fatal(err)
	}
	create(t, s, list.ID, "item", "Late", map[string]any{"n": num(200)})
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	op, err := s.Operation(testContext, "alice", ops.Data[0].ID)
	if err != nil || op.Status != operation.StatusSucceeded || op.Processed == 0 {
		t.Fatalf("finished operation: %+v %v", op, err)
	}
	surfaces, err := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(list.ID)).Count(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Client.FieldValue.Query().Where(fieldvalue.FieldKeyEQ("n")).Count(testContext); err != nil || rows != surfaces {
		t.Fatalf("index rows %d for %d surfaces: %v", rows, surfaces, err)
	}
	for value, want := range map[string]int{"0": 0, "100": 1, "107": 1, "200": 1, "3": 1} {
		page, err := s.Query(testContext, "alice", list.ID, QueryRequest{Surface: "head", Query: QuerySpec{Filter: &FilterExpr{Field: "n", Value: []byte(value)}}})
		if err != nil || len(page.Data) != want {
			t.Fatalf("n=%s: %d rows %v", value, len(page.Data), err)
		}
	}
	// Toggling again before the build runs supersedes it.
	for _, flag := range []bool{false, true, false} {
		if _, err = s.UpdateField(testContext, "alice", list.ID, d.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &flag}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	superseded, err := s.Client.Operation.Query().Where(operation.StatusEQ(operation.StatusSuperseded)).Count(testContext)
	if err != nil || superseded != 2 {
		t.Fatalf("superseded: %d %v", superseded, err)
	}
	if rows, err := s.Client.FieldValue.Query().Where(fieldvalue.FieldKeyEQ("n")).Count(testContext); err != nil || rows != 0 {
		t.Fatalf("rows after unindexing: %d %v", rows, err)
	}
	if got, err := s.Client.FieldDefinition.Get(testContext, d.ID); err != nil || got.IndexStatus != fielddefinition.IndexStatusReady {
		t.Fatalf("final status: %+v %v", got, err)
	}
	if _, err = s.Operation(testContext, "stranger", op.ID); err == nil {
		t.Fatal("operation visible without collection access")
	}
}

// Indexing an empty collection has nothing to build.
func TestEmptyCollectionIndexIsReadyAtOnce(t *testing.T) {
	s, _, list := fixture(t)
	d, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "n", Label: "N", Type: "integer"})
	if err != nil {
		t.Fatal(err)
	}
	indexed := true
	field, err := s.UpdateField(testContext, "alice", list.ID, d.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &indexed})
	if err != nil || field.IndexStatus != fielddefinition.IndexStatusReady {
		t.Fatalf("%+v %v", field, err)
	}
	if n, err := s.Client.Operation.Query().Count(testContext); err != nil || n != 0 {
		t.Fatal("operation queued for an empty collection", n, err)
	}
}

// RunOperations wakes when work is queued and stops with its context.
func TestRunOperationsBuildsQueuedIndexes(t *testing.T) {
	s, _, list := fixture(t)
	d, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "n", Label: "N", Type: "integer"})
	if err != nil {
		t.Fatal(err)
	}
	create(t, s, list.ID, "item", "One", map[string]any{"n": num(1)})
	ctx, cancel := context.WithCancel(testContext)
	stopped := make(chan struct{})
	go func() {
		s.RunOperations(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(stopped)
	}()
	indexed := true
	if _, err = s.UpdateField(testContext, "alice", list.ID, d.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &indexed}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := s.Client.FieldDefinition.Get(testContext, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.IndexStatus == fielddefinition.IndexStatusReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not build the queued index")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("runner ignored cancellation")
	}
}
