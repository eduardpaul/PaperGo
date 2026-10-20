package zvecstore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/search"
)

func TestOfflineCopyRoundTripPreservesSourceRecords(t *testing.T) {
	ctx := context.Background()
	source, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	d := testDocument("copy")
	d.Fields = []search.Field{{Key: "large", Type: "integer", Values: []search.FieldValue{{Int: 9007199254740993}}}, {Key: "precise", Type: "number", Values: []search.FieldValue{{Text: "exact-96-bit-ordering-key"}}}}
	if err = source.UpsertDocuments(ctx, []search.Document{d}, 42); err != nil {
		t.Fatal(err)
	}
	// Cross the copy batch boundary inside one generation and part.
	chunks := make([]search.Chunk, 205)
	for i := range chunks {
		chunks[i] = search.Chunk{Part: "read", Ordinal: i, Kind: "content", Page: i + 1, Text: "invoice payment " + strings.Repeat("a", i%7+1)}
	}
	if err = source.StageContent(ctx, d.ResourceID, d.BlobID, "published", chunks); err != nil {
		t.Fatal(err)
	}
	if err = source.PublishContent(ctx, d.ResourceID, d.BlobID, "published"); err != nil {
		t.Fatal(err)
	}
	if err = source.StageContent(ctx, d.ResourceID, "other-blob", "staged", []search.Chunk{{Part: "read", Text: "unpublished"}}); err != nil {
		t.Fatal(err)
	}
	todo, _, err := source.GenerationToEmbed(ctx, "model", "published")
	if err != nil {
		t.Fatal(err)
	}
	vs := []search.HashVector{}
	for _, c := range todo {
		vs = append(vs, search.HashVector{InputHash: c.InputHash, Vector: []float32{1, 0, 0, 0}})
	}
	if err = source.SetVectors(ctx, "model", vs); err != nil {
		t.Fatal(err)
	}
	native := openTest(t, filepath.Join(t.TempDir(), "zvec"))
	if err = search.Copy(ctx, source, native); err != nil {
		t.Fatal(err)
	}
	if err = search.Copy(ctx, source, native); err != nil {
		t.Fatal("idempotent import", err)
	}
	destination, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "copy.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err = search.Copy(ctx, native, destination); err != nil {
		t.Fatal(err)
	}
	records := func(s search.Store) []string {
		t.Helper()
		out := []string{}
		if err := s.Export(ctx, func(r search.Record) error { raw, err := json.Marshal(r); out = append(out, string(raw)); return err }); err != nil {
			t.Fatal(err)
		}
		sort.Strings(out)
		return out
	}
	if a, b := records(source), records(destination); !reflect.DeepEqual(a, b) {
		t.Fatalf("source records changed: %d -> %d", len(a), len(b))
	}
	if todo, _, err = destination.GenerationToEmbed(ctx, "model", "published"); err != nil || len(todo) != 0 {
		t.Fatal("copied inputs need embedding again", todo, err)
	}
}
