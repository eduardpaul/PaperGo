package daltest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"modernc.org/sqlite"

	"papergo/internal/dal"
	"papergo/internal/dal/database"
)

// WriteMeter measures what the database does for the writes code under test
// makes through SQL, a handle on the test's database that counts every
// statement. Between Start and Stop it also reads the engine's own account of
// the writes: on PostgreSQL the WAL bytes the cluster wrote and each table's
// tuple counters, on SQLite the pages the WAL received.
type WriteMeter struct {
	SQL *sql.DB

	db      *database.Database
	dialect dal.Dialect
	mu      sync.Mutex
	counts  statementCounts
	before  engineState
	started time.Time
}

// WriteCost is what a measured span cost.
type WriteCost struct {
	Statements int
	Reads      int
	Writes     int
	// Elapsed is the span's wall time.
	Elapsed time.Duration
	// Tables holds, per table, the write statements that named it and the
	// rows they changed, as the driver reported them.
	Tables map[string]TableWrites
	// WALBytes is the write-ahead log the span produced: PostgreSQL's WAL
	// bytes, or SQLite's WAL pages times the page size.
	WALBytes int64
	// Tuples holds PostgreSQL's per-table tuple counters; empty on SQLite.
	Tuples map[string]TupleCounts
}

// TableWrites counts the write statements on one table and their rows.
type TableWrites struct {
	Inserts, Updates, Deletes int
	Rows                      int64
}

// TupleCounts are PostgreSQL's counters of one table's changed tuples. An
// update that is not HOT writes an entry into every index of the table.
type TupleCounts struct {
	Inserted, Updated, HOT, Deleted int64
}

type statementCounts struct {
	reads, writes int
	tables        map[string]TableWrites
}

type engineState struct {
	wal    int64
	tuples map[string]TupleCounts
}

// NewWriteMeter opens a counting handle on db's database. It closes with
// the test.
func NewWriteMeter(t testing.TB, db *database.Database) *WriteMeter {
	t.Helper()
	m := &WriteMeter{db: db, dialect: db.DAL.Dialect(), counts: statementCounts{tables: map[string]TableWrites{}}}
	var connector driver.Connector
	if m.dialect == dal.DialectPostgres {
		config, err := pgx.ParseConfig(db.URL)
		if err != nil {
			t.Fatal(err)
		}
		config.RuntimeParams["jit"] = "off"
		connector = stdlib.GetConnector(*config)
	} else {
		// The meter reads the WAL's length at Stop, so its connections never
		// checkpoint on their own.
		dsn := strings.Replace(database.DSN(db.Path, database.DefaultOptions), fmt.Sprintf("wal_autocheckpoint%%28%d%%29", database.WALAutoCheckpointPages), "wal_autocheckpoint%280%29", 1)
		if !strings.Contains(dsn, "wal_autocheckpoint%280%29") {
			t.Fatal("the SQLite DSN does not set wal_autocheckpoint")
		}
		c, err := sqlite.NewConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		connector = c
	}
	m.SQL = sql.OpenDB(countingConnector{Connector: connector, meter: m})
	m.SQL.SetMaxOpenConns(8)
	m.SQL.SetMaxIdleConns(8)
	t.Cleanup(func() { m.SQL.Close() })
	return m
}

// Start begins a measured span.
func (m *WriteMeter) Start(t testing.TB) {
	t.Helper()
	m.mu.Lock()
	m.counts = statementCounts{tables: map[string]TableWrites{}}
	m.mu.Unlock()
	m.before = m.engine(t, true)
	m.mu.Lock()
	m.counts = statementCounts{tables: map[string]TableWrites{}}
	m.mu.Unlock()
	m.started = time.Now()
}

