package daltest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"papergo/internal/dal/database"
	"papergo/internal/identity"
)

// PostgresDSN names the PostgreSQL server application tests run on instead
// of SQLite, from PAPERGO_TEST_POSTGRES_DSN; empty runs them on SQLite.
func PostgresDSN() string { return os.Getenv("PAPERGO_TEST_POSTGRES_DSN") }

// Database creates a fixture database for the test and opens it: a SQLite
// file, or a PostgreSQL database when PostgresDSN is set.
func Database(t testing.TB) *database.Database {
	t.Helper()
	if PostgresDSN() != "" {
		return postgresDatabase(t, false)
	}
	return DatabaseAt(t, filepath.Join(t.TempDir(), "test.db"))
}

// EmptyDatabase creates a database with the schema and nothing else.
func EmptyDatabase(t testing.TB) *database.Database {
	t.Helper()
	if PostgresDSN() != "" {
		return postgresDatabase(t, true)
	}
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.DAL.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

// Open opens the test database path names, as a process that restarts
// opens its database again, creating it from the fixture the first time:
// the SQLite file at path, or a PostgreSQL database whose URL the file at
// path records. The test closes each handle it opens.
func Open(t testing.TB, path string) *database.Database {
	t.Helper()
	ctx := context.Background()
	if _, err := os.Stat(path); err != nil {
		if PostgresDSN() == "" {
			return DatabaseAt(t, path)
		}
		db := postgresDatabase(t, false)
		if err = os.WriteFile(path, []byte(db.URL), 0o600); err != nil {
			t.Fatal(err)
		}
		return db
	}
	var db *database.Database
	var err error
	if PostgresDSN() == "" {
		db, err = database.Open(ctx, path)
	} else {
		var url []byte
		if url, err = os.ReadFile(path); err == nil {
			db, err = database.OpenPostgres(ctx, string(url))
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// DatabaseAt creates the fixture database at path and opens it.
func DatabaseAt(t testing.TB, path string) *database.Database {
	t.Helper()
	contents, err := fixture()
	if err != nil {
		t.Fatalf("create fixture database: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var foreignKeys, synchronous, autoCheckpoint int
	var journalMode string
	var journalLimit int64
	if err = db.SQL.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign keys disabled: %d %v", foreignKeys, err)
	}
	if err = db.SQL.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("journal mode: %s %v", journalMode, err)
	}
	if err = db.SQL.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("synchronous: %d %v", synchronous, err)
	}
	if err = db.SQL.QueryRow("PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil || autoCheckpoint != database.WALAutoCheckpointPages {
		t.Fatalf("wal_autocheckpoint: %d %v", autoCheckpoint, err)
	}
	if err = db.SQL.QueryRow("PRAGMA journal_size_limit").Scan(&journalLimit); err != nil || journalLimit != database.JournalSizeLimitBytes {
		t.Fatalf("journal_size_limit: %d %v", journalLimit, err)
	}
	return db
}

// fixture builds the fixture database once per test binary: the DAL schema,
// the organization and the fixture users. Tests copy its file, which is much
// faster than installing the schema and directory for each test.
var fixture = sync.OnceValues(func() ([]byte, error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "daltest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fixture.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = seed(ctx, db); err != nil {
		return nil, err
	}
	// Move every page into the database file so the copy is complete.
	if _, err = db.SQL.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, err
	}
	if err = db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// seed installs the DAL schema, the organization and the fixture users.
func seed(ctx context.Context, db *database.Database) error {
	if err := db.DAL.Migrate(ctx); err != nil {
		return fmt.Errorf("apply DAL schema: %w", err)
	}
	directory := identity.Directory{DB: db.SQL}
	if err := directory.Initialize(ctx, "organization-admin", ""); err != nil {
		return err
	}
	// Content fixtures exercise ACL ownership independently of organization-wide
	// administration. Alice can curate schemas/taxonomy but has no workspace.manage.
	for _, subject := range []string{"alice", "bob", "carol", "dave", "mallory", "writer", "reader", "visitor", "member", "owner", "other", "eve", "admin", "local-admin", "delegate", "editor", "outsider", "stranger", "publisher", "rita"} {
		if err := directory.EnsureUser(ctx, subject); err != nil {
			return err
		}
	}
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Fixture curator", Scopes: []string{"contentType.manage", "taxonomy.manage", "user.read", "user.manage", "group.read", "group.manage", "role.read", "role.manage", "audit.read"}})
	if err != nil {
		return err
	}
	return directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false)
}
