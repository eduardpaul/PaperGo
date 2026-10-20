package daltest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"papergo/internal/dal"
)

// Store tests run on both engines: SQLite always, PostgreSQL when
// TEST_POSTGRES_DSN names a server whose user may create databases.

// Engines runs a test once per engine, as subtests named SQLite and
// Postgres, each opening stores with newStore.
func Engines(t *testing.T, test func(t *testing.T, newStore func(testing.TB) *dal.Store)) {
	t.Run("SQLite", func(t *testing.T) { test(t, SQLiteStore) })
	t.Run("Postgres", func(t *testing.T) { test(t, PostgresStore) })
}

// Main runs a package's tests and drops the PostgreSQL template its stores
// were cloned from. Each package that opens PostgreSQL stores calls it from
// TestMain.
func Main(m *testing.M) {
	code := m.Run()
	if dropStoreTemplate != nil {
		dropStoreTemplate()
	}
	os.Exit(code)
}

// sqliteTemplate is a database file with the SQLite schema installed. Tests
// copy it instead of installing the schema, which is slow under the race
// detector.
var sqliteTemplate = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "daltest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.sqlite")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := dal.NewStoreWithDialect(db, dal.DialectSQLite).Migrate(context.Background()); err != nil {
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// SQLiteStore opens a store on a new SQLite database with the schema.
func SQLiteStore(t testing.TB) *dal.Store {
	t.Helper()
	template, err := sqliteTemplate()
	if err != nil {
		t.Fatalf("create sqlite template: %v", err)
	}
	file := filepath.Join(t.TempDir(), "test.sqlite")
	if err := os.WriteFile(file, template, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_time_format=sqlite&_texttotime=1")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	return dal.NewStoreWithDialect(db, dal.DialectSQLite)
}

// storeServer connects to the server TEST_POSTGRES_DSN names, where store
// databases are created and dropped.
type storeServer struct {
	config   *pgx.ConnConfig
	db       *sql.DB
	template string
}

var dropStoreTemplate func()

// storeTemplate creates, once per test binary, a database with the
// PostgreSQL schema installed. Each store clones it with CREATE DATABASE ...
// TEMPLATE, which copies files instead of running the schema DDL.
var storeTemplate = sync.OnceValues(func() (*storeServer, error) {
	config, err := pgx.ParseConfig(os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		return nil, fmt.Errorf("parse TEST_POSTGRES_DSN: %w", err)
	}
	config.ConnectTimeout = 5 * time.Second
	server := &storeServer{config: config, db: stdlib.OpenDB(*config), template: storeName()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := dropStale(ctx, server.db); err != nil {
		server.db.Close()
		return nil, err
	}
	if _, err := server.db.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{server.template}.Sanitize()); err != nil {
		server.db.Close()
		return nil, fmt.Errorf("create postgres template database: %w", err)
	}
	dropStoreTemplate = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = server.db.ExecContext(ctx, "DROP DATABASE "+pgx.Identifier{server.template}.Sanitize()+" WITH (FORCE)")
		server.db.Close()
	}
	// The template must have no open connections when it is cloned.
	db := stdlib.OpenDB(*server.connConfig(server.template))
	defer db.Close()
	if err := dal.NewStoreWithDialect(db, dal.DialectPostgres).Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate postgres template: %w", err)
	}
	return server, db.Close()
})

func (s *storeServer) connConfig(database string) *pgx.ConnConfig {
	config := s.config.Copy()
	config.Database = database
	return config
}

func storeName() string {
	return postgresPrefix + fmt.Sprint(time.Now().Unix()) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

// PostgresStore opens a store on a new PostgreSQL database with the schema,
// dropped after the test. Without TEST_POSTGRES_DSN the test skips.
func PostgresStore(t testing.TB) *dal.Store {
	t.Helper()
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping PostgreSQL integration test")
	}
	server, err := storeTemplate()
	if err != nil {
		t.Fatal(err)
	}
	name := storeName()
	quoted := pgx.Identifier{name}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := server.db.ExecContext(ctx, "CREATE DATABASE "+quoted+" TEMPLATE "+pgx.Identifier{server.template}.Sanitize()); err != nil {
		t.Fatalf("create postgres test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := server.db.ExecContext(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop postgres test database: %v", err)
		}
	})
	db := stdlib.OpenDB(*server.connConfig(name))
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	return dal.NewStoreWithDialect(db, dal.DialectPostgres)
}