// Stop ends the span and returns its cost.
func (m *WriteMeter) Stop(t testing.TB) WriteCost {
	t.Helper()
	elapsed := time.Since(m.started)
	m.mu.Lock()
	counts := m.counts
	m.counts = statementCounts{tables: map[string]TableWrites{}}
	m.mu.Unlock()
	after := m.engine(t, false)
	out := WriteCost{Elapsed: elapsed, Statements: counts.reads + counts.writes, Reads: counts.reads, Writes: counts.writes, Tables: counts.tables, WALBytes: after.wal - m.before.wal, Tuples: map[string]TupleCounts{}}
	if m.dialect == dal.DialectSQLite {
		out.WALBytes = after.wal
	}
	for table, a := range after.tuples {
		b := m.before.tuples[table]
		d := TupleCounts{Inserted: a.Inserted - b.Inserted, Updated: a.Updated - b.Updated, HOT: a.HOT - b.HOT, Deleted: a.Deleted - b.Deleted}
		if d != (TupleCounts{}) {
			out.Tuples[table] = d
		}
	}
	return out
}

// engine reads the engine's write counters. PostgreSQL backends publish
// their table counters when they exit, so the idle connections of both
// handles close first.
func (m *WriteMeter) engine(t testing.TB, start bool) engineState {
	t.Helper()
	ctx := context.Background()
	if m.dialect == dal.DialectSQLite {
		conn, err := m.SQL.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var pageSize int64
		if err = conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
			t.Fatal(err)
		}
		mode := "PASSIVE"
		if start {
			mode = "TRUNCATE"
		}
		var busy, frames, checkpointed int64
		if err = conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint("+mode+")").Scan(&busy, &frames, &checkpointed); err != nil {
			t.Fatal(err)
		}
		if busy != 0 {
			t.Fatal("the WAL checkpoint was blocked")
		}
		if start {
			return engineState{}
		}
		return engineState{wal: frames * pageSize}
	}
	for _, db := range []*sql.DB{m.SQL, m.db.SQL} {
		db.SetMaxIdleConns(0)
		db.SetMaxIdleConns(8)
	}
	reader, err := sql.Open("pgx", m.db.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var state engineState
	// Exiting backends publish their counters a moment after they close.
	for stable := 0; stable < 3; {
		time.Sleep(50 * time.Millisecond)
		next := engineState{tuples: map[string]TupleCounts{}}
		rows, err := reader.QueryContext(ctx, `SELECT relname, n_tup_ins, n_tup_upd, n_tup_hot_upd, n_tup_del FROM pg_stat_user_tables`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			var c TupleCounts
			if err = rows.Scan(&name, &c.Inserted, &c.Updated, &c.HOT, &c.Deleted); err != nil {
				t.Fatal(err)
			}
			next.tuples[name] = c
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = reader.ExecContext(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		if sameTuples(next.tuples, state.tuples) {
			stable++
		} else {
			stable = 0
		}
		state.tuples = next.tuples
	}
	if err = reader.QueryRowContext(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(), '0/0')::bigint`).Scan(&state.wal); err != nil {
		t.Fatal(err)
	}
	return state
}

func sameTuples(a, b map[string]TupleCounts) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Write prints a cost per operation of n operations as a table.
func (c WriteCost) Write(w io.Writer, name string, n int) {
	per := func(v int64) string { return fmt.Sprintf("%.1f", float64(v)/float64(n)) }
	fmt.Fprintf(w, "%s: %s statements (%s reads, %s writes), %s rows written, %s WAL bytes, %.2f ms per operation\n",
		name, per(int64(c.Statements)), per(int64(c.Reads)), per(int64(c.Writes)), per(c.RowsWritten()), per(c.WALBytes), float64(c.Elapsed.Microseconds())/1000/float64(n))
	tables := make([]string, 0, len(c.Tables)+len(c.Tuples))
	seen := map[string]bool{}
	for t := range c.Tables {
		tables, seen[t] = append(tables, t), true
	}
	for t := range c.Tuples {
		if !seen[t] {
			tables = append(tables, t)
		}
	}
	sort.Strings(tables)
	fmt.Fprintf(w, "  %-26s %7s %7s %7s %7s", "table", "insert", "update", "delete", "rows")
	if len(c.Tuples) > 0 {
		fmt.Fprintf(w, " %8s %8s %8s %8s", "tup.ins", "tup.upd", "tup.hot", "tup.del")
	}
	fmt.Fprintln(w)
	for _, t := range tables {
		s := c.Tables[t]
		fmt.Fprintf(w, "  %-26s %7s %7s %7s %7s", t, per(int64(s.Inserts)), per(int64(s.Updates)), per(int64(s.Deletes)), per(s.Rows))
		if len(c.Tuples) > 0 {
			u := c.Tuples[t]
			fmt.Fprintf(w, " %8s %8s %8s %8s", per(u.Inserted), per(u.Updated), per(u.HOT), per(u.Deleted))
		}
		fmt.Fprintln(w)
	}
}

// RowsWritten is the rows every write statement changed.
func (c WriteCost) RowsWritten() int64 {
	var n int64
	for _, t := range c.Tables {
		n += t.Rows
	}
	return n
}

var (
	readStatement  = regexp.MustCompile(`(?is)^\s*(SELECT|PRAGMA|VALUES|SHOW|EXPLAIN)\b`)
	writeStatement = regexp.MustCompile(`(?is)(?:^|[^A-Z_])(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+"?([A-Za-z_][A-Za-z0-9_]*)"?`)
	forUpdate      = regexp.MustCompile(`(?is)FOR\s+(NO\s+KEY\s+)?$`)
	control        = regexp.MustCompile(`(?is)^\s*(BEGIN|COMMIT|ROLLBACK|SAVEPOINT|RELEASE|SET|SAVEPOINT)\b`)
)

// record counts one statement and the rows it changed.
func (m *WriteMeter) record(query string, rows int64) {
	if control.MatchString(query) {
		return
	}
	verb, table := "", ""
	if !readStatement.MatchString(query) {
		for _, match := range writeStatement.FindAllStringSubmatchIndex(query, -1) {
			if forUpdate.MatchString(query[:match[2]]) {
				continue
			}
			verb, table = strings.ToUpper(strings.Fields(query[match[2]:match[3]])[0]), strings.ToLower(query[match[4]:match[5]])
			break
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if table == "" {
		m.counts.reads++
		return
	}
	m.counts.writes++
	s := m.counts.tables[table]
	switch verb {
	case "INSERT", "REPLACE":
		s.Inserts++
	case "UPDATE":
		s.Updates++
	case "DELETE":
		s.Deletes++
	}
	if rows > 0 {
		s.Rows += rows
	}
	m.counts.tables[table] = s
}

// The counting driver wraps the engine driver's connections and statements.

type countingConnector struct {
	driver.Connector
	meter *WriteMeter
}

func (c countingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, meter: c.meter}, nil
}

type countingConn struct {
	driver.Conn
	meter *WriteMeter
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var stmt driver.Stmt
	var err error
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err = p.PrepareContext(ctx, query)
	} else {
		stmt, err = c.Conn.Prepare(query)
	}
	if err != nil {
		return nil, err
	}
	return &countingStmt{Stmt: stmt, query: query, meter: c.meter}, nil
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	// Both engines' drivers begin transactions with their options.
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	c.meter.record(query, n)
	return res, nil
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &countingRows{Rows: rows, query: query, meter: c.meter}, nil
}

func (c *countingConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *countingConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *countingConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *countingConn) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := c.Conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

// countingStmt counts the executions of a prepared statement. Both engines'
// drivers execute statements with a context, the methods database/sql calls.
type countingStmt struct {
	driver.Stmt
	query string
	meter *WriteMeter
}

func (s *countingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	e, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, args)
	if err == nil {
		n, _ := res.RowsAffected()
		s.meter.record(s.query, n)
	}
	return res, err
}

func (s *countingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &countingRows{Rows: rows, query: s.query, meter: s.meter}, nil
}

func (s *countingStmt) CheckNamedValue(v *driver.NamedValue) error {
	if n, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

// countingRows counts a query's returned rows as the rows a write with
// RETURNING changed.
type countingRows struct {
	driver.Rows
	query string
	meter *WriteMeter
	n     int64
	done  bool
}

func (r *countingRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		r.n++
	}
	return err
}

func (r *countingRows) ColumnTypeDatabaseTypeName(index int) string {
	if c, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return c.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (r *countingRows) ColumnTypeScanType(index int) reflect.Type {
	if c, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return c.ColumnTypeScanType(index)
	}
	return reflect.TypeFor[any]()
}

func (r *countingRows) Close() error {
	if !r.done {
		r.done = true
		r.meter.record(r.query, r.n)
	}
	return r.Rows.Close()
}
