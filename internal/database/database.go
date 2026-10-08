package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	"modernc.org/sqlite"
	"net/url"
	"os"
	"papergo/ent"
	"path/filepath"
	"strings"
)

// SQLite's built-in lower() folds ASCII only; unicode_lower matches Go's
// strings.ToLower so case-insensitive searches work for every script.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unicode_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		default:
			return v, nil
		}
	})
}

type Database struct {
	SQL    *sql.DB
	Client *ent.Client
}

func Open(ctx context.Context, path string) (*Database, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &Database{SQL: db, Client: ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))}, nil
}
func (d *Database) Close() error { return d.Client.Close() }

// CheckSchema prevents the API from serving with missing or unapplied migrations.
func (d *Database) CheckSchema(ctx context.Context) error {
	var version string
	if err := d.SQL.QueryRowContext(ctx, "SELECT version FROM atlas_schema_revisions WHERE applied = total AND (error IS NULL OR error = '') ORDER BY version DESC LIMIT 1").Scan(&version); err != nil {
		return fmt.Errorf("apply Atlas migrations before starting the API: %w", err)
	}
	if version != "20261007000600" {
		return fmt.Errorf("database migration version %s is incompatible with this API; expected 20261007000600", version)
	}
	if _, err := d.SQL.ExecContext(ctx, "SELECT id FROM item_surface_search LIMIT 0"); err != nil {
		return fmt.Errorf("search migration is missing: %w", err)
	}
	return nil
}
