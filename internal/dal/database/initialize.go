package database

import (
	"context"
)

// Initialize creates or upgrades the SQLite database at path, as the server
// does when it starts, while no process uses it.
func Initialize(ctx context.Context, path string) error {
	lease, err := AcquireLease(path, true)
	if err != nil {
		return err
	}
	defer lease.Close()
	db, err := Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Upgrade(ctx); err != nil {
		return err
	}
	return db.CheckSchema(ctx)
}

// InitializePostgres creates or upgrades the PostgreSQL database dsn names,
// as the server does when it starts.
func InitializePostgres(ctx context.Context, dsn string) error {
	db, err := OpenPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Upgrade(ctx); err != nil {
		return err
	}
	return db.CheckSchema(ctx)
}
