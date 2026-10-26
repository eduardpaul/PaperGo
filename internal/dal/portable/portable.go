// Package portable reads and writes the rows of portable packages: the
// tables a package carries, exported and imported as rows of plain values
// that read the same on every engine. Statements are built as text for the
// store's engine; table and column names come from Tables and the schema.
package portable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stephenafamo/bob"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/dialect"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

// Tables lists the tables a package carries, in the order an import writes
// them. Text read from files travels with their bytes: reading it again can
// mean OCR. Credentials, audit, cursors and execution state belong to this
// installation and are deliberately absent.
var Tables = []string{
	"identity_users", "identity_groups", "identity_roles", "identity_memberships", "identity_assignments", "identity_preferences",
	"term_groups", "term_sets", "terms", "relationship_types", "field_definitions", "content_types",
	"resources", "schema_revisions", "schema_templates", "list_views", "smart_folders",
	"blobs", "blob_previews", "item_revisions", "integration_revision_data", "integration_items", "item_histories", "publications", "item_surfaces", "business_keys", "grants", "relationships",
	"workflows", "workflow_versions", "workflow_triggers", "group_inboxes",
}

// DataTables hold content. A configuration package carries none of their rows.
var DataTables = []string{"blobs", "blob_previews", "item_revisions", "integration_revision_data", "integration_items", "item_histories", "publications", "item_surfaces", "business_keys", "relationships"}

// Kind is how a column's values travel.
type Kind uint8

const (
	// Text travels as a string.
	Text Kind = iota
	// Integer travels as an int64.
	Integer
	// Real travels as a float64.
	Real
	// Boolean travels as a bool.
	Boolean
	// Time travels as RFC 3339 text in UTC.
	Time
	// JSON travels as the document's canonical text.
	JSON
)

// Column is a column a package carries.
type Column struct {
	Name string
	Kind Kind
}

// Row maps columns to package values. As a match, a nil value matches NULL.
type Row map[string]any

// Scope selects the rows an export reads.
type Scope struct {
	// WorkspaceID selects one workspace's rows; empty selects the
	// organization's.
	WorkspaceID string
	// Data selects content as well as configuration.
	Data bool
}

// Repo reads and writes package rows on one connection or transaction.
type Repo struct {
	exec bob.Executor
	d    dialect.Dialect
}

func NewRepo(exec bob.Executor, d dialect.Dialect) *Repo {
	return &Repo{exec: exec, d: d}
}

// one scans the single row of a query.
func (r *Repo) one(ctx context.Context, dest any, query string, args ...any) error {
	rows, err := r.exec.QueryContext(ctx, query, args...)
	if err != nil {
		return dberr.Translate(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return dberr.Translate(err)
		}
		return dberr.ErrNotFound
	}
	if err := rows.Scan(dest); err != nil {
		return dberr.Translate(err)
	}
	return dberr.Translate(rows.Close())
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// columns caches each engine's package columns; the schema is fixed.
var columns sync.Map

type columnsKey struct {
	postgres bool
	table    string
}

var jsonCheck = regexp.MustCompile(`json_valid\(\s*"?(\w+)"?`)

// Columns lists the columns a package carries of a table, in schema order.
// Password hashes are credentials and never travel.
func (r *Repo) Columns(ctx context.Context, table string) ([]Column, error) {
	if !slices.Contains(Tables, table) {
		return nil, fmt.Errorf("portable: %s is not a package table", table)
	}
	key := columnsKey{r.d.Postgres, table}
	if cached, ok := columns.Load(key); ok {
		return cached.([]Column), nil
	}
	var out []Column
	var err error
	if r.d.Postgres {
		out, err = r.postgresColumns(ctx, table)
	} else {
		out, err = r.sqliteColumns(ctx, table)
	}
	if err != nil {
		return nil, err
	}
	out = slices.DeleteFunc(out, func(c Column) bool { return c.Name == "password_hash" })
	columns.Store(key, out)
	return out, nil
}

func (r *Repo) postgresColumns(ctx context.Context, table string) ([]Column, error) {
	rows, err := r.exec.QueryContext(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, dberr.Translate(err)
		}
		kind := Text
		switch typ {
		case "boolean":
			kind = Boolean
		case "timestamp with time zone", "timestamp without time zone", "date":
			kind = Time
		case "jsonb", "json":
			kind = JSON
		case "bigint", "integer", "smallint":
			kind = Integer
		case "numeric", "double precision", "real":
			kind = Real
		}
		out = append(out, Column{Name: name, Kind: kind})
	}
	return out, dberr.Translate(rows.Err())
}

