package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// HasAdministrator reports whether startup has already created the
// administrator role. A later configuration change does not add another one.
func HasAdministrator(ctx context.Context, db *sql.DB) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM identity_roles WHERE id=?)", AdministratorRole).Scan(&exists)
	return exists, err
}

// SetInitialPasswordHash stores hash for subject when that account has no
// local password yet. A second start leaves an existing password unchanged.
func (d Directory) SetInitialPasswordHash(ctx context.Context, subject, hash string) error {
	var existing string
	err := d.DB.QueryRowContext(ctx, "SELECT password_hash FROM identity_users WHERE id=?", subject).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("local administrator %s was not created", subject)
	}
	if err != nil {
		return err
	}
	if existing != "" {
		return nil
	}
	if hash == "" {
		return errors.New("admin password is required")
	}
	result, err := d.DB.ExecContext(ctx, "UPDATE identity_users SET password_hash=? WHERE id=? AND password_hash=''", hash, subject)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var current string
		if err = d.DB.QueryRowContext(ctx, "SELECT password_hash FROM identity_users WHERE id=?", subject).Scan(&current); err != nil {
			return err
		}
		if current == "" {
			return errors.New("admin password is required")
		}
	}
	return nil
}
