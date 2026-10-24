package database_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/database"
)

// steps returns the embedded migrations followed by one test migration for
// each statement.
func steps(db *database.Database, statements ...string) []dal.Migration {
	out := append([]dal.Migration{}, dal.Migrations(db.DAL.Dialect())...)
	for _, statement := range statements {
		version := len(out) + 1
		out = append(out, dal.Migration{Version: version, Name: fmt.Sprintf("%04d_test.sql", version), SQL: statement})
	}
	return out
}

func installed(t *testing.T, db *database.Database) int {
	t.Helper()
	version, err := db.DAL.InstalledVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestUpgradeCreatesEmptyDatabase(t *testing.T) {
	ctx := t.Context()
	var db *database.Database
	if daltest.PostgresDSN() != "" {
		db = daltest.EmptyDatabase(t)
		if _, err := db.SQL.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	} else {
		var err error
		if db, err = database.Open(ctx, filepath.Join(t.TempDir(), "data", "papergo.db")); err != nil {
			t.Fatal(err)
		}
		defer db.Close()
	}
	result, err := db.Upgrade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.To != dal.SchemaVersion() || result.Snapshot != "" {
		t.Fatalf("%+v", result)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err = db.Upgrade(ctx); err != nil || result.Created || result.From != result.To {
		t.Fatalf("restart changed the database: %+v %v", result, err)
	}
}

func TestUpgradeAppliesPendingMigrations(t *testing.T) {
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	base := installed(t, db)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO identity_users(id,created_at,display_name) VALUES('persisted',CURRENT_TIMESTAMP,'Preserved')`); err != nil {
		t.Fatal(err)
	}
	migrations := steps(db,
		`CREATE TABLE upgrade_probe(id INTEGER PRIMARY KEY, note TEXT NOT NULL);
		INSERT INTO upgrade_probe VALUES(1, 'one');`,
		`ALTER TABLE upgrade_probe ADD COLUMN extra TEXT NOT NULL DEFAULT 'added';
		UPDATE identity_users SET display_name = display_name || ' twice';`)
	result, err := db.UpgradeWith(ctx, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.From != base || result.To != base+2 || installed(t, db) != base+2 {
		t.Fatalf("%+v", result)
	}
	var note, extra, name string
	if err = db.SQL.QueryRowContext(ctx, `SELECT note, extra FROM upgrade_probe WHERE id=1`).Scan(&note, &extra); err != nil || note != "one" || extra != "added" {
		t.Fatal(note, extra, err)
	}
	if err = db.SQL.QueryRowContext(ctx, `SELECT display_name FROM identity_users WHERE id='persisted'`).Scan(&name); err != nil || name != "Preserved twice" {
		t.Fatal(name, err)
	}
	if db.DAL.Dialect() == dal.DialectSQLite {
		if result.Snapshot == "" {
			t.Fatal("no snapshot before the upgrade")
		}
		snapshot, err := database.Open(ctx, result.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Close()
		if version := installed(t, snapshot); version != base {
			t.Fatalf("snapshot at version %d", version)
		}
	}
	again, err := db.UpgradeWith(ctx, migrations)
	if err != nil || again.From != base+2 || again.To != base+2 || again.Snapshot != "" {
		t.Fatalf("current database changed: %+v %v", again, err)
	}
	if err = db.CheckSchema(ctx); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("test migrations went past this build: %v", err)
	}
}

func TestUpgradeRollsBackEveryStep(t *testing.T) {
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	base := installed(t, db)
	_, err := db.UpgradeWith(ctx, steps(db,
		`CREATE TABLE upgrade_probe(id INTEGER PRIMARY KEY)`,
		`INSERT INTO upgrade_missing VALUES(1)`))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%04d_test.sql", base+2)) {
		t.Fatalf("failing migration not reported: %v", err)
	}
	if version := installed(t, db); version != base {
		t.Fatalf("version moved to %d", version)
	}
	if exists, err := hasTable(ctx, db, "upgrade_probe"); err != nil || exists {
		t.Fatal("earlier step kept", err)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRefusesNewerDatabase(t *testing.T) {
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	if _, err := db.SQL.ExecContext(ctx, fmt.Sprintf(`UPDATE schema_metadata SET version=%d`, dal.SchemaVersion()+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Upgrade(ctx); err == nil || !strings.Contains(err.Error(), "downgrades are unsupported") {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx); err == nil {
		t.Fatal("newer database accepted")
	}
}

// A SQLite CHECK change rebuilds the table, as the migrations README shows,
// while other tables refer to it.
func TestSQLiteMigrationRebuildsReferencedTable(t *testing.T) {
	if daltest.PostgresDSN() != "" {
		t.Skip("SQLite table rebuild")
	}
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	_, err := db.UpgradeWith(ctx, steps(db,
		`CREATE TABLE probe_parent(id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('a')));
		CREATE TABLE probe_child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES probe_parent(id) ON DELETE CASCADE);
		CREATE INDEX probe_parent_kind ON probe_parent(kind);
		INSERT INTO probe_parent VALUES(1, 'a');
		INSERT INTO probe_child VALUES(1, 1);`,
		`CREATE TABLE probe_parent_new(id INTEGER PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('a', 'b')));
		INSERT INTO probe_parent_new(id, kind) SELECT id, kind FROM probe_parent;
		DROP TABLE probe_parent;
		ALTER TABLE probe_parent_new RENAME TO probe_parent;
		CREATE INDEX probe_parent_kind ON probe_parent(kind);`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.ExecContext(ctx, `INSERT INTO probe_parent VALUES(2, 'b')`); err != nil {
		t.Fatal("widened CHECK refused:", err)
	}
	var children int
	if err = db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM probe_child WHERE parent_id=1`).Scan(&children); err != nil || children != 1 {
		t.Fatal(children, err)
	}
	if _, err = db.SQL.ExecContext(ctx, `INSERT INTO probe_child VALUES(2, 99)`); err == nil {
		t.Fatal("foreign keys stayed off")
	}
	if _, err = db.SQL.ExecContext(ctx, `DELETE FROM probe_parent WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM probe_child`).Scan(&children); err != nil || children != 0 {
		t.Fatal("reference lost its table:", children, err)
	}
}