// sqliteColumns reads declared types. JSON columns are TEXT, known by the
// json_valid check the table places on them.
func (r *Repo) sqliteColumns(ctx context.Context, table string) ([]Column, error) {
	var ddl string
	if err := r.one(ctx, &ddl, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table); err != nil {
		return nil, err
	}
	documents := map[string]bool{}
	for _, m := range jsonCheck.FindAllStringSubmatch(ddl, -1) {
		documents[m[1]] = true
	}
	rows, err := r.exec.QueryContext(ctx, "PRAGMA table_info("+quote(table)+")")
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	var out []Column
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaults any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaults, &pk); err != nil {
			return nil, dberr.Translate(err)
		}
		kind := Text
		switch typ = strings.ToUpper(typ); {
		case documents[name]:
			kind = JSON
		case typ == "BOOLEAN":
			kind = Boolean
		case strings.Contains(typ, "DATE") || strings.Contains(typ, "TIME"):
			kind = Time
		case strings.Contains(typ, "INT"):
			kind = Integer
		case typ == "NUMERIC" || typ == "REAL" || strings.Contains(typ, "DOUBLE"):
			kind = Real
		}
		out = append(out, Column{Name: name, Kind: kind})
	}
	return out, dberr.Translate(rows.Err())
}

// column finds a table's column by name.
func column(cols []Column, name string) (Column, error) {
	for _, c := range cols {
		if c.Name == name {
			return c, nil
		}
	}
	return Column{}, fmt.Errorf("portable: unknown column %s", name)
}

// timeLayouts are the forms timestamps take in packages and SQLite text.
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02"}

func parseTime(text string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("portable: invalid timestamp %q", text)
}

// Normalize converts a value of a column, read from a package or the
// database, to its package form.
func Normalize(c Column, v any) (any, error) {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if v == nil {
		return nil, nil
	}
	invalid := fmt.Errorf("portable: invalid value for %s", c.Name)
	switch c.Kind {
	case Boolean:
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		}
		return nil, invalid
	case Integer:
		switch x := v.(type) {
		case int64:
			return x, nil
		case int32:
			return int64(x), nil
		case int:
			return int64(x), nil
		}
		return nil, invalid
	case Real:
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
		return nil, invalid
	case Time:
		switch x := v.(type) {
		case time.Time:
			return x.UTC().Format(time.RFC3339Nano), nil
		case string:
			t, err := parseTime(x)
			if err != nil {
				return nil, err
			}
			return t.Format(time.RFC3339Nano), nil
		}
		return nil, invalid
	}
	switch x := v.(type) {
	case string:
		if c.Kind == JSON {
			return document(x)
		}
		return x, nil
	case map[string]any, []any:
		if c.Kind == JSON {
			return encode(x)
		}
	}
	return nil, invalid
}

// document is the canonical text of a JSON document: compact, with sorted
// keys and numbers as written, so documents compare equal as text whichever
// engine stored them.
func document(text string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var v any
	if err := decoder.Decode(&v); err != nil {
		return "", fmt.Errorf("portable: invalid JSON document: %w", err)
	}
	if decoder.More() {
		return "", errors.New("portable: invalid JSON document")
	}
	return encode(v)
}

func encode(v any) (string, error) {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// argument converts a package value to a statement argument.
func argument(c Column, v any) (any, error) {
	v, err := Normalize(c, v)
	if err != nil || v == nil {
		return v, err
	}
	if c.Kind == Time {
		return parseTime(v.(string))
	}
	return v, nil
}

// where is the condition matching every column of match.
func (r *Repo) where(cols []Column, match Row) (string, []any, error) {
	names := make([]string, 0, len(match))
	for name := range match {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := []string{"TRUE"}
	args := []any{}
	for _, name := range names {
		c, err := column(cols, name)
		if err != nil {
			return "", nil, err
		}
		v, err := argument(c, match[name])
		if err != nil {
			return "", nil, err
		}
		if v == nil {
			parts = append(parts, quote(name)+" IS NULL")
			continue
		}
		parts = append(parts, quote(name)+" = ?")
		args = append(args, v)
	}
	return strings.Join(parts, " AND "), args, nil
}

// read runs a query of a table's package columns and returns the rows.
func (r *Repo) read(ctx context.Context, cols []Column, query string, args ...any) ([][]any, error) {
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := [][]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, dberr.Translate(err)
		}
		for i, c := range cols {
			if values[i], err = Normalize(c, values[i]); err != nil {
				return nil, err
			}
		}
		out = append(out, values)
	}
	return out, dberr.Translate(rows.Err())
}

func list(cols []Column) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quote(c.Name)
	}
	return strings.Join(names, ",")
}

// Export reads the rows of a table that scope selects, in ID order, up to
// limit rows when limit is positive.
func (r *Repo) Export(ctx context.Context, table string, scope Scope, limit int) ([]Column, [][]any, error) {
	cols, err := r.Columns(ctx, table)
	if err != nil {
		return nil, nil, err
	}
	condition, args := exportScope(table, scope)
	query := "SELECT " + list(cols) + " FROM " + quote(table) + " WHERE " + condition + " ORDER BY id"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := r.read(ctx, cols, query, args...)
	return cols, rows, err
}

