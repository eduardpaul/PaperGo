// Package sqlite runs DAL statements on SQLite with models generated from
// schema/sqlite.sql. Write transactions start by reserving SQLite's single
// writer, so statements need no row or workspace locks: once a transaction
// writes, no other writer can interleave.
package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"strings"

	"github.com/stephenafamo/bob"
	"modernc.org/sqlite"
)

// SQLite's built-in lower() folds ASCII only; unicode_lower matches Go's
// strings.ToLower so case-insensitive searches work for every script.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unicode_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		default:
			return v, nil
		}
	})
}

// Queries runs SQLite statements on one connection or transaction.
type Queries struct {
	exec bob.Executor
}

func Bind(exec bob.Executor) Queries {
	return Queries{exec: exec}
}

// emptyChanges is the page of a token whose cached page expired.
var emptyChanges = json.RawMessage(`[]`)
