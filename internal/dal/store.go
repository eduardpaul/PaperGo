package dal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/stephenafamo/bob"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/delta"
	"papergo/internal/dal/dialect"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/driver/postgres"
	"papergo/internal/dal/driver/sqlite"
	"papergo/internal/dal/events"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/live"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/portable"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/runs"
	"papergo/internal/dal/surfaces"
	"papergo/internal/dal/tasks"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/dal/views"
	"papergo/internal/dal/waits"
	"papergo/internal/dal/workflows"
)

//go:embed schema/sqlite.sql
var SQLiteSchema string

//go:embed schema/postgres.sql
var PostgresSchema string

// Dialect identifies the underlying SQL engine.
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

// Store manages database access and atomic repository transactions.
type Store struct {
	db      bob.DB
	sqlDB   *sql.DB
	tx      *sql.Tx
	txExec  bob.Executor
	dialect Dialect
}

// queries is every domain contract a driver implements.
type queries interface {
	audit.Queries
	blobs.Queries
	catalog.Queries
	delta.Queries
	directory.Queries
	events.Queries
	grants.Queries
	integrations.Queries
	live.Queries
	operations.Queries
	relationships.Queries
	resources.Queries
	revisions.Queries
	runs.Queries
	surfaces.Queries
	tasks.Queries
	taxonomy.Queries
	views.Queries
	waits.Queries
	workflows.Queries
}

// bind runs domain statements with the store's driver on exec.
func (s *Store) bind(exec bob.Executor) queries {
	if s.dialect == DialectSQLite {
		return sqlite.Bind(exec)
	}
	return postgres.Bind(exec)
}

// writer returns a function that runs a domain's statements atomically with
// the store's driver, inside the store's transaction when WithTx was used.
func writer[Q any](s *Store) func(context.Context, func(Q) error) error {
	return func(ctx context.Context, fn func(Q) error) error {
		return s.transact(ctx, func(exec bob.Executor) error { return fn(any(s.bind(exec)).(Q)) })
	}
}

// Audit returns the audit log, bound to the store's transaction when WithTx
// was used.
func (s *Store) Audit() *audit.Repo {
	return audit.NewRepo(s.bind(s.exec()), writer[audit.Queries](s))
}

// Blobs returns the blob repository, bound to the store's transaction when
// WithTx was used.
func (s *Store) Blobs() *blobs.Repo {
	return blobs.NewRepo(s.bind(s.exec()), writer[blobs.Queries](s))
}

// Grants returns the resource grant repository, bound to the store's
// transaction when WithTx was used.
func (s *Store) Grants() *grants.Repo {
	return grants.NewRepo(s.bind(s.exec()), writer[grants.Queries](s))
}

// Integrations returns the list integration repository, bound to the store's
// transaction when WithTx was used.
func (s *Store) Integrations() *integrations.Repo {
	return integrations.NewRepo(s.bind(s.exec()), writer[integrations.Queries](s))
}

// LiveChanges returns the change notification log.
func (s *Store) LiveChanges() *live.Repo {
	return live.NewRepo(s.bind(s.exec()))
}

// Catalog returns the field definitions, content types and the content
// types collections accept, bound to the store's transaction when WithTx was
// used.
func (s *Store) Catalog() *catalog.Repo {
	return catalog.NewRepo(s.bind(s.exec()), writer[catalog.Queries](s))
}

// Content runs content queries, inside the store's transaction when WithTx
// was used.
func (s *Store) Content() *content.Repo {
	return content.NewRepo(s.exec(), s.sqlDialect())
}

// Portable returns the package row repository, bound to the store's
// transaction when WithTx was used.
func (s *Store) Portable() *portable.Repo {
	return portable.NewRepo(s.exec(), s.sqlDialect())
}

// sqlDialect is the store's engine for statements built as text.
func (s *Store) sqlDialect() dialect.Dialect {
	if s.dialect == DialectPostgres {
		return dialect.Postgres
	}
	return dialect.SQLite
}

// Delta returns the collection synchronization repository, bound to the
// store's transaction when WithTx was used.
func (s *Store) Delta() *delta.Repo {
	return delta.NewRepo(s.bind(s.exec()), writer[delta.Queries](s))
}

// Directory returns identity records, settings and group inboxes, bound to the
// store's transaction when WithTx was used.
func (s *Store) Directory() *directory.Repo {
	return directory.NewRepo(s.bind(s.exec()), writer[directory.Queries](s))
}

// Events returns the domain event log, bound to the store's transaction when
// WithTx was used.
func (s *Store) Events() *events.Repo {
	return events.NewRepo(s.bind(s.exec()), writer[events.Queries](s))
}