// exportScope is the condition selecting a table's rows in scope.
func exportScope(table string, scope Scope) (string, []any) {
	collections := []any{string(refs.KindWorkspace), string(refs.KindList), string(refs.KindLibrary)}
	if !scope.Data && slices.Contains(DataTables, table) {
		return "FALSE", nil
	}
	ws := scope.WorkspaceID
	if ws == "" {
		switch {
		case table == "resources" && !scope.Data:
			return "kind IN (?,?,?)", collections
		case table == "grants" && !scope.Data:
			return "resource_id IN (SELECT id FROM resources WHERE kind IN (?,?,?))", collections
		case table == "schema_revisions" && !scope.Data:
			return "id IN (SELECT schema_head_id FROM resources)", nil
		}
		return "TRUE", nil
	}
	if slices.Contains([]string{"identity_roles", "identity_assignments", "identity_preferences"}, table) {
		return "FALSE", nil
	}
	resourcesIn, resourceArgs := "SELECT id FROM resources WHERE workspace_id=?", []any{ws}
	if !scope.Data {
		resourcesIn += " AND kind IN (?,?,?)"
		resourceArgs = append(resourceArgs, collections...)
	}
	switch table {
	case "resources":
		return "id IN (" + resourcesIn + ")", resourceArgs
	case "list_views":
		return "container_id IN (" + resourcesIn + ")", resourceArgs
	case "schema_revisions":
		if !scope.Data {
			return "container_id IN (" + resourcesIn + ") AND id IN (SELECT schema_head_id FROM resources)", resourceArgs
		}
		return "container_id IN (" + resourcesIn + ")", resourceArgs
	case "schema_templates", "smart_folders", "workflows", "workflow_triggers":
		return "workspace_id=?", []any{ws}
	case "blobs", "item_revisions", "integration_items", "item_histories", "publications", "item_surfaces", "business_keys":
		return "item_id IN (" + resourcesIn + ")", resourceArgs
	case "grants":
		return "resource_id IN (" + resourcesIn + ")", resourceArgs
	case "group_inboxes":
		return "library_id IN (" + resourcesIn + ")", resourceArgs
	case "workflow_versions":
		return "workflow_id IN (SELECT id FROM workflows WHERE workspace_id=?)", []any{ws}
	case "integration_revision_data":
		return "revision_id IN (SELECT r.id FROM item_revisions r JOIN resources c ON c.id=r.container_id WHERE c.workspace_id=?)", []any{ws}
	case "blob_previews":
		return "sha256 IN (SELECT b.sha256 FROM blobs b JOIN resources r ON r.id=b.item_id WHERE r.workspace_id=?)", []any{ws}
	case "relationships":
		return "source_id IN (" + resourcesIn + ") OR target_id IN (" + resourcesIn + ")", append(slices.Clone(resourceArgs), resourceArgs...)
	}
	return "TRUE", nil
}

// Find reads the package columns of the table row that matches, or
// dberr.ErrNotFound.
func (r *Repo) Find(ctx context.Context, table string, match Row) (Row, error) {
	cols, err := r.Columns(ctx, table)
	if err != nil {
		return nil, err
	}
	condition, args, err := r.where(cols, match)
	if err != nil {
		return nil, err
	}
	rows, err := r.read(ctx, cols, "SELECT "+list(cols)+" FROM "+quote(table)+" WHERE "+condition+" ORDER BY id LIMIT 1", args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, dberr.ErrNotFound
	}
	out := Row{}
	for i, c := range cols {
		out[c.Name] = rows[0][i]
	}
	return out, nil
}

// Count counts the table rows that match.
func (r *Repo) Count(ctx context.Context, table string, match Row) (int, error) {
	cols, err := r.Columns(ctx, table)
	if err != nil {
		return 0, err
	}
	condition, args, err := r.where(cols, match)
	if err != nil {
		return 0, err
	}
	var n int
	err = r.one(ctx, &n, r.d.Rebind("SELECT count(*) FROM "+quote(table)+" WHERE "+condition), args...)
	return n, err
}

// Insert adds a row of package values; columns it leaves out are NULL.
func (r *Repo) Insert(ctx context.Context, table string, row Row) error {
	cols, err := r.Columns(ctx, table)
	if err != nil {
		return err
	}
	args := make([]any, len(cols))
	for i, c := range cols {
		if args[i], err = argument(c, row[c.Name]); err != nil {
			return err
		}
	}
	marks := strings.TrimPrefix(strings.Repeat(",?", len(cols)), ",")
	_, err = r.exec.ExecContext(ctx, r.d.Rebind("INSERT INTO "+quote(table)+" ("+list(cols)+") VALUES ("+marks+")"), args...)
	return dberr.Translate(err)
}

