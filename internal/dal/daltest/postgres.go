package daltest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"papergo/internal/dal/database"
)

// Fixture databases are named with their creation time, so a later run can
// drop the ones an interrupted run left behind.
const postgresPrefix = "papergo_fixture_"

// postgresServer is the server PostgresDSN names, with the fixture database
// tests clone.
type postgresServer struct {
	admin    *pgx.Conn
	mu       sync.Mutex
	template string
}

var postgresFixture = sync.OnceValues(func() (*postgresServer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	config, err := pgx.ParseConfig(PostgresDSN())
	if err != nil {
		return nil, fmt.Errorf("parse PAPERGO_TEST_POSTGRES_DSN: %w", err)
	}
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	server := &postgresServer{admin: admin}
	stale := stdlib.OpenDB(*config)
	err = dropStale(ctx, stale)
	stale.Close()
	if err != nil {
		return nil, err
	}
	server.template = server.name()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{server.template}.Sanitize()); err != nil {
		return nil, fmt.Errorf("create postgres fixture: %w", err)
	}
	db, err := database.OpenPostgres(ctx, server.dsn(server.template))
	if err != nil {
		return nil, err
	}
	err = seed(ctx, db)
	// The fixture must have no connections when it is cloned.
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	return server, err
})

func (s *postgresServer) name() string {
	return postgresPrefix + strconv.FormatInt(time.Now().Unix(), 10) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func (s *postgresServer) dsn(database string) string {
	dsn := PostgresDSN()
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + database
		return u.String()
	}
	return dsn + " dbname=" + database
}

// dropStale drops test databases an interrupted run left behind a day ago.
func dropStale(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT datname FROM pg_database WHERE starts_with(datname, $1)", postgresPrefix)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, name := range names {
		created, _, _ := strings.Cut(strings.TrimPrefix(name, postgresPrefix), "_")
		if unix, err := strconv.ParseInt(created, 10, 64); err == nil && time.Since(time.Unix(unix, 0)) > 24*time.Hour {
			if _, err = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *postgresServer) exec(ctx context.Context, statement string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.admin.Exec(ctx, statement)
	return err
}

// postgresDatabase clones the fixture into a database the test drops; empty
// creates one with only the schema instead.
func postgresDatabase(t testing.TB, empty bool) *database.Database {
	t.Helper()
	server, err := postgresFixture()
	if err != nil {
		t.Fatalf("create postgres fixture: %v", err)
	}
	ctx := context.Background()
	name := server.name()
	quoted := pgx.Identifier{name}.Sanitize()
	create := "CREATE DATABASE " + quoted + " TEMPLATE " + pgx.Identifier{server.template}.Sanitize()
	if empty {
		create = "CREATE DATABASE " + quoted
	}
	if err = server.exec(ctx, create); err != nil {
		t.Fatalf("create postgres test database: %v", err)
	}
	db, err := database.OpenPostgres(ctx, server.dsn(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if err := server.exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop postgres test database: %v", err)
		}
	})
	if empty {
		if err = db.DAL.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
