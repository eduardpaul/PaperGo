package releaseplan

import (
	"os"
	"strings"
	"testing"
)

func TestPublishedSnapshotPlan(t *testing.T) {
	from := `CREATE TABLE parent(id TEXT PRIMARY KEY, name TEXT NOT NULL); CREATE TABLE child(id INTEGER PRIMARY KEY AUTOINCREMENT,parent_id TEXT REFERENCES parent(id), value TEXT); CREATE INDEX child_parent ON child(parent_id); CREATE TABLE audit(value TEXT); CREATE TRIGGER child_insert AFTER INSERT ON child BEGIN INSERT INTO audit VALUES(new.value);END;`
	to := strings.Replace(from, "value TEXT); CREATE INDEX", "value TEXT, extra TEXT NOT NULL DEFAULT 'added'); CREATE INDEX", 1)
	statements, err := Generate(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) == 0 {
		t.Fatal("no upgrade generated")
	}
}

func TestRealPaperGoSnapshot(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/20261007000100_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	from := string(raw)
	to := strings.Replace(from, "CREATE TABLE blob_deletions (id TEXT PRIMARY KEY NOT NULL,", "CREATE TABLE blob_deletions (release_note TEXT NOT NULL DEFAULT '', id TEXT PRIMARY KEY NOT NULL,", 1)
	if from == to {
		t.Fatal("fixture replacement failed")
	}
	if _, err = Generate(t.Context(), from, to); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseAmbiguousReleaseChanges(t *testing.T) {
	from := `CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT NOT NULL);`
	for _, to := range []string{
		`CREATE TABLE renamed(id INTEGER PRIMARY KEY, name TEXT NOT NULL);`,
		`CREATE TABLE records(id INTEGER PRIMARY KEY);`,
		`CREATE TABLE records(id INTEGER PRIMARY KEY, name INTEGER NOT NULL);`,
		`CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT NOT NULL, required TEXT NOT NULL);`,
		from + ` INSERT INTO records VALUES(1,'new seed');`,
	} {
		t.Run(to, func(t *testing.T) {
			if _, err := Generate(t.Context(), from, to); err == nil {
				t.Fatal("unsafe release accepted")
			}
		})
	}
}
