package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"entgo.io/ent/dialect"
	"errors"
	"fmt"
	"papergo/internal/database/sqlgen"
	"time"
)

// PostgresDialect implements Dialect for PostgreSQL databases.
type PostgresDialect struct {
	db *sql.DB
}

func NewPostgresDialect(db *sql.DB) *PostgresDialect {
	return &PostgresDialect{db: db}
}

func (d *PostgresDialect) Name() string       { return "postgres" }
func (d *PostgresDialect) EntDialect() string { return dialect.Postgres }
func (d *PostgresDialect) Rebind(query string) string {
	return sqlgen.RebindPostgres(query)
}

func (d *PostgresDialect) JSONExtract(col string, path string) string {
	return fmt.Sprintf("(%s->>'%s')", col, path)
}

func (d *PostgresDialect) JSONEach(col string) string {
	return fmt.Sprintf("jsonb_array_elements_text((%s)::jsonb)", col)
}

func (d *PostgresDialect) JSONType(col string) string {
	return fmt.Sprintf("jsonb_typeof((%s)::jsonb)", col)
}

func (d *PostgresDialect) Instr(haystack, needle string) string {
	return fmt.Sprintf("(STRPOS(%s, %s) > 0)", haystack, needle)
}

func (d *PostgresDialect) Lower(col string) string {
	return fmt.Sprintf("LOWER(%s)", col)
}

func (d *PostgresDialect) CollateNoCase(col string) string {
	return fmt.Sprintf("LOWER(%s)", col)
}

func (d *PostgresDialect) BooleanLiteral(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func (d *PostgresDialect) ForUpdateSkipLocked() string {
	return " FOR UPDATE SKIP LOCKED"
}

func (d *PostgresDialect) ExecuteWrite(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	const maxRetries = 3
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		tx, beginErr := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if beginErr != nil {
			return beginErr
		}

		err = fn(tx)
		if err != nil {
			_ = tx.Rollback()
			if isRetryable(err) && attempt < maxRetries-1 {
				time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
				continue
			}
			return err
		}

		err = tx.Commit()
		if err == nil {
			return nil
		}
		if isRetryable(err) && attempt < maxRetries-1 {
			time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
			continue
		}
		return err
	}

	return err
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 40001: serialization_failure, 40P01: deadlock_detected
	return errors.Is(err, sql.ErrConnDone) ||
		containsAny(msg, "40001", "40P01", "deadlock detected", "could not serialize access")
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) && hasSubstr(s, sub) {
			return true
		}
	}
	return false
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func hashToAdvisoryKey(target string) int64 {
	h := sha256.Sum256([]byte("papergo:lease:" + target))
	return int64(binary.BigEndian.Uint64(h[:8]))
}

func (d *PostgresDialect) AcquireLease(target string, exclusive bool) (Lease, error) {
	if d.db == nil {
		return nil, errors.New("postgres lease requires database handle")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}

	key := hashToAdvisoryKey(target)
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		conn.Close()
		return nil, err
	}

	if !locked {
		conn.Close()
		return nil, fmt.Errorf("data is in use; stop the API before restoring or upgrading: lease %s is held", target)
	}

	return &postgresLease{
		conn: conn,
		key:  key,
	}, nil
}

type postgresLease struct {
	conn *sql.Conn
	key  int64
}

func (l *postgresLease) Close() error {
	if l.conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.key)
	return l.conn.Close()
}
