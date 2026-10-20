package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"modernc.org/sqlite"
)

type BackupLock struct{ conn *sql.Conn }

// FreezeBackupReferences reserves the SQLite writer while immutable files and
// the database snapshot are archived. Reads use separate pooled connections.
func (d *Database) FreezeBackupReferences(ctx context.Context) (*BackupLock, error) {
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("freeze backup references: %w", err)
	}
	return &BackupLock{conn: conn}, nil
}

func (lock *BackupLock) Close() error {
	if lock.conn == nil {
		return nil
	}
	_, err := lock.conn.ExecContext(context.Background(), "ROLLBACK")
	err = errors.Join(err, lock.conn.Close())
	lock.conn = nil
	return err
}

// Snapshot copies all SQLite tables through the driver's online backup API.
func (d *Database) Snapshot(ctx context.Context, path string) error {
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) (err error) {
		backup, err := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(path)
		if err != nil {
			return err
		}
		defer func() {
			if e := backup.Finish(); err == nil {
				err = e
			}
		}()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, e := backup.Step(256)
			if e != nil {
				return e
			}
			if !more {
				return nil
			}
		}
	})
}

func (d *Database) CheckIntegrity(ctx context.Context) error {
	var integrity string
	if err := d.SQL.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("database integrity check failed: %s %v", integrity, err)
	}
	rows, err := d.SQL.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	bad := rows.Next()
	if err := rows.Err(); err != nil {
		return err
	}
	if bad {
		return errors.New("database foreign key check failed")
	}
	return nil
}
