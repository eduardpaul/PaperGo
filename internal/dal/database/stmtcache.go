package database

import (
	"container/list"
	"context"
	"database/sql/driver"

	"modernc.org/sqlite"
)

// stmtCacheSize bounds the prepared statements one connection keeps.
const stmtCacheSize = 256

// cachingConnector opens SQLite connections that keep their prepared
// statements by SQL text. SQLite parses and plans a statement every time it
// is prepared, and PaperGo's statements, permission checks above all,
// repeat with the same text and different arguments; a cached statement is
// only reset and bound again.
type cachingConnector struct{ driver.Connector }

func (c cachingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &cachingConn{conn: conn, entries: map[string]*list.Element{}, order: list.New()}, nil
}

// innerConn is what the SQLite driver's connections implement.
type innerConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// cachingConn is a connection with a bounded LRU of prepared statements. A
// connection serves one caller at a time, so it needs no locking; a
// statement whose rows are still open is busy, and a second use of its text
// meanwhile prepares a statement of its own.
type cachingConn struct {
	conn    driver.Conn
	entries map[string]*list.Element
	order   *list.List
}

type cachedStmt struct {
	query string
	stmt  driver.Stmt
	busy  bool
}

func (c *cachingConn) inner() innerConn { return c.conn.(innerConn) }

// statement returns the cached statement for query, preparing it on first
// use, or nil when it is busy.
func (c *cachingConn) statement(ctx context.Context, query string) (*cachedStmt, error) {
	if e, ok := c.entries[query]; ok {
		c.order.MoveToFront(e)
		s := e.Value.(*cachedStmt)
		if s.busy {
			return nil, nil
		}
		return s, nil
	}
	stmt, err := c.inner().PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	s := &cachedStmt{query: query, stmt: stmt}
	c.entries[query] = c.order.PushFront(s)
	for c.order.Len() > stmtCacheSize {
		oldest := c.order.Back().Value.(*cachedStmt)
		if oldest.busy {
			break
		}
		c.evict(oldest)
	}
	return s, nil
}

func (c *cachingConn) evict(s *cachedStmt) {
	c.order.Remove(c.entries[s.query])
	delete(c.entries, s.query)
	_ = s.stmt.Close()
}

func (c *cachingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s, err := c.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return c.inner().QueryContext(ctx, query, args)
	}
	rows, err := s.stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		c.forget(s, err)
		return nil, err
	}
	s.busy = true
	return &cachedRows{Rows: rows, stmt: s}, nil
}

func (c *cachingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	s, err := c.statement(ctx, query)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return c.inner().ExecContext(ctx, query, args)
	}
	res, err := s.stmt.(driver.StmtExecContext).ExecContext(ctx, args)
	if err != nil {
		c.forget(s, err)
	}
	return res, err
}

// forget drops a statement that failed: SQLite may have left it in a state
// a reset does not cover, and failures are rare.
func (c *cachingConn) forget(s *cachedStmt, _ error) {
	if e, ok := c.entries[s.query]; ok && e.Value == s {
		c.evict(s)
	}
}

func (c *cachingConn) Prepare(query string) (driver.Stmt, error) { return c.conn.Prepare(query) }
func (c *cachingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.inner().PrepareContext(ctx, query)
}
func (c *cachingConn) Begin() (driver.Tx, error) {
	return c.inner().BeginTx(context.Background(), driver.TxOptions{})
}
func (c *cachingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.inner().BeginTx(ctx, opts)
}
func (c *cachingConn) Ping(ctx context.Context) error         { return c.inner().Ping(ctx) }
func (c *cachingConn) ResetSession(ctx context.Context) error { return c.inner().ResetSession(ctx) }
func (c *cachingConn) IsValid() bool                          { return c.inner().IsValid() }
func (c *cachingConn) NewBackup(dst string) (*sqlite.Backup, error) {
	return c.conn.(backuper).NewBackup(dst)
}

type backuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// Close finalizes the cached statements before the connection.
func (c *cachingConn) Close() error {
	for e := c.order.Front(); e != nil; e = e.Next() {
		_ = e.Value.(*cachedStmt).stmt.Close()
	}
	c.entries, c.order = nil, list.New()
	return c.conn.Close()
}

// cachedRows frees its statement for reuse once closed; the driver resets
// the statement instead of finalizing it.
type cachedRows struct {
	driver.Rows
	stmt *cachedStmt
}

func (r *cachedRows) Close() error {
	r.stmt.busy = false
	return r.Rows.Close()
}
