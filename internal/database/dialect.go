package database

import (
	"context"
	"database/sql"
	"io"
)

// Lease is an exclusive or shared hold on a database or storage resource.
type Lease interface {
	io.Closer
}

// Dialect abstracts database-specific SQL generation, parameter binding,
// and concurrency control across SQLite and PostgreSQL.
type Dialect interface {
	Name() string
	EntDialect() string
	Rebind(query string) string

	// SQL Expressions
	JSONExtract(col string, path string) string
	JSONEach(col string) string
	JSONType(col string) string
	Instr(haystack, needle string) string
	Lower(col string) string
	CollateNoCase(col string) string
	BooleanLiteral(b bool) string
	ForUpdateSkipLocked() string

	// Transaction & Concurrency Control
	ExecuteWrite(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error
	AcquireLease(path string, exclusive bool) (Lease, error)
}
