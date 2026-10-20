package database

import (
	"context"
	"database/sql"
	"os"
	"sort"

	"papergo/internal/dal"
)

// TableAllocation is the space one table or index occupies. Bytes includes
// the page slack reported as Unused. Names are schema objects, never row values.
type TableAllocation struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Unused int64  `json:"unused"`
}

// Allocation is the main database file, split into allocated pages, freelist
// pages, and unused bytes inside pages that still belong to a table or index.
// DatabaseBytes is the file size when it is read. In WAL mode that file can be
// smaller than PageCount times PageSize until a checkpoint copies the newest pages.
type Allocation struct {
	DatabaseBytes  int64
	PageSize       int
	PageCount      int
	FreelistCount  int
	FreelistBytes  int64
	UnusedBytes    int64
	AccountedBytes int64
	Tables         []TableAllocation
}

// ReadAllocation reads page and dbstat totals. dbstat lists pages owned by a
// table or index and omits freelist pages, so AccountedBytes plus
// FreelistBytes matches PageCount times PageSize. UnusedBytes is slack inside
// pages that still belong to a table or index. On PostgreSQL it reads the
// database size and the pages of the schema's tables and indexes; pages
// have no freelist or slack figures there.
func ReadAllocation(ctx context.Context, db *sql.DB) (Allocation, error) {
	if dal.NewStore(db).Dialect() == dal.DialectPostgres {
		return readPostgresAllocation(ctx, db)
	}
	// Keep page counts and dbstat on the same snapshot while writers continue.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Allocation{}, err
	}
	defer tx.Rollback()
	var out Allocation
	if err := tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&out.PageSize); err != nil {
		return Allocation{}, err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA page_count").Scan(&out.PageCount); err != nil {
		return Allocation{}, err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&out.FreelistCount); err != nil {
		return Allocation{}, err
	}
	out.FreelistBytes = int64(out.FreelistCount) * int64(out.PageSize)
	rows, err := tx.QueryContext(ctx, `SELECT name, SUM(pgsize), SUM(unused) FROM dbstat GROUP BY name`)
	if err != nil {
		return Allocation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var name sql.NullString
		var bytes, unused int64
		if err = rows.Scan(&name, &bytes, &unused); err != nil {
			return Allocation{}, err
		}
		out.AccountedBytes += bytes
		if !name.Valid || name.String == "" {
			continue
		}
		out.UnusedBytes += unused
		out.Tables = append(out.Tables, TableAllocation{Name: name.String, Bytes: bytes, Unused: unused})
	}
	if err = rows.Err(); err != nil {
		return Allocation{}, err
	}
	sort.Slice(out.Tables, func(i, j int) bool { return out.Tables[i].Name < out.Tables[j].Name })
	var file string
	if err = tx.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		return Allocation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Allocation{}, err
	}
	info, err := os.Stat(file)
	if err != nil {
		return Allocation{}, err
	}
	out.DatabaseBytes = info.Size()
	if out.Tables == nil {
		out.Tables = []TableAllocation{}
	}
	return out, nil
}

func readPostgresAllocation(ctx context.Context, db *sql.DB) (Allocation, error) {
	var out Allocation
	if err := db.QueryRowContext(ctx, `SELECT pg_database_size(current_database()), current_setting('block_size')::int`).Scan(&out.DatabaseBytes, &out.PageSize); err != nil {
		return Allocation{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.relname, pg_relation_size(c.oid) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'i') ORDER BY c.relname`)
	if err != nil {
		return Allocation{}, err
	}
	defer rows.Close()
	out.Tables = []TableAllocation{}
	for rows.Next() {
		var table TableAllocation
		if err = rows.Scan(&table.Name, &table.Bytes); err != nil {
			return Allocation{}, err
		}
		out.AccountedBytes += table.Bytes
		out.Tables = append(out.Tables, table)
	}
	out.PageCount = int(out.AccountedBytes / int64(out.PageSize))
	return out, rows.Err()
}
