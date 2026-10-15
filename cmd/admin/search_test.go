package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"papergo/internal/search"
	"papergo/internal/search/sqlitestore"
	"papergo/internal/search/zvecstore"
)

func TestSearchCopyCommand(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(root, "papergo.db")
	if err := os.WriteFile(database, nil, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := sqlitestore.Open(ctx, filepath.Join(root, "search.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = source.UpsertDocuments(ctx, []search.Document{{ResourceID: "a", Surface: "published", Views: 3, Name: "Invoice"}}, 1); err != nil {
		t.Fatal(err)
	}
	source.Close()
	args := []string{"search", "copy", "--to", "zvec", "--database", database}
	if err = run(ctx, append(append([]string{}, args...), "--languages", "none"), new(bytes.Buffer)); err == nil {
		t.Fatal("copied with incompatible source languages")
	}
	// A rejected copy must not recreate or empty the source.
	preserved, err := sqlitestore.Open(ctx, filepath.Join(root, "search.db"), sqlitestore.Options{Languages: []string{"english"}, NoRebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := preserved.Stats(ctx); err != nil || stats.Documents != 1 {
		t.Fatal(stats, err)
	}
	preserved.Close()
	if err = run(ctx, args, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if err = run(ctx, args, new(bytes.Buffer)); err == nil {
		t.Fatal("existing destination overwritten")
	}
	s, err := zvecstore.Open(ctx, filepath.Join(root, "search", "zvec"), zvecstore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if stats, err := s.Stats(ctx); err != nil || stats.Documents != 1 {
		t.Fatal(stats, err)
	}
}
