// Package postgres runs DAL statements on PostgreSQL with models generated
// from schema/postgres.sql. Concurrent writers are serialized with row locks
// taken in a fixed order: workspace, then the row.
package postgres

import (
	"encoding/json"

	"github.com/stephenafamo/bob"
)

// Queries runs PostgreSQL statements on one connection or transaction.
type Queries struct {
	exec bob.Executor
}

func Bind(exec bob.Executor) Queries {
	return Queries{exec: exec}
}

// emptyChanges is the page of a token whose cached page expired.
var emptyChanges = json.RawMessage(`[]`)