// Operations returns the background operation repository, bound to the
// store's transaction when WithTx was used.
func (s *Store) Operations() *operations.Repo {
	return operations.NewRepo(s.bind(s.exec()), writer[operations.Queries](s))
}

// Relationships returns the links between items and their types, bound to
// the store's transaction when WithTx was used.
func (s *Store) Relationships() *relationships.Repo {
	return relationships.NewRepo(s.bind(s.exec()), writer[relationships.Queries](s))
}

// Resources returns the content hierarchy, bound to the store's
// transaction when WithTx was used.
func (s *Store) Resources() *resources.Repo {
	return resources.NewRepo(s.bind(s.exec()), writer[resources.Queries](s))
}

// Revisions returns item revisions, their history and publications, bound
// to the store's transaction when WithTx was used.
func (s *Store) Revisions() *revisions.Repo {
	return revisions.NewRepo(s.bind(s.exec()), writer[revisions.Queries](s))
}

// Runs returns the workflow run repository, bound to the store's transaction
// when WithTx was used.
func (s *Store) Runs() *runs.Repo {
	return runs.NewRepo(s.bind(s.exec()), writer[runs.Queries](s))
}

// Surfaces returns item surfaces, their indexed field values and group
// counts, bound to the store's transaction when WithTx was used.
func (s *Store) Surfaces() *surfaces.Repo {
	return surfaces.NewRepo(s.bind(s.exec()), writer[surfaces.Queries](s))
}

// Tasks returns the task repository, bound to the store's transaction when
// WithTx was used.
func (s *Store) Tasks() *tasks.Repo {
	return tasks.NewRepo(s.bind(s.exec()), writer[tasks.Queries](s))
}

// Taxonomy returns the organization term store, bound to the store's
// transaction when WithTx was used.
func (s *Store) Taxonomy() *taxonomy.Repo {
	return taxonomy.NewRepo(s.bind(s.exec()), writer[taxonomy.Queries](s))
}

// Views returns list views and smart folders, bound to the store's
// transaction when WithTx was used.
func (s *Store) Views() *views.Repo {
	return views.NewRepo(s.bind(s.exec()), writer[views.Queries](s))
}

// Waits returns the workflow wait repository, bound to the store's
// transaction when WithTx was used.
func (s *Store) Waits() *waits.Repo {
	return waits.NewRepo(s.bind(s.exec()), writer[waits.Queries](s))
}

// Workflows returns the workflow definition repository, bound to the store's
// transaction when WithTx was used.
func (s *Store) Workflows() *workflows.Repo {
	return workflows.NewRepo(s.bind(s.exec()), writer[workflows.Queries](s))
}

// NewStore creates a new Store wrapping a standard *sql.DB connection pool.
// It automatically detects whether the underlying driver is SQLite or PostgreSQL.
func NewStore(sqlDB *sql.DB) *Store {
	dialect := detectDialect(sqlDB)
	return &Store{
		db:      bob.NewDB(sqlDB),
		sqlDB:   sqlDB,
		dialect: dialect,
	}
}

// NewStoreWithDialect creates a Store with an explicit dialect override.
func NewStoreWithDialect(sqlDB *sql.DB, dialect Dialect) *Store {
	return &Store{
		db:      bob.NewDB(sqlDB),
		sqlDB:   sqlDB,
		dialect: dialect,
	}
}

// WithTx returns a Store bound to an existing *sql.Tx transaction.
// All repository statements on the returned Store execute inside tx
// without opening nested transactions.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	if tx == nil {
		return s
	}
	return &Store{
		db:      s.db,
		sqlDB:   s.sqlDB,
		tx:      tx,
		txExec:  bob.NewTx(tx),
		dialect: s.dialect,
	}
}

func detectDialect(sqlDB *sql.DB) Dialect {
	if sqlDB == nil {
		return DialectPostgres
	}
	driverType := fmt.Sprintf("%T", sqlDB.Driver())
	if strings.Contains(strings.ToLower(driverType), "sqlite") {
		return DialectSQLite
	}
	return DialectPostgres
}

// exec is the executor driver statements run on, bound to tx when WithTx was used.
func (s *Store) exec() bob.Executor {
	if s.txExec != nil {
		return s.txExec
	}
	return s.db
}

// SQLDB returns the underlying *sql.DB.
func (s *Store) SQLDB() *sql.DB {
	return s.sqlDB
}

// Tx returns the active *sql.Tx if this Store is bound to a transaction, or nil.
func (s *Store) Tx() *sql.Tx {
	return s.tx
}

