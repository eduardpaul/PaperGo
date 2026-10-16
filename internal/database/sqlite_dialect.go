package database

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	"fmt"
	"os"
	"sync"
)

// SQLiteDialect implements Dialect for SQLite databases.
type SQLiteDialect struct {
	writeMu sync.Mutex
}

var SQLite = &SQLiteDialect{}

func (d *SQLiteDialect) Name() string       { return "sqlite" }
func (d *SQLiteDialect) EntDialect() string { return dialect.SQLite }
func (d *SQLiteDialect) Rebind(query string) string {
	return query
}

func (d *SQLiteDialect) JSONExtract(col string, path string) string {
	return fmt.Sprintf("json_extract(%s, '$.%s')", col, path)
}

func (d *SQLiteDialect) JSONEach(col string) string {
	return fmt.Sprintf("json_each(%s)", col)
}

func (d *SQLiteDialect) JSONType(col string) string {
	return fmt.Sprintf("json_type(%s)", col)
}

func (d *SQLiteDialect) Instr(haystack, needle string) string {
	return fmt.Sprintf("(instr(%s, %s) > 0)", haystack, needle)
}

func (d *SQLiteDialect) Lower(col string) string {
	return fmt.Sprintf("unicode_lower(%s)", col)
}

func (d *SQLiteDialect) CollateNoCase(col string) string {
	return fmt.Sprintf("%s COLLATE NOCASE", col)
}

func (d *SQLiteDialect) BooleanLiteral(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (d *SQLiteDialect) ForUpdateSkipLocked() string {
	return ""
}

func (d *SQLiteDialect) ExecuteWrite(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *SQLiteDialect) AcquireLease(path string, exclusive bool) (Lease, error) {
	f, err := AcquireLease(path, exclusive)
	if err != nil {
		return nil, err
	}
	return fileLease{f: f}, nil
}

type fileLease struct {
	f *os.File
}

func (l fileLease) Close() error {
	return l.f.Close()
}
