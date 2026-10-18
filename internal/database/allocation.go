package database

import (
	"context"
	"database/sql"
	"os"
	"sort"
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
// pages that still belong to a table or index.
func ReadAllocation(ctx context.Context, db *sql.DB) (Allocation, error) {
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