// execContext executes a SQL statement on the active transaction (if bound via WithTx) or on the underlying *sql.DB.
func (s *Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.sqlDB.ExecContext(ctx, query, args...)
}

// queryRowContext executes a single-row SQL query on the active transaction (if bound via WithTx) or on the underlying *sql.DB.
func (s *Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.sqlDB.QueryRowContext(ctx, query, args...)
}

// Dialect returns the active SQL dialect.
func (s *Store) Dialect() Dialect {
	return s.dialect
}

// Migrate applies the embedded clean DDL schema for the store's dialect.
func (s *Store) Migrate(ctx context.Context) error {
	ddl := PostgresSchema
	if s.dialect == DialectSQLite {
		ddl = SQLiteSchema
	}
	if s.tx != nil {
		if _, err := s.tx.ExecContext(ctx, ddl); err != nil {
			return dberr.Translate(err)
		}
		_, err := s.tx.ExecContext(ctx, `INSERT INTO schema_metadata(id,hash) VALUES(1,$1)`, s.SchemaHash())
		return dberr.Translate(err)
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return dberr.Translate(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		return dberr.Translate(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_metadata(id,hash) VALUES(1,$1)`, s.SchemaHash()); err != nil {
		return dberr.Translate(err)
	}
	return dberr.Translate(tx.Commit())
}

// Retry runs a transaction again while the database refuses it for a
// concurrent one: a PostgreSQL serialization failure or deadlock, or a busy
// SQLite file. Each attempt waits longer, up to about a second in all.
func Retry(ctx context.Context, attempt func() error) error {
	for wait := 5 * time.Millisecond; ; wait *= 2 {
		err := attempt()
		if !dberr.IsRetryable(err) || wait > 640*time.Millisecond {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait + time.Duration(rand.Int64N(int64(wait)))):
		}
	}
}

// transact runs fn atomically on the driver's executor, again when the
// database refuses it for a concurrent transaction. When the Store is bound
// to a *sql.Tx via WithTx, fn executes directly inside that transaction,
// whose owner retries it.
func (s *Store) transact(ctx context.Context, fn func(exec bob.Executor) error) error {
	if s.txExec != nil {
		return dberr.Translate(fn(s.txExec))
	}
	return Retry(ctx, func() error { return s.attempt(ctx, fn) })
}

func (s *Store) attempt(ctx context.Context, fn func(exec bob.Executor) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dberr.Translate(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if s.dialect == DialectSQLite {
		// Even an UPDATE matching no rows acquires SQLite's reserved write lock.
		if _, err := tx.ExecContext(ctx, `UPDATE resources SET id = id WHERE FALSE`); err != nil {
			return dberr.Translate(err)
		}
	}

	if err := fn(tx); err != nil {
		return dberr.Translate(err)
	}

	return dberr.Translate(tx.Commit(ctx))
}

// Savepoint runs fn under a savepoint of the store's transaction. When fn
// reports undo or an error, its changes are rolled back and the transaction
// goes on.
func (s *Store) Savepoint(ctx context.Context, name string, fn func() (undo bool, err error)) error {
	if s.tx == nil {
		return fmt.Errorf("savepoint %s needs a transaction", name)
	}
	if _, err := s.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return dberr.Translate(err)
	}
	undo, err := fn()
	if err != nil {
		_, _ = s.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name)
		_, _ = s.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name)
		return err
	}
	if undo {
		if _, err := s.tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
			return dberr.Translate(err)
		}
	}
	_, err = s.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name)
	return dberr.Translate(err)
}

// SchemaHash identifies the authoritative schema for this store's engine.
func (s *Store) SchemaHash() string {
	schema := SQLiteSchema
	if s.dialect == DialectPostgres {
		schema = PostgresSchema
	}
	sum := sha256.Sum256([]byte(schema))
	return base64.StdEncoding.EncodeToString(sum[:])
}
func (s *Store) CheckSchema(ctx context.Context) error {
	var actual string
	if err := s.queryRowContext(ctx, `SELECT hash FROM schema_metadata WHERE id=1`).Scan(&actual); err != nil {
		return fmt.Errorf("database has no initialized DAL schema: %w", err)
	}
	if actual != s.SchemaHash() {
		return fmt.Errorf("database schema differs from this application; initialize a fresh database")
	}
	return nil
}
func (s *Store) HasTables(ctx context.Context) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`
	if s.dialect == DialectPostgres {
		query = `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'`
	}
	err := s.queryRowContext(ctx, query).Scan(&count)
	return count > 0, err
}
