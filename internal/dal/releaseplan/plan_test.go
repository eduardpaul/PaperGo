package releaseplan

import (
	"os"
	"papergo/internal/dal/database"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedSnapshotPlan(t *testing.T) {
	from := `CREATE TABLE parent(id TEXT PRIMARY KEY, name TEXT NOT NULL); CREATE TABLE child(id INTEGER PRIMARY KEY AUTOINCREMENT,parent_id TEXT REFERENCES parent(id), payload TEXT); CREATE INDEX child_parent ON child(parent_id); CREATE TABLE audit(payload TEXT); CREATE TRIGGER child_insert AFTER INSERT ON child BEGIN INSERT INTO audit VALUES(new.payload);END;`
	to := strings.Replace(from, "payload TEXT); CREATE INDEX", "payload TEXT, extra TEXT NOT NULL DEFAULT 'added'); CREATE INDEX", 1)
	statements, err := Generate(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) == 0 {
		t.Fatal("no upgrade generated")
	}
}

func TestRealPaperGoSnapshot(t *testing.T) {
	raw, err := os.ReadFile("../schema/sqlite.sql")
	if err != nil {
		t.Fatal(err)
	}
	from := string(raw)
	to := strings.Replace(from, "CREATE TABLE blob_deletions (", "CREATE TABLE blob_deletions (release_note TEXT NOT NULL DEFAULT '',", 1)
	if from == to {
		t.Fatal("fixture replacement failed")
	}
	if _, err = Generate(t.Context(), from, to); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsupportedAlterationsAndSeedChanges(t *testing.T) {
	from := `CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT NOT NULL);`
	for _, to := range []string{
		`CREATE TABLE records(id INTEGER PRIMARY KEY, name INTEGER NOT NULL);`,
		`CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT);`,
		`CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT NOT NULL CHECK(length(name)>2));`,
		from + ` INSERT INTO records VALUES(1,'new seed');`,
	} {
		t.Run(to, func(t *testing.T) {
			if _, err := Generate(t.Context(), from, to); err == nil {
				t.Fatal("unsupported release accepted")
			}
		})
	}
}

func TestNoChangeAndMissingTool(t *testing.T) {
	source := `CREATE TABLE records(id INTEGER PRIMARY KEY);`
	steps, err := Generate(t.Context(), source, source)
	if err != nil || len(steps) != 0 {
		t.Fatal(steps, err)
	}
	t.Setenv("SQLITE3DEF", filepath.Join(t.TempDir(), "missing-sqlite3def"))
	if _, err = Generate(t.Context(), source, source); err == nil {
		t.Fatal("missing build tool accepted")
	}
}

func TestDDLAndRecursiveTriggersPreserveRows(t *testing.T) {
	from := `CREATE TABLE records(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT); CREATE TABLE audit(message TEXT); CREATE INDEX records_name ON records(name); CREATE TABLE removed(id INTEGER); CREATE TRIGGER record_insert AFTER INSERT ON records BEGIN INSERT INTO audit VALUES('old'); END;`
	to := `CREATE TABLE records(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT,note TEXT DEFAULT 'added'); CREATE TABLE audit(message TEXT); CREATE INDEX records_note ON records(note); CREATE TRIGGER record_insert AFTER INSERT ON records BEGIN INSERT INTO audit SELECT name FROM records WHERE id IN (WITH RECURSIVE ids(id) AS (SELECT new.id UNION ALL SELECT id-1 FROM ids WHERE id>1) SELECT id FROM ids); END;`
	steps, err := Generate(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.Exec(from + `INSERT INTO records VALUES(1,'keep');`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.SQL.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = database.ExecuteUpgrade(t.Context(), conn, steps)
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec(`INSERT INTO records(name) VALUES('next')`); err != nil {
		t.Fatal(err)
	}
	var note string
	if err = db.SQL.QueryRow(`SELECT note FROM records WHERE name='keep'`).Scan(&note); err != nil || note != "added" {
		t.Fatal(note, err)
	}
	var count int
	if err = db.SQL.QueryRow(`SELECT count(*) FROM audit`).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err = db.SQL.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('removed','records_name')`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
