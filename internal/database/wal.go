package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
)

// Checkpoint is one passive observation of the main database journal.
// Busy, Log and Checkpointed are the three columns of wal_checkpoint(PASSIVE).
// Incomplete is set when that checkpoint left frames in the journal.
// JournalBytes is the size of the -wal file.
type Checkpoint struct {
	Busy           int    `json:"busy"`
	Log            int    `json:"log"`
	Checkpointed   int    `json:"checkpointed"`
	Incomplete     bool   `json:"incomplete"`
	JournalBytes   int64  `json:"journal_bytes"`
	JournalLimit   int64  `json:"journal_size_limit"`
	AutoCheckpoint int    `json:"wal_autocheckpoint"`
	JournalMode    string `json:"journal_mode"`
	Synchronous    string `json:"synchronous"`
}

// ObserveCheckpoint runs PRAGMA wal_checkpoint(PASSIVE) and reads the journal
// file size. Passive mode copies committed frames without waiting for readers,
// and it leaves the -wal file in place. Committed frames stay in the journal
// until a later checkpoint copies them.
func ObserveCheckpoint(ctx context.Context, db *sql.DB) (Checkpoint, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return Checkpoint{}, err
	}
	defer conn.Close()
	var out Checkpoint
	if err = conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&out.Busy, &out.Log, &out.Checkpointed); err != nil {
		return Checkpoint{}, err
	}
	out.Incomplete = out.Busy != 0 || (out.Log >= 0 && out.Checkpointed >= 0 && out.Log > out.Checkpointed)
	if err = conn.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&out.AutoCheckpoint); err != nil {
		return Checkpoint{}, err
	}
	if err = conn.QueryRowContext(ctx, "PRAGMA journal_size_limit").Scan(&out.JournalLimit); err != nil {
		return Checkpoint{}, err
	}
	if err = conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&out.JournalMode); err != nil {
		return Checkpoint{}, err
	}
	var synchronous int
	if err = conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return Checkpoint{}, err
	}
	out.Synchronous = synchronousName(synchronous)
	var file string
	if err = conn.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file); err != nil {
		return Checkpoint{}, err
	}
	info, err := os.Stat(file + "-wal")
	if err == nil {
		out.JournalBytes = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, err
	}
	return out, nil
}

func synchronousName(v int) string {
	switch v {
	case 0:
		return "OFF"
	case 1:
		return "NORMAL"
	case 2:
		return "FULL"
	case 3:
		return "EXTRA"
	default:
		return strconv.Itoa(v)
	}
}
