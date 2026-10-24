package dal

import (
	"testing"
	"testing/fstest"
)

func TestLoadMigrations(t *testing.T) {
	file := func(sql string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(sql)} }
	valid := fstest.MapFS{
		"migrations/sqlite/README.md":               file("notes"),
		"migrations/sqlite/0001_add_notes.sql":      file("ALTER TABLE a ADD COLUMN b TEXT;"),
		"migrations/sqlite/0002_widen_layout.sql":   file("SELECT 1;"),
		"migrations/postgres/README.md":             file("notes"),
		"migrations/postgres/0001_add_notes.sql":    file("ALTER TABLE a ADD COLUMN b TEXT;"),
		"migrations/postgres/0002_widen_layout.sql": file("SELECT 1;"),
	}
	sets, err := loadMigrations(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got := sets[DialectPostgres]; len(got) != 2 || got[1].Version != 2 || got[1].Name != "0002_widen_layout.sql" || got[1].SQL != "SELECT 1;" {
		t.Fatalf("%+v", got)
	}
	for name, change := range map[string]func(fstest.MapFS){
		"gap": func(fs fstest.MapFS) {
			fs["migrations/sqlite/0004_late.sql"] = file("SELECT 1;")
			fs["migrations/postgres/0004_late.sql"] = file("SELECT 1;")
		},
		"name":   func(fs fstest.MapFS) { fs["migrations/sqlite/3_bad.sql"] = file("SELECT 1;") },
		"empty":  func(fs fstest.MapFS) { fs["migrations/sqlite/0002_widen_layout.sql"] = file(" \n") },
		"engine": func(fs fstest.MapFS) { delete(fs, "migrations/postgres/0002_widen_layout.sql") },
		"renamed": func(fs fstest.MapFS) {
			delete(fs, "migrations/postgres/0002_widen_layout.sql")
			fs["migrations/postgres/0002_other.sql"] = file("SELECT 1;")
		},
	} {
		t.Run(name, func(t *testing.T) {
			fs := fstest.MapFS{}
			for k, v := range valid {
				fs[k] = v
			}
			change(fs)
			if _, err := loadMigrations(fs); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// The embedded migrations load, and a new database is at their last version.
func TestEmbeddedMigrations(t *testing.T) {
	if _, err := loadMigrations(migrationFiles); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Dialect{DialectSQLite, DialectPostgres} {
		if len(Migrations(d)) != SchemaVersion() {
			t.Fatal(d)
		}
	}
}
