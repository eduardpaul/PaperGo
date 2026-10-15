package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"papergo/internal/database"
	"papergo/internal/search"
	"papergo/internal/search/zvecstore"
)

func TestOfflineNativeSearchBackup(t *testing.T) {
	in, _, item, _ := fixture(t)
	ctx := context.Background()
	key := bytes.Repeat([]byte{42}, 32)
	if err := os.WriteFile(in.DatabasePath+".integrations.key", key, 0600); err != nil {
		t.Fatal(err)
	}
	in.SearchStore, in.SearchPath = "zvec", filepath.Join(filepath.Dir(in.DatabasePath), "search", "zvec")
	open := func(path string) *zvecstore.Store {
		s, err := zvecstore.Open(ctx, path, zvecstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	s := open(in.SearchPath)
	d := search.Document{ResourceID: item, Surface: search.SurfacePublished, Views: search.ViewPublished, ScopeID: "scope", Name: "Native backup", BlobID: "blob"}
	c := search.Chunk{Part: "read", Text: "saved invoice", Page: 3}
	for _, err := range []error{
		s.UpsertDocuments(ctx, []search.Document{d}, 1),
		s.StageContent(ctx, item, "blob", "generation", []search.Chunk{c}),
		s.PublishContent(ctx, item, "blob", "generation"),
		s.SetVectors(ctx, "model", []search.HashVector{{InputHash: c.Hash(), Vector: []float32{1, 0, 0, 0}}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Create(ctx, in); err == nil {
		t.Fatal("backup accepted an open native collection")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := database.AcquireLease(in.DatabasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = Create(ctx, in); err == nil {
		t.Fatal("native backup accepted a running API")
	}
	lease.Close()
	if err = Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	out := Options{DatabasePath: filepath.Join(root, "main.db"), BlobPath: filepath.Join(root, "blobs"), ArchivePath: in.ArchivePath, SearchStore: "zvec", SearchPath: filepath.Join(root, "search", "zvec")}
	if err = os.MkdirAll(out.SearchPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = Restore(ctx, out); err == nil {
		t.Fatal("existing native destination overwritten without force")
	}
	if err = os.Remove(out.SearchPath); err != nil {
		t.Fatal(err)
	}
	if err = Restore(ctx, out); err != nil {
		t.Fatal(err)
	}
	if restoredKey, err := os.ReadFile(out.DatabasePath + ".integrations.key"); err != nil || !bytes.Equal(restoredKey, key) {
		t.Fatal("integration encryption key was not restored alongside native search", err)
	}
	restored := open(out.SearchPath)
	if restored.Fresh() {
		t.Fatal("native snapshot discarded")
	}
	result, err := restored.Search(ctx, search.Query{Mode: search.ModeSemantic, Model: "model", Vector: []float32{1, 0, 0, 0}, Content: true, Filter: search.Filter{Access: search.Access{Read: []string{"scope"}}}})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].ResourceID != item || result.Hits[0].Page != 3 {
		t.Fatal(result, err)
	}
}
