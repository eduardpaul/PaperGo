package daltest

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"papergo/internal/dal"
)

// Row maps columns to values. As a condition, a nil value matches NULL and
// NotNull matches any value.
type Row map[string]any

// NotNull matches a column that holds a value.
var NotNull = notNull{}

type notNull struct{}

// quote quotes a name; a table of another schema is named schema.table.
func quote(name string) string {
	parts := strings.Split(name, ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

// DBOS names a table of the workflow engine's own: in its schema on
// PostgreSQL, beside PaperGo's tables on SQLite.
func DBOS(s *dal.Store, table string) string {
	if s.Dialect() == dal.DialectPostgres {
		return "dbos." + table
	}
	return table
}

// statement runs a statement written with ? placeholders on the store.
func statement(s *dal.Store, query string) string {
	if s.Dialect() != dal.DialectPostgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, c := range query {
		if c == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

func where(match Row) (string, []any) {
	if len(match) == 0 {
		return "", nil
	}
	columns := make([]string, 0, len(match))
	for column := range match {
		columns = append(columns, column)
	}
	slices.Sort(columns)
	parts := []string{}
	args := []any{}
	for _, column := range columns {
		switch v := match[column].(type) {
		case nil:
			parts = append(parts, quote(column)+" IS NULL")
		case notNull:
			parts = append(parts, quote(column)+" IS NOT NULL")
		default:
			parts = append(parts, quote(column)+" = ?")
			args = append(args, v)
		}
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// Count counts a table's rows that match.
func Count(t testing.TB, s *dal.Store, table string, match Row) int {
	t.Helper()
	condition, args := where(match)
	var n int
	if err := s.SQLDB().QueryRowContext(context.Background(), statement(s, "SELECT count(*) FROM "+quote(table)+condition), args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// Values reads a column of a table's rows that match, in column order.
func Values(t testing.TB, s *dal.Store, table, column string, match Row) []any {
	t.Helper()
	condition, args := where(match)
	rows, err := s.SQLDB().QueryContext(context.Background(), statement(s, "SELECT "+quote(column)+" FROM "+quote(table)+condition+" ORDER BY 1"), args...)
	if err != nil {
		t.Fatalf("read %s.%s: %v", table, column, err)
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Insert adds a row, as tests seed states the application never writes.
func Insert(t testing.TB, s *dal.Store, table string, row Row) {
	t.Helper()
	if err := TryInsert(s, table, row); err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

// TryInsert is Insert reporting the error the database raised.
func TryInsert(s *dal.Store, table string, row Row) error {
	columns := make([]string, 0, len(row))
	for column := range row {
		columns = append(columns, column)
	}
	slices.Sort(columns)
	names := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, column := range columns {
		names[i], args[i] = quote(column), row[column]
	}
	marks := strings.TrimPrefix(strings.Repeat(",?", len(columns)), ",")
	_, err := s.SQLDB().ExecContext(context.Background(), statement(s, "INSERT INTO "+quote(table)+" ("+strings.Join(names, ",")+") VALUES ("+marks+")"), args...)
	return err
}

// Update sets columns of a table's rows that match and returns how many
// changed, as tests age or corrupt records.
func Update(t testing.TB, s *dal.Store, table string, set, match Row) int64 {
	t.Helper()
	n, err := TryUpdate(s, table, set, match)
	if err != nil {
		t.Fatalf("update %s: %v", table, err)
	}
	return n
}

// TryUpdate is Update reporting the error the database raised.
func TryUpdate(s *dal.Store, table string, set, match Row) (int64, error) {
	columns := make([]string, 0, len(set))
	for column := range set {
		columns = append(columns, column)
	}
	slices.Sort(columns)
	parts := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, column := range columns {
		parts[i], args[i] = quote(column)+" = ?", set[column]
	}
	condition, conditionArgs := where(match)
	result, err := s.SQLDB().ExecContext(context.Background(), statement(s, "UPDATE "+quote(table)+" SET "+strings.Join(parts, ",")+condition), append(args, conditionArgs...)...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Delete removes a table's rows that match and returns how many it removed.
func Delete(t testing.TB, s *dal.Store, table string, match Row) int64 {
	t.Helper()
	n, err := TryDelete(s, table, match)
	if err != nil {
		t.Fatalf("delete from %s: %v", table, err)
	}
	return n
}

// TryDelete is Delete reporting the error the database raised.
func TryDelete(s *dal.Store, table string, match Row) (int64, error) {
	condition, args := where(match)
	result, err := s.SQLDB().ExecContext(context.Background(), statement(s, "DELETE FROM "+quote(table)+condition), args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Op is a write a rejection applies to.
type Op string

const (
	OnInsert Op = "INSERT"
	OnUpdate Op = "UPDATE"
	OnDelete Op = "DELETE"
)

var rejections atomic.Int64

// literal writes a value into trigger DDL, which takes no parameters.
func literal(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case int, int64:
		return fmt.Sprint(x)
	}
	panic(fmt.Sprintf("daltest: unsupported literal %T", v))
}

// Reject makes the database fail writes of a table's rows that match, as a
// failure in the middle of a transaction would. The row is the one written,
// or the one deleted. It returns a function that stops rejecting; the test's
// cleanup stops it too.
func Reject(t testing.TB, s *dal.Store, table string, op Op, match Row) func() {
	t.Helper()
	name := fmt.Sprintf("daltest_reject_%d", rejections.Add(1))
	row := "NEW"
	if op == OnDelete {
		row = "OLD"
	}
	conditions := []string{"TRUE"}
	for column, v := range match {
		switch v.(type) {
		case nil:
			conditions = append(conditions, row+"."+quote(column)+" IS NULL")
		case notNull:
			conditions = append(conditions, row+"."+quote(column)+" IS NOT NULL")
		default:
			conditions = append(conditions, row+"."+quote(column)+" = "+literal(v))
		}
	}
	condition := strings.Join(conditions, " AND ")
	ctx := context.Background()
	var create, drop []string
	if s.Dialect() == dal.DialectPostgres {
		create = []string{
			`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF ` + condition + ` THEN RAISE EXCEPTION 'rejected by test' USING ERRCODE = 'integrity_constraint_violation'; END IF; RETURN COALESCE(NEW, OLD); END $$`,
			`CREATE TRIGGER ` + name + ` BEFORE ` + string(op) + ` ON ` + quote(table) + ` FOR EACH ROW EXECUTE FUNCTION ` + name + `()`,
		}
		drop = []string{`DROP TRIGGER IF EXISTS ` + name + ` ON ` + quote(table), `DROP FUNCTION IF EXISTS ` + name + `()`}
	} else {
		create = []string{`CREATE TRIGGER ` + name + ` BEFORE ` + string(op) + ` ON ` + quote(table) + ` WHEN ` + condition + ` BEGIN SELECT RAISE(ABORT, 'rejected by test'); END`}
		drop = []string{`DROP TRIGGER IF EXISTS ` + name}
	}
	for _, statement := range create {
		if _, err := s.SQLDB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("reject %s %s: %v", op, table, err)
		}
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		for _, statement := range drop {
			if _, err := s.SQLDB().ExecContext(ctx, statement); err != nil {
				t.Errorf("stop rejecting %s %s: %v", op, table, err)
			}
		}
	}
	t.Cleanup(stop)
	return stop
}

// HideColumn renames a table's column, so statements that read it fail.
func HideColumn(t testing.TB, s *dal.Store, table, column string) {
	t.Helper()
	if _, err := s.SQLDB().ExecContext(context.Background(), "ALTER TABLE "+quote(table)+" RENAME COLUMN "+quote(column)+" TO "+quote("hidden_"+column)); err != nil {
		t.Fatalf("hide %s.%s: %v", table, column, err)
	}
}

// NullColumn replaces a table with a view of its rows that reads NULL in
// column, so reads that expect a value fail to scan it.
func NullColumn(t testing.TB, s *dal.Store, table, column string) {
	t.Helper()
	ctx := context.Background()
	rows, err := s.SQLDB().QueryContext(ctx, "SELECT * FROM "+quote(table)+" WHERE FALSE")
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	columns, err := rows.Columns()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	stored := "stored_" + table
	list := make([]string, len(columns))
	for i, c := range columns {
		list[i] = quote(c)
		if c == column {
			list[i] = "NULL AS " + quote(c)
		}
	}
	for _, statement := range []string{"ALTER TABLE " + quote(table) + " RENAME TO " + quote(stored), "CREATE VIEW " + quote(table) + " AS SELECT " + strings.Join(list, ",") + " FROM " + quote(stored)} {
		if _, err = s.SQLDB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("replace %s: %v", table, err)
		}
	}
}
