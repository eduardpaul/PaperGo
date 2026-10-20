package database_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/dal/database"
	"papergo/internal/dal/migrations"
)

func TestReleaseUpgradePreservesDataAndRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	db, err := database.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	from := `CREATE TABLE records(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL); CREATE TABLE empty_seq(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT); CREATE TABLE audit(name TEXT); CREATE TRIGGER record_insert AFTER INSERT ON records BEGIN INSERT INTO audit VALUES(new.name); END;`
	to := strings.Replace(from, "name TEXT NOT NULL);", "name TEXT NOT NULL, added TEXT NOT NULL DEFAULT 'default');", 1)
	to = strings.Replace(to, "CREATE TABLE empty_seq(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT);", "CREATE TABLE empty_seq(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, added TEXT);", 1)
	a := migrations.Release{Version: "v1.0.0", Schema: from, Hash: migrations.Hash(from)}
	b := migrations.Release{Version: "v1.1.0", Schema: to, Hash: migrations.Hash(to)}
	b.Statements = []string{"ALTER TABLE records ADD COLUMN added TEXT NOT NULL DEFAULT 'default'", "ALTER TABLE empty_seq ADD COLUMN added TEXT"}
	if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec("INSERT INTO records VALUES(50,'keep'); INSERT INTO records VALUES(100,'delete'); DELETE FROM records WHERE id=100; INSERT INTO empty_seq VALUES(200,'gone'); DELETE FROM empty_seq;"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(snapshot); err != nil {
		t.Fatal(err)
	}
	var name, added string
	if err = db.SQL.QueryRow("SELECT name,added FROM records WHERE id=50").Scan(&name, &added); err != nil || name != "keep" || added != "default" {
		t.Fatal(name, added, err)
	}
	var audit int
	if err = db.SQL.QueryRow("SELECT count(*) FROM audit").Scan(&audit); err != nil || audit != 2 {
		t.Fatal("copy fired triggers", audit, err)
	}
	if _, err = db.SQL.Exec("INSERT INTO records(name) VALUES('next')"); err != nil {
		t.Fatal(err)
	}
	var id int
	if err = db.SQL.QueryRow("SELECT id FROM records WHERE name='next'").Scan(&id); err != nil || id != 101 {
		t.Fatal("sequence regressed", id, err)
	}
	if _, err = db.SQL.Exec("INSERT INTO empty_seq(name) VALUES('return')"); err != nil {
		t.Fatal(err)
	}
	if err = db.SQL.QueryRow("SELECT id FROM empty_seq").Scan(&id); err != nil || id != 201 {
		t.Fatal("empty sequence regressed", id, err)
	}
	again, err := db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a, b}})
	if err != nil || again != "" {
		t.Fatal("restart must not upgrade", again, err)
	}
	// A failing step must undo earlier DDL and leave the published hash unchanged.
	c := migrations.Release{Version: "v1.2.0", Schema: to + " CREATE TABLE future(id INTEGER);", Statements: []string{"CREATE TABLE future(id INTEGER)", "INSERT INTO absent VALUES(1)"}}
	c.Hash = migrations.Hash(c.Schema)
	if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a, b, c}}); err == nil {
		t.Fatal("bad upgrade accepted")
	}
	var count int
	if err = db.SQL.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='future'").Scan(&count); err != nil || count != 0 {
		t.Fatal("DDL was not rolled back", count, err)
	}
	var version, hash string
	if err = db.SQL.QueryRow("SELECT version,schema_hash FROM papergo_release").Scan(&version, &hash); err != nil || hash != b.Hash || version != b.Version {
		t.Fatal("hash changed after failure", hash, err)
	}
	var fk int
	if err = db.SQL.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatal("foreign keys disabled", fk, err)
	}
	// No version older than the installed schema can be started.
	if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a}}); err == nil {
		t.Fatal("downgrade accepted")
	}
}

