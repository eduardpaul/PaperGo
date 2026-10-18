package database

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

func TestAllocationReconcilesWhileWriting(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "papergo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.Exec(`CREATE TABLE alloc_probe(payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	stop, started := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			query := `INSERT INTO alloc_probe VALUES(zeroblob(4000))`
			if i%40 == 39 {
				query = `DELETE FROM alloc_probe`
			}
			_, err := db.SQL.ExecContext(ctx, query)
			if i == 0 {
				close(started)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	<-started
	for range 100 {
		alloc, err := ReadAllocation(ctx, db.SQL)
		if err != nil {
			t.Fatal(err)
		}
		pages := int64(alloc.PageCount) * int64(alloc.PageSize)
		var named int64
		for _, table := range alloc.Tables {
			named += table.Bytes
		}
		if alloc.AccountedBytes+alloc.FreelistBytes != pages || named != alloc.AccountedBytes {
			t.Fatalf("pages %d accounted %d named %d freelist %d", pages, alloc.AccountedBytes, named, alloc.FreelistBytes)
		}
	}
}

func TestAllocationReconcilesPagesAndFreelist(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "papergo.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.SQL.Exec(`CREATE TABLE alloc_probe(id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("a"), 500)
	for i := 0; i < 200; i++ {
		if _, err = db.SQL.Exec(`INSERT INTO alloc_probe(payload) VALUES(?)`, payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.SQL.Exec(`DELETE FROM alloc_probe WHERE id <= 100`); err != nil {
		t.Fatal(err)
	}
	alloc, err := ReadAllocation(ctx, db.SQL)
	if err != nil {
		t.Fatal(err)
	}
	pages := int64(alloc.PageCount) * int64(alloc.PageSize)
	var named, unused int64
	for _, table := range alloc.Tables {
		named += table.Bytes
		unused += table.Unused
	}
	if alloc.DatabaseBytes != pages || alloc.AccountedBytes+alloc.FreelistBytes != pages || named+alloc.FreelistBytes != pages {
		t.Fatalf("pages %d accounted %d file %d named %d freelist %d (%d pages) unused %d", pages, alloc.AccountedBytes, alloc.DatabaseBytes, named, alloc.FreelistBytes, alloc.FreelistCount, alloc.UnusedBytes)
	}
	if unused != alloc.UnusedBytes || alloc.FreelistCount == 0 || alloc.UnusedBytes == 0 {
		t.Fatalf("freelist pages %d slack %d", alloc.FreelistCount, alloc.UnusedBytes)
	}
	t.Logf("pages %d accounted %d freelist %d (%d pages) slack %d file %d", pages, alloc.AccountedBytes, alloc.FreelistBytes, alloc.FreelistCount, alloc.UnusedBytes, alloc.DatabaseBytes)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ReadAllocation(ctx, db.SQL)
	if err != nil {
		t.Fatal(err)
	}
	pages = int64(again.PageCount) * int64(again.PageSize)
	named, unused = 0, 0
	for _, table := range again.Tables {
		named += table.Bytes
		unused += table.Unused
	}
	if again.DatabaseBytes != pages || again.AccountedBytes+again.FreelistBytes != pages || named+again.FreelistBytes != pages || again.FreelistCount == 0 || again.UnusedBytes == 0 || unused != again.UnusedBytes {
		t.Fatalf("restart pages %d accounted %d file %d named %d freelist %d slack %d", pages, again.AccountedBytes, again.DatabaseBytes, named, again.FreelistBytes, again.UnusedBytes)
	}
}