// Update sets columns of the table rows that match.
func (r *Repo) Update(ctx context.Context, table string, match, set Row) error {
	cols, err := r.Columns(ctx, table)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, len(names))
	args := make([]any, len(names))
	for i, name := range names {
		c, err := column(cols, name)
		if err != nil {
			return err
		}
		if args[i], err = argument(c, set[name]); err != nil {
			return err
		}
		parts[i] = quote(name) + " = ?"
	}
	condition, conditionArgs, err := r.where(cols, match)
	if err != nil {
		return err
	}
	_, err = r.exec.ExecContext(ctx, r.d.Rebind("UPDATE "+quote(table)+" SET "+strings.Join(parts, ",")+" WHERE "+condition), append(args, conditionArgs...)...)
	return dberr.Translate(err)
}

// MergeUser updates an existing account from a package row: profile,
// status and deletion. Credentials belong to this installation; only an
// imported deletion clears the account's password.
func (r *Repo) MergeUser(ctx context.Context, row Row) error {
	cols, err := r.Columns(ctx, "identity_users")
	if err != nil {
		return err
	}
	args := []any{}
	for _, name := range []string{"active", "display_name", "email", "deleted_at", "id"} {
		c, err := column(cols, name)
		if err != nil {
			return err
		}
		v, err := argument(c, row[name])
		if err != nil {
			return err
		}
		args = append(args, v)
	}
	password := ""
	if row["deleted_at"] != nil {
		password = ", password_hash=''"
	}
	_, err = r.exec.ExecContext(ctx, r.d.Rebind(`UPDATE identity_users SET active=?, display_name=?, email=?, deleted_at=?`+password+` WHERE id=?`), args...)
	return dberr.Translate(err)
}

// DeferReferences defers foreign key checks to the end of the transaction,
// so an import can write rows that refer to rows it writes later.
func (r *Repo) DeferReferences(ctx context.Context) error {
	statement := "PRAGMA defer_foreign_keys=ON"
	if r.d.Postgres {
		statement = "SET CONSTRAINTS ALL DEFERRED"
	}
	_, err := r.exec.ExecContext(ctx, statement)
	return dberr.Translate(err)
}

// Unresolved reports whether a deferred foreign key refers to a missing row.
func (r *Repo) Unresolved(ctx context.Context) (bool, error) {
	if !r.d.Postgres {
		rows, err := r.exec.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return false, dberr.Translate(err)
		}
		defer rows.Close()
		found := rows.Next()
		return found, dberr.Translate(rows.Err())
	}
	// Checking now raises the violation; the savepoint keeps the transaction
	// usable and the constraints deferred.
	if _, err := r.exec.ExecContext(ctx, "SAVEPOINT portable_references"); err != nil {
		return false, dberr.Translate(err)
	}
	_, check := r.exec.ExecContext(ctx, "SET CONSTRAINTS ALL IMMEDIATE")
	if _, err := r.exec.ExecContext(ctx, "ROLLBACK TO SAVEPOINT portable_references"); err != nil {
		return false, dberr.Translate(err)
	}
	if _, err := r.exec.ExecContext(ctx, "RELEASE SAVEPOINT portable_references"); err != nil {
		return false, dberr.Translate(err)
	}
	check = dberr.Translate(check)
	if errors.Is(check, dberr.ErrForeignKeyViolation) {
		return true, nil
	}
	return false, check
}

// IncompleteHomes reports whether a personal Home is deleted, belongs to a
// missing or deleted account, lacks its owner's manage grant or lacks a
// live Documents library or Inbox.
func (r *Repo) IncompleteHomes(ctx context.Context) (bool, error) {
	var found bool
	err := r.one(ctx, &found, r.d.Rebind(`SELECT EXISTS(SELECT 1 FROM resources w WHERE w.personal_owner_id IS NOT NULL AND (
w.deleted_at IS NOT NULL
OR NOT EXISTS(SELECT 1 FROM identity_users u WHERE u.id = w.personal_owner_id AND u.deleted_at IS NULL)
OR NOT EXISTS(SELECT 1 FROM grants g WHERE g.resource_id = w.id AND g.principal_type = ? AND g.principal_id = w.personal_owner_id AND g.action = ?)
OR NOT EXISTS(SELECT 1 FROM resources l WHERE l.workspace_id = w.id AND l.system_key = ? AND l.deleted_at IS NULL)
OR NOT EXISTS(SELECT 1 FROM resources l WHERE l.workspace_id = w.id AND l.system_key = ? AND l.deleted_at IS NULL)))`),
		string(grants.PrincipalUser), string(grants.ActionManage), string(resources.SystemDocuments), string(resources.SystemInbox))
	return found, err
}