func TestUpgradeChecksReferencesBeforeCommit(t *testing.T) {
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.Exec("CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id));"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.SQL.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = database.ExecuteUpgrade(t.Context(), conn, []string{"INSERT INTO child VALUES(999)"}); err == nil {
		t.Fatal("invalid foreign key accepted")
	}
	var count int
	if err = conn.QueryRowContext(t.Context(), "SELECT count(*) FROM child").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestReleaseChainTracksVersionsAndInitializesLatest(t *testing.T) {
	a := migrations.Release{Version: "v1.0.0", Schema: `CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT);`}
	a.Hash = migrations.Hash(a.Schema)
	b := migrations.Release{Version: "v1.1.0", Schema: `CREATE TABLE records(id INTEGER PRIMARY KEY,name TEXT,note TEXT);`, Statements: []string{`ALTER TABLE records ADD COLUMN note TEXT`}}
	b.Hash = migrations.Hash(b.Schema)
	c := migrations.Release{Version: "v1.2.0", Schema: b.Schema, Statements: []string{`UPDATE records SET note='step two'`}}
	c.Hash = b.Hash
	d := migrations.Release{Version: "v1.3.0", Schema: c.Schema + `CREATE INDEX records_note ON records(note);`, Statements: []string{`UPDATE records SET note=note || ' -> step three'`, `CREATE INDEX records_note ON records(note)`}}
	d.Hash = migrations.Hash(d.Schema)
	bundle := migrations.Bundle{Format: 1, Releases: []migrations.Release{a, b, c, d}}
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip releases", true: "fresh latest"}[fresh], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			db, err := database.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if !fresh {
				if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a}}); err != nil {
					t.Fatal(err)
				}
				if _, err = db.SQL.Exec(`INSERT INTO records VALUES(1,'keep')`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.ApplyRelease(t.Context(), path, bundle); err != nil {
				t.Fatal(err)
			}
			var version, hash string
			if err = db.SQL.QueryRow(`SELECT version,schema_hash FROM papergo_release`).Scan(&version, &hash); err != nil || version != d.Version || hash != d.Hash {
				t.Fatal(version, hash, err)
			}
			if !fresh {
				var note string
				if err = db.SQL.QueryRow(`SELECT note FROM records WHERE id=1`).Scan(&note); err != nil || note != "step two -> step three" {
					t.Fatal(note, err)
				}
			}
			if snapshot, err := db.ApplyRelease(t.Context(), path, bundle); err != nil || snapshot != "" {
				t.Fatal(snapshot, err)
			}
			// A downgrade is rejected even when the requested and installed hashes match.
			same := d
			same.Version = "v1.4.0"
			same.Statements = nil
			bundle.Releases = append(bundle.Releases, same)
			if _, err = db.ApplyRelease(t.Context(), path, bundle); err != nil {
				t.Fatal(err)
			}
			bundle.Releases = bundle.Releases[:len(bundle.Releases)-1]
			if _, err = db.ApplyRelease(t.Context(), path, bundle); err == nil {
				t.Fatal("same-schema downgrade accepted")
			}
		})
	}
}

func TestReleaseChainRollsBackEarlierSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := migrations.Release{Version: "v1.0.0", Schema: `CREATE TABLE records(id INTEGER PRIMARY KEY);`}
	a.Hash = migrations.Hash(a.Schema)
	b := migrations.Release{Version: "v1.1.0", Schema: a.Schema + `CREATE TABLE second(id INTEGER);`, Statements: []string{`CREATE TABLE second(id INTEGER)`}}
	b.Hash = migrations.Hash(b.Schema)
	c := migrations.Release{Version: "v1.2.0", Schema: b.Schema + `CREATE TABLE third(id INTEGER);`, Statements: []string{`INSERT INTO missing VALUES(1)`}}
	c.Hash = migrations.Hash(c.Schema)
	if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ApplyRelease(t.Context(), path, migrations.Bundle{Format: 1, Releases: []migrations.Release{a, b, c}}); err == nil {
		t.Fatal("broken chain accepted")
	}
	var count int
	if err = db.SQL.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='second'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("earlier step committed", count, err)
	}
	var version string
	if err = db.SQL.QueryRow(`SELECT version FROM papergo_release`).Scan(&version); err != nil || version != a.Version {
		t.Fatal(version, err)
	}
}
