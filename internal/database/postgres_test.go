package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestPostgresConnectionAndDialect(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		url = "postgres://postgres:postgres@localhost:5432/papergo?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := Open(ctx, url)
	if err != nil {
		t.Skipf("skipping postgres test (no postgres connection at %s): %v", url, err)
		return
	}
	defer db.Close()

	if db.Dialect == nil || db.Dialect.Name() != "postgres" {
		t.Fatalf("expected postgres dialect, got: %v", db.Dialect)
	}

	// Test ExecuteWrite
	err = db.Dialect.ExecuteWrite(ctx, db.SQL, func(tx *sql.Tx) error {
		var one int
		return tx.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	})
	if err != nil {
		t.Fatalf("ExecuteWrite failed: %v", err)
	}

	// Test AcquireLease (Advisory Lock)
	lease, err := db.Dialect.AcquireLease("test-target", true)
	if err != nil {
		t.Fatalf("AcquireLease failed: %v", err)
	}

	// Concurrent lease should fail
	_, err = db.Dialect.AcquireLease("test-target", true)
	if err == nil {
		lease.Close()
		t.Fatalf("expected concurrent lease to fail, but it succeeded")
	}

	if err = lease.Close(); err != nil {
		t.Fatalf("lease Close failed: %v", err)
	}

	// After release, should succeed again
	lease2, err := db.Dialect.AcquireLease("test-target", true)
	if err != nil {
		t.Fatalf("AcquireLease after release failed: %v", err)
	}
	lease2.Close()
}
