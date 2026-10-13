// Package releaseplan creates upgrade SQL from two complete release snapshots.
// Development keeps one schema file. Only publication creates a history.
package releaseplan

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	atl "ariga.io/atlas/sql/sqlite"
	"papergo/internal/database"
)

type column struct {
	Name, Type           string
	Required, PK, Hidden int
	Default              sql.NullString
}
type object struct{ Kind, Name, SQL string }
type catalog struct {
	Objects []object
	Tables  map[string]object
	Columns map[string][]column
	Kinds   map[string]string
	Seeds   []string
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func inspect(ctx context.Context, db *sql.DB, source string) (catalog, error) {
	c := catalog{Tables: map[string]object{}, Columns: map[string][]column{}, Kinds: map[string]string{}}
	rows, err := db.QueryContext(ctx, "SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY type,name")
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var o object
		if err = rows.Scan(&o.Kind, &o.Name, &o.SQL); err != nil {
			rows.Close()
			return c, err
		}
		c.Objects = append(c.Objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	rows, err = db.QueryContext(ctx, "PRAGMA table_list")
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var schema, name, kind string
		var n, wr, strict int
		if err = rows.Scan(&schema, &name, &kind, &n, &wr, &strict); err != nil {
			rows.Close()
			return c, err
		}
		if schema == "main" {
			c.Kinds[name] = kind
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	for _, o := range c.Objects {
		if o.Kind != "table" || c.Kinds[o.Name] == "shadow" {
			continue
		}
		c.Tables[o.Name] = o
		rows, err = db.QueryContext(ctx, "PRAGMA table_xinfo("+quote(o.Name)+")")
		if err != nil {
			return c, err
		}
		for rows.Next() {
			var cid int
			var col column
			if err = rows.Scan(&cid, &col.Name, &col.Type, &col.Required, &col.Default, &col.PK, &col.Hidden); err != nil {
				rows.Close()
				return c, err
			}
			c.Columns[o.Name] = append(c.Columns[o.Name], col)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return c, err
		}
	}
	drv, _ := atl.Open(db)
	stmts, err := drv.(*atl.Driver).ScanStmts(source)
	if err != nil {
		return c, err
	}
	for _, stmt := range stmts {
		upper := strings.ToUpper(strings.TrimSpace(stmt.Text))
		if strings.HasPrefix(upper, "INSERT INTO ") {
			c.Seeds = append(c.Seeds, stmt.Text)
			continue
		}
		if !strings.HasPrefix(upper, "CREATE ") {
			return c, fmt.Errorf("release snapshot contains unsupported statement: %s", stmt.Text)
		}
	}
	return c, nil
}

func snapshot(ctx context.Context, path, source string) (*database.Database, catalog, error) {
	db, err := database.Open(ctx, path)
	if err != nil {
		return nil, catalog{}, err
	}
	if _, err = db.SQL.ExecContext(ctx, source); err != nil {
		db.Close()
		return nil, catalog{}, err
	}
	c, err := inspect(ctx, db.SQL, source)
	if err != nil {
		db.Close()
		return nil, c, err
	}
	return db, c, nil
}

// Generate refuses column/table removals, type changes, primary key changes and
// new required columns without defaults. Tightened constraints are checked on
// the customer's data by the atomic runtime transaction.
func Generate(ctx context.Context, from, to string) ([]string, error) {
	root, err := os.MkdirTemp("", "papergo-release-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	old, a, err := snapshot(ctx, filepath.Join(root, "old.db"), from)
	if err != nil {
		return nil, err
	}
	defer old.Close()
	next, b, err := snapshot(ctx, filepath.Join(root, "new.db"), to)
	if err != nil {
		return nil, err
	}
	defer next.Close()
	if strings.Join(a.Seeds, "\n") != strings.Join(b.Seeds, "\n") {
		return nil, fmt.Errorf("seed data changes need an explicit release transformation; automatic publication is blocked")
	}
	for name, table := range a.Tables {
		target, ok := b.Tables[name]
		if !ok {
			return nil, fmt.Errorf("automatic release would drop table %s", name)
		}
		if a.Kinds[name] != b.Kinds[name] {
			return nil, fmt.Errorf("table kind changed: %s", name)
		}
		if a.Kinds[name] == "virtual" && table.SQL != target.SQL {
			return nil, fmt.Errorf("virtual table definition changed: %s", name)
		}
		cols := map[string]column{}
		for _, c := range b.Columns[name] {
			cols[c.Name] = c
		}
		for _, c := range a.Columns[name] {
			n, ok := cols[c.Name]
			if !ok {
				return nil, fmt.Errorf("automatic release would drop column %s.%s", name, c.Name)
			}
			if !strings.EqualFold(c.Type, n.Type) || c.PK != n.PK || c.Hidden != n.Hidden {
				return nil, fmt.Errorf("column identity changed: %s.%s", name, c.Name)
			}
			if c.Hidden != 0 && table.SQL != target.SQL {
				return nil, fmt.Errorf("generated column rebuild requires review: %s.%s", name, c.Name)
			}
			delete(cols, c.Name)
		}
		for _, c := range cols {
			if c.Required != 0 && !c.Default.Valid && c.Hidden == 0 {
				return nil, fmt.Errorf("new required column %s.%s has no default", name, c.Name)
			}
		}
	}
	if from == to {
		return nil, nil
	}
	var out []string
	for _, o := range a.Objects {
		if o.Kind == "trigger" || o.Kind == "view" || o.Kind == "index" {
			out = append(out, "DROP "+strings.ToUpper(o.Kind)+" "+quote(o.Name))
		}
	}
	// Save AUTOINCREMENT high-water marks even when the largest row was deleted.
	out = append(out, `CREATE TEMP TABLE "__papergo_release_sequences" (name TEXT,seq INTEGER)`)
	var hasSequence int
	if err = old.SQL.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='sqlite_sequence'").Scan(&hasSequence); err != nil {
		return nil, err
	}
	if hasSequence != 0 {
		out = append(out, `INSERT INTO "__papergo_release_sequences" SELECT name,seq FROM sqlite_sequence`)
	}
	names := make([]string, 0, len(b.Tables))
	for name := range b.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := b.Tables[name]
		prior, exists := a.Tables[name]
		if exists && prior.SQL == target.SQL {
			continue
		}
		if !exists {
			out = append(out, target.SQL)
			continue
		}
		var cols []string
		// Preserve rowids used by external FTS tables and application projections.
		if !strings.Contains(strings.ToUpper(prior.SQL), "WITHOUT ROWID") {
			hasRowID := false
			for _, c := range a.Columns[name] {
				if strings.EqualFold(c.Name, "rowid") {
					hasRowID = true
				}
			}
			if !hasRowID {
				cols = append(cols, "rowid")
			}
		}
		for _, c := range a.Columns[name] {
			if c.Hidden == 0 {
				cols = append(cols, quote(c.Name))
			}
		}
		joined := strings.Join(cols, ",")
		temp := quote("__papergo_release_" + name)
		out = append(out, "CREATE TEMP TABLE "+temp+" AS SELECT "+joined+" FROM "+quote(name), "DROP TABLE "+quote(name), target.SQL, "INSERT INTO "+quote(name)+" ("+joined+") SELECT "+joined+" FROM "+temp, "DROP TABLE "+temp)
	}
	for _, o := range b.Objects {
		if o.Kind == "index" || o.Kind == "view" {
			out = append(out, o.SQL)
		}
	}
	var targetSequence int
	if err = next.SQL.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='sqlite_sequence'").Scan(&targetSequence); err != nil {
		return nil, err
	}
	if targetSequence != 0 {
		out = append(out, `UPDATE sqlite_sequence SET seq=max(seq,coalesce((SELECT seq FROM "__papergo_release_sequences" s WHERE s.name=sqlite_sequence.name),seq))`)
	}
	out = append(out, `DROP TABLE "__papergo_release_sequences"`)
	for _, o := range b.Objects {
		if o.Kind == "trigger" {
			out = append(out, o.SQL)
		}
	}
	for _, name := range names {
		if b.Kinds[name] == "virtual" {
			sql := strings.ToLower(b.Tables[name].SQL)
			if !strings.Contains(sql, "using fts5") || !strings.Contains(sql, "content=") {
				return nil, fmt.Errorf("only external-content FTS5 virtual tables can be rebuilt: %s", name)
			}
			out = append(out, "INSERT INTO "+quote(name)+" ("+quote(name)+") VALUES ('rebuild')")
		}
	}
	// Verify the generated program reconstructs every SQLite definition exactly.
	conn, err := old.SQL.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err = database.ExecuteUpgrade(ctx, conn, out); err != nil {
		return nil, fmt.Errorf("verify generated upgrade: %w", err)
	}
	actual, err := inspect(ctx, old.SQL, to)
	if err != nil {
		return nil, err
	}
	objects := func(c catalog) string {
		var values []string
		for _, o := range c.Objects {
			if c.Kinds[o.Name] != "shadow" {
				values = append(values, o.Kind+"\x00"+o.Name+"\x00"+o.SQL)
			}
		}
		return strings.Join(values, "\n")
	}
	if objects(actual) != objects(b) {
		return nil, fmt.Errorf("generated upgrade does not reproduce target SQLite schema")
	}
	return out, nil
}
