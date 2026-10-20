package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLeaseUsesSameFileForDatabaseAliases(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "data.db")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.db")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	api, err := AcquireLease(alias, false)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if restore, e := AcquireLease(target, true); e == nil {
		restore.Close()
		t.Fatal("restore bypassed an API lease held through a database alias")
	}
	backup, err := AcquireLease(target, false)
	if err != nil {
		t.Fatal("shared backup lease should coexist with the API", err)
	}
	backup.Close()
	api.Close()
	restore, err := AcquireLease(target, true)
	if err != nil {
		t.Fatal("restore should work after API shutdown", err)
	}
	restore.Close()
}
