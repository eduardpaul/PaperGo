package dal

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Hand-written upgrades, one file per engine for each schema change:
// migrations/<engine>/NNNN_name.sql. Migration N takes a database from
// schema version N-1 to N. The schema masters always describe the latest
// version, which a new database gets directly.
//
//go:embed migrations/sqlite migrations/postgres
var migrationFiles embed.FS

// Migration is one hand-written upgrade step of an engine.
type Migration struct {
	Version int
	Name    string // file name, such as 0001_add_notes.sql
	SQL     string
}

var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

var migrationSets = func() map[Dialect][]Migration {
	sets, err := loadMigrations(migrationFiles)
	if err != nil {
		panic(err)
	}
	return sets
}()

// Migrations returns the engine's upgrade steps in order.
func Migrations(d Dialect) []Migration { return migrationSets[d] }

// SchemaVersion is the version the schema masters describe: the number of
// the last migration.
func SchemaVersion() int { return len(migrationSets[DialectSQLite]) }

// loadMigrations reads migrations/<engine>/. Each engine numbers its files
// 0001, 0002, … without gaps, and both engines have the same file names, so
// a schema version means the same schema on either.
func loadMigrations(fsys fs.FS) (map[Dialect][]Migration, error) {
	sets := map[Dialect][]Migration{}
	for _, d := range []Dialect{DialectSQLite, DialectPostgres} {
		dir := path.Join("migrations", string(d))
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if name == "README.md" {
				continue
			}
			match := migrationName.FindStringSubmatch(name)
			if match == nil || entry.IsDir() {
				return nil, fmt.Errorf("migration %s/%s: name it NNNN_description.sql", dir, name)
			}
			version, _ := strconv.Atoi(match[1])
			if want := len(sets[d]) + 1; version != want {
				return nil, fmt.Errorf("migration %s/%s: expected number %04d", dir, name, want)
			}
			raw, err := fs.ReadFile(fsys, path.Join(dir, name))
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(string(raw)) == "" {
				return nil, fmt.Errorf("migration %s/%s is empty", dir, name)
			}
			sets[d] = append(sets[d], Migration{Version: version, Name: name, SQL: string(raw)})
		}
	}
	names := func(d Dialect) []string {
		var out []string
		for _, m := range sets[d] {
			out = append(out, m.Name)
		}
		return out
	}
	if !slices.Equal(names(DialectSQLite), names(DialectPostgres)) {
		return nil, fmt.Errorf("migrations/sqlite and migrations/postgres must contain the same files: %v and %v", names(DialectSQLite), names(DialectPostgres))
	}
	return sets, nil
}
