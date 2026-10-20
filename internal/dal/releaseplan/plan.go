// Package releaseplan generates release SQL with the pinned sqlite3def CLI.
package releaseplan

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	atl "ariga.io/atlas/sql/sqlite"
	"papergo/internal/dal/database"
)

const SQLite3DefVersion = "3.11.26"

// Generate uses complete SQLite snapshots, never a customer's database. Triggers
// are installed separately because sqlite3def cannot parse recursive trigger CTEs.
// Atlas inspects the result only to reject changes sqlite3def silently omits;
// it never generates migration SQL.
func Generate(ctx context.Context, from, to string) ([]string, error) {
	tool := os.Getenv("SQLITE3DEF")
	if tool == "" {
		tool = "sqlite3def"
	}
	version, err := exec.CommandContext(ctx, tool, "--version").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sqlite3def %s is required: %w: %s", SQLite3DefVersion, err, version)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(version)), SQLite3DefVersion+" ") && strings.TrimSpace(string(version)) != SQLite3DefVersion {
		return nil, fmt.Errorf("expected sqlite3def %s, got %s", SQLite3DefVersion, bytes.TrimSpace(version))
	}
	root, err := os.MkdirTemp("", "papergo-release-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	old, err := database.Open(ctx, filepath.Join(root, "old.db"))
	if err != nil {
		return nil, err
	}
	defer old.Close()
	next, err := database.Open(ctx, filepath.Join(root, "new.db"))
	if err != nil {
		return nil, err
	}
	defer next.Close()
	for _, snapshot := range []struct {
		db     *sql.DB
		source string
	}{{old.SQL, from}, {next.SQL, to}} {
		if _, err = snapshot.db.ExecContext(ctx, snapshot.source); err != nil {
			return nil, fmt.Errorf("initialize release snapshot: %w", err)
		}
	}
	driver, err := atl.Open(old.SQL)
	if err != nil {
		return nil, err
	}
	// Seed changes cannot be inferred by a DDL tool. Keep them explicit.
	seeds := func(source string) (string, error) {
		stmts, e := driver.(*atl.Driver).ScanStmts(source)
		if e != nil {
			return "", e
		}
		var inserts []string
		for _, stmt := range stmts {
			upper := strings.ToUpper(strings.TrimSpace(stmt.Text))
			switch {
			case strings.HasPrefix(upper, "INSERT INTO "):
				inserts = append(inserts, stmt.Text)
			case strings.HasPrefix(upper, "CREATE "):
			case strings.TrimSpace(strings.TrimSuffix(upper, ";")) == "PRAGMA FOREIGN_KEYS = ON":
			default:
				return "", fmt.Errorf("unsupported snapshot statement: %s", stmt.Text)
			}
		}
		return strings.Join(inserts, "\n"), nil
	}
	a, err := seeds(from)
	if err != nil {
		return nil, err
	}
	b, err := seeds(to)
	if err != nil {
		return nil, err
	}
	if a != b {
		return nil, fmt.Errorf("seed changes require an explicit release transformation")
	}
	// Use SQLite itself to separate triggers from the DDL; no SQL AST rewriting.
	ddl := func(db *sql.DB) (string, []string, error) {
		rows, e := db.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 WHEN 'view' THEN 2 ELSE 3 END,name`)
		if e != nil {
			return "", nil, e
		}
		defer rows.Close()
		var source strings.Builder
		var triggers []string
		for rows.Next() {
			var kind, name, text string
			if e = rows.Scan(&kind, &name, &text); e != nil {
				return "", nil, e
			}
			if kind == "trigger" {
				triggers = append(triggers, text)
				continue
			}
			source.WriteString(text + ";\n")
		}
		return source.String(), triggers, rows.Err()
	}
	oldDDL, oldTriggers, err := ddl(old.SQL)
	if err != nil {
		return nil, err
	}
	newDDL, newTriggers, err := ddl(next.SQL)
	if err != nil {
		return nil, err
	}
	oldFile, newFile := filepath.Join(root, "previous.sql"), filepath.Join(root, "target.sql")
	if err = os.WriteFile(oldFile, []byte(oldDDL), 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(newFile, []byte(newDDL), 0600); err != nil {
		return nil, err
	}
	// Enable drops so the tool emits the complete desired diff instead of silently
	// skipping removals. Publication verifies that diff before embedding it.
	cmd := exec.CommandContext(ctx, tool, "--dry-run", "--enable-drop", "--file", newFile, oldFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3def: %w: %s", err, stderr.String())
	}
	stmts, err := driver.(*atl.Driver).ScanStmts(string(raw))
	if err != nil {
		return nil, err
	}
	var out []string
	for i, stmt := range stmts {
		text := strings.TrimSpace(stmt.Text)
		upper := strings.TrimSuffix(strings.ToUpper(text), ";")
		if (i == 0 && upper == "BEGIN") || (i == len(stmts)-1 && upper == "COMMIT") {
			continue
		}
		if !strings.HasPrefix(upper, "CREATE ") && !strings.HasPrefix(upper, "ALTER ") && !strings.HasPrefix(upper, "DROP ") && !strings.HasPrefix(upper, "INSERT ") {
			return nil, fmt.Errorf("unexpected sqlite3def statement: %s", text)
		}
		out = append(out, text)
	}
	if len(out) > 0 || strings.Join(oldTriggers, "\n") != strings.Join(newTriggers, "\n") {
		rows, e := old.SQL.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='trigger' ORDER BY name")
		if e != nil {
			return nil, e
		}
		var drops []string
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				rows.Close()
				return nil, e
			}
			drops = append(drops, `DROP TRIGGER "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(drops, out...)
		out = append(out, newTriggers...)
	}
	conn, err := old.SQL.Conn(ctx)
	if err != nil {
		return nil, err
	}
	err = database.ExecuteUpgrade(ctx, conn, out)
	conn.Close()
	if err != nil {
		return nil, fmt.Errorf("verify sqlite3def migration: %w", err)
	}
	actual, err := driver.InspectSchema(ctx, "main", nil)
	if err != nil {
		return nil, err
	}
	targetDriver, err := atl.Open(next.SQL)
	if err != nil {
		return nil, err
	}
	target, err := targetDriver.InspectSchema(ctx, "main", nil)
	if err != nil {
		return nil, err
	}
	// Atlas normalizes implicit UNIQUE index names on the desired side. Inspect
	// both sides through that normalization before comparing two live catalogs.
	if _, err = driver.SchemaDiff(target, actual); err != nil {
		return nil, err
	}
	changes, err := driver.SchemaDiff(actual, target)
	if err != nil {
		return nil, err
	}
	if len(changes) > 0 {
		return nil, fmt.Errorf("sqlite3def did not reproduce the target schema (%d differences); unsupported alterations require an explicit reviewed release transformation", len(changes))
	}
	return out, nil
}
