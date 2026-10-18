package database

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWALPolicyOnEveryConnectionAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "papergo.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	assertWALPolicy(t, db.SQL)
	first, err := db.SQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.SQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertWALPolicy(t, first)
	assertWALPolicy(t, second)
	first.Close()
	second.Close()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", DSN(abs, DefaultOptions))
	if err != nil {
		t.Fatal(err)
	}
	if err = other.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	assertWALPolicy(t, other)
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	assertWALPolicy(t, reopened.SQL)
	dsn := DSN(abs, DefaultOptions)
	for _, part := range []string{"journal_mode%28WAL%29", "synchronous%28FULL%29", "wal_autocheckpoint%28256%29", "journal_size_limit%281048576%29"} {
		if !strings.Contains(dsn, part) {
			t.Fatalf("dsn missing %s: %s", part, dsn)
		}
	}
}

func TestCompletedCheckpointBoundsTheJournal(t *testing.T) {
	ctx := context.Background()
	const rows, size = 700, 3000
	policyPath := filepath.Join(t.TempDir(), "policy.db")
	controlPath := filepath.Join(t.TempDir(), "control.db")
	policy, err := Open(ctx, policyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { policy.Close() })
	var pageSize int
	if err = policy.SQL.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil || pageSize != 4096 {
		t.Fatalf("page size %d: %v", pageSize, err)
	}
	control, err := sql.Open("sqlite", controlDSN(t, controlPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { control.Close() })
	if err = control.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	policyWrite := writePayload(t, policy.SQL, rows, size)
	controlWrite := writePayload(t, control, rows, size)
	policyGrown := fileSize(t, policyPath+"-wal")
	controlGrown := fileSize(t, controlPath+"-wal")
	if policyGrown <= JournalSizeLimitBytes || controlGrown <= JournalSizeLimitBytes {
		t.Fatalf("journals after the large commit: policy %d control %d", policyGrown, controlGrown)
	}
	// A completed passive checkpoint copies frames and leaves the file at its
	// high water. The next commit restarts that journal and applies the limit.
	policyCheckpointStarted := time.Now()
	status, err := ObserveCheckpoint(ctx, policy.SQL)
	if err != nil {
		t.Fatal(err)
	}
	policyCheckpoint := time.Since(policyCheckpointStarted)
	controlCheckpointStarted := time.Now()
	var busy, log, checkpointed int
	if err = control.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed); err != nil {
		t.Fatal(err)
	}
	controlCheckpoint := time.Since(controlCheckpointStarted)
	if status.Incomplete || busy != 0 || log != checkpointed {
		t.Fatalf("checkpoint policy %+v control busy=%d log=%d checkpointed=%d", status, busy, log, checkpointed)
	}
	policyFollow := writeOne(t, policy.SQL)
	controlFollow := writeOne(t, control)
	policyJournal := fileSize(t, policyPath+"-wal")
	controlJournal := fileSize(t, controlPath+"-wal")
	if policyJournal <= 0 || policyJournal > JournalSizeLimitBytes || controlJournal <= JournalSizeLimitBytes {
		t.Fatalf("journals after the following commit: policy %d control %d", policyJournal, controlJournal)
	}
	var policyRows, controlRows int
	if err = policy.SQL.QueryRow("SELECT count(*) FROM wal_probe").Scan(&policyRows); err != nil || policyRows != rows+1 {
		t.Fatalf("policy rows %d: %v", policyRows, err)
	}
	if err = control.QueryRow("SELECT count(*) FROM wal_probe").Scan(&controlRows); err != nil || controlRows != rows+1 {
		t.Fatalf("control rows %d: %v", controlRows, err)
	}
	t.Logf("wal policy: write %s checkpoint %s follow-up %s journal %d after %d; default autocheckpoint: write %s checkpoint %s follow-up %s journal %d after %d; pages copied %d",
		policyWrite, policyCheckpoint, policyFollow, policyGrown, policyJournal, controlWrite, controlCheckpoint, controlFollow, controlGrown, controlJournal, status.Checkpointed)
}

