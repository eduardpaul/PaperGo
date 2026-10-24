package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"
	"papergo/internal/dal"
)

// papergo_time normalizes stored timestamps for comparisons in SQL.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("papergo_time", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		value, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("invalid stored timestamp")
		}
		t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", value)
		if err != nil {
			return nil, err
		}
		return t.UTC().Format("2006-01-02T15:04:05.000000000Z"), nil
	})
}

type Database struct {
	SQL *sql.DB
	DAL *dal.Store
	// URL is the PostgreSQL database's connection URL; empty for SQLite.
	URL string
	// Path is the SQLite file's absolute path; empty for PostgreSQL.
	Path string
}

// Options are the connection settings of a handle on the PaperGo SQLite file.
type Options struct {
	TxLock      string // "" (deferred) or "immediate"
	BusyTimeout time.Duration
}

// DefaultOptions are the settings Open uses. Write transactions take the
// write lock when they begin and wait up to 30 seconds for it, so PaperGo
// requests and runner steps queue for the single SQLite writer instead of
// failing with SQLITE_BUSY. Read-only transactions stay deferred.
var DefaultOptions = Options{TxLock: "immediate", BusyTimeout: 30 * time.Second}

// WALAutoCheckpointPages is the passive checkpoint threshold, in pages.
// At the 4096-byte page size, 256 pages are about 1 MiB of journal.
const WALAutoCheckpointPages = 256

// JournalSizeLimitBytes is the retained -wal size after a checkpoint resets
// the journal. A large transaction or a blocked checkpoint can grow the file
// past this until the next successful checkpoint.
const JournalSizeLimitBytes = 1 << 20

// DSN returns the data source name of the SQLite file at the absolute path
// abs, so every handle on the file uses the same pragmas.
func DSN(abs string, o Options) string {
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	q.Set("_time_format", "sqlite")
	q.Set("_texttotime", "1")
	if o.TxLock != "" {
		q.Set("_txlock", o.TxLock)
	}
	for _, pragma := range []string{
		"foreign_keys(1)",
		fmt.Sprintf("busy_timeout(%d)", o.BusyTimeout.Milliseconds()),
		"journal_mode(WAL)",
		"synchronous(FULL)",
		fmt.Sprintf("wal_autocheckpoint(%d)", WALAutoCheckpointPages),
		fmt.Sprintf("journal_size_limit(%d)", JournalSizeLimitBytes),
	} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// OpenSQLite opens the SQLite database dsn names with connections that
// keep their prepared statements.
func OpenSQLite(dsn string) (*sql.DB, error) {
	connector, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(cachingConnector{connector}), nil
}

func Open(ctx context.Context, path string) (*Database, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	db, err := OpenSQLite(DSN(abs, DefaultOptions))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &Database{
		SQL:  db,
		DAL:  dal.NewStoreWithDialect(db, dal.DialectSQLite),
		Path: abs,
	}, nil
}

// OpenPostgres connects to the PostgreSQL database named by dsn.
func OpenPostgres(ctx context.Context, dsn string) (*Database, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	// PaperGo's statements are short and many embed permission checks whose
	// estimated cost reaches the JIT threshold; compiling them takes far
	// longer than running them.
	if _, set := config.RuntimeParams["jit"]; !set {
		config.RuntimeParams["jit"] = "off"
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return &Database{SQL: db, DAL: dal.NewStoreWithDialect(db, dal.DialectPostgres), URL: dsn}, nil
}

func (d *Database) Close() error { return d.SQL.Close() }

// CheckSchema checks the installed fingerprint against the DAL schema master.
func (d *Database) CheckSchema(ctx context.Context) error { return d.DAL.CheckSchema(ctx) }