// SQLite migrations run with foreign keys off, so references are checked
// before COMMIT.
func TestSQLiteMigrationChecksReferences(t *testing.T) {
	if daltest.PostgresDSN() != "" {
		t.Skip("PostgreSQL checks references as statements run")
	}
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	base := installed(t, db)
	_, err := db.UpgradeWith(ctx, steps(db,
		`CREATE TABLE probe_parent(id INTEGER PRIMARY KEY);
		CREATE TABLE probe_child(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES probe_parent(id));
		INSERT INTO probe_child VALUES(1, 99);`))
	if err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatal(err)
	}
	if version := installed(t, db); version != base {
		t.Fatalf("version moved to %d", version)
	}
}

// Instances that start together on one PostgreSQL database apply each
// migration once.
func TestPostgresInstancesUpgradeOnce(t *testing.T) {
	if daltest.PostgresDSN() == "" {
		t.Skip("PostgreSQL advisory lock")
	}
	ctx := t.Context()
	db := daltest.EmptyDatabase(t)
	migrations := steps(db,
		`CREATE TABLE upgrade_probe(id SERIAL PRIMARY KEY);
		INSERT INTO upgrade_probe DEFAULT VALUES;`)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance, err := database.OpenPostgres(ctx, db.URL)
			if err != nil {
				errs[i] = err
				return
			}
			defer instance.Close()
			_, errs[i] = instance.UpgradeWith(ctx, migrations)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := db.SQL.QueryRowContext(ctx, `SELECT count(*) FROM upgrade_probe`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal(rows, err)
	}
}

func hasTable(ctx context.Context, db *database.Database, name string) (bool, error) {
	query := `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`
	if db.DAL.Dialect() == dal.DialectPostgres {
		query = `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1`
	}
	var count int
	err := db.SQL.QueryRowContext(ctx, query, name).Scan(&count)
	return count > 0, err
}