func TestLargeTransactionAndBlockedReaderKeepCommittedData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "papergo.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.SQL.Exec(`CREATE TABLE wal_probe(id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec(`INSERT INTO wal_probe(payload) VALUES(?)`, []byte("seed")); err != nil {
		t.Fatal(err)
	}
	reader, err := db.SQL.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var seen int
	if err = reader.QueryRow("SELECT count(*) FROM wal_probe").Scan(&seen); err != nil || seen != 1 {
		t.Fatalf("reader snapshot %d: %v", seen, err)
	}
	conn, err := db.SQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA cache_size = -32"); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("y"), 3000)
	const extra = 500
	for i := 0; i < extra; i++ {
		if _, err = tx.Exec(`INSERT INTO wal_probe(payload) VALUES(?)`, payload); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	during := fileSize(t, path+"-wal")
	var visible int
	if err = db.SQL.QueryRow("SELECT count(*) FROM wal_probe").Scan(&visible); err != nil || visible != 1 {
		tx.Rollback()
		t.Fatalf("uncommitted rows visible: %d %v", visible, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if during <= JournalSizeLimitBytes {
		t.Fatalf("open transaction stayed within the journal limit: %d", during)
	}
	blocked, err := ObserveCheckpoint(ctx, db.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked.Incomplete || blocked.JournalBytes <= JournalSizeLimitBytes {
		t.Fatalf("reader did not keep the journal: %+v during %d", blocked, during)
	}
	if err = reader.QueryRow("SELECT count(*) FROM wal_probe").Scan(&seen); err != nil || seen != 1 {
		t.Fatalf("reader snapshot moved: %d %v", seen, err)
	}
	if err = db.SQL.QueryRow("SELECT count(*) FROM wal_probe").Scan(&visible); err != nil || visible != extra+1 {
		t.Fatalf("committed rows: %d %v", visible, err)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	done, err := ObserveCheckpoint(ctx, db.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if done.Incomplete || done.JournalBytes <= JournalSizeLimitBytes {
		t.Fatalf("passive checkpoint changed the blocked journal: %+v", done)
	}
	if _, err = db.SQL.Exec(`INSERT INTO wal_probe(payload) VALUES(?)`, []byte("tail")); err != nil {
		t.Fatal(err)
	}
	reset := fileSize(t, path+"-wal")
	if reset <= 0 || reset > JournalSizeLimitBytes {
		t.Fatalf("journal after the following commit: %d", reset)
	}
	if err = db.SQL.QueryRow("SELECT count(*) FROM wal_probe").Scan(&visible); err != nil || visible != extra+2 {
		t.Fatalf("rows after checkpoint: %d %v", visible, err)
	}
	var seed string
	if err = db.SQL.QueryRow("SELECT payload FROM wal_probe WHERE id = 1").Scan(&seed); err != nil || seed != "seed" {
		t.Fatalf("seed after checkpoint: %q %v", seed, err)
	}
	t.Logf("journal during open transaction %d, while reader blocked checkpoint %d, after passive checkpoint %d, after the following commit %d", during, blocked.JournalBytes, done.JournalBytes, reset)
}

type pragmaConn interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func assertWALPolicy(t *testing.T, db pragmaConn) {
	t.Helper()
	ctx := context.Background()
	var mode string
	var synchronous, auto int
	var limit int64
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %s: %v", mode, err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("synchronous %d: %v", synchronous, err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&auto); err != nil || auto != WALAutoCheckpointPages {
		t.Fatalf("wal_autocheckpoint %d: %v", auto, err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_size_limit").Scan(&limit); err != nil || limit != JournalSizeLimitBytes {
		t.Fatalf("journal_size_limit %d: %v", limit, err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign keys %d: %v", foreignKeys, err)
	}
}

func writePayload(t *testing.T, db *sql.DB, rows, size int) time.Duration {
	t.Helper()
	start := time.Now()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`CREATE TABLE wal_probe(id INTEGER PRIMARY KEY, payload BLOB)`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO wal_probe(payload) VALUES(?)`)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), size)
	for i := 0; i < rows; i++ {
		if _, err = stmt.Exec(payload); err != nil {
			stmt.Close()
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = stmt.Close(); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return time.Since(start)
}

func controlDSN(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	uriPath := filepath.ToSlash(abs)
	if filepath.VolumeName(abs) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	q.Set("_time_format", "sqlite")
	q.Set("_txlock", DefaultOptions.TxLock)
	for _, pragma := range []string{
		"foreign_keys(1)",
		fmt.Sprintf("busy_timeout(%d)", DefaultOptions.BusyTimeout.Milliseconds()),
		"journal_mode(WAL)",
		"synchronous(FULL)",
	} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func writeOne(t *testing.T, db *sql.DB) time.Duration {
	t.Helper()
	start := time.Now()
	if _, err := db.Exec(`INSERT INTO wal_probe(payload) VALUES(?)`, []byte("tail")); err != nil {
		t.Fatal(err)
	}
	return time.Since(start)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
