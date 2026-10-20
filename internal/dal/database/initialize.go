package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Initialize installs the embedded DAL schema in a fresh file. Existing files
// must already match the selected schema; local databases are disposable.
func Initialize(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	fresh := err == nil
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if fresh {
		if err := file.Close(); err != nil {
			return err
		}
	}
	lease, err := AcquireLease(path, false)
	if err != nil {
		return err
	}
	defer lease.Close()
	db, err := Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	if fresh {
		if err := db.DAL.Migrate(ctx); err != nil {
			return err
		}
	}
	return db.CheckSchema(ctx)
}
