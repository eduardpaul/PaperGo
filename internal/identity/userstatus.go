package identity

import (
	"context"
	"database/sql"
	"errors"
)

type UserStatus struct {
	Subject string `json:"subject"`
	Active  bool   `json:"active"`
}

func (d Directory) UserStatus(ctx context.Context, actor, subject string) (UserStatus, error) {
	if err := RequireScope(ctx, d.DB, actor, "user.read"); err != nil {
		return UserStatus{}, err
	}
	out := UserStatus{Subject: subject}
	err := d.DB.QueryRowContext(ctx, "SELECT active FROM identity_users WHERE id=?", subject).Scan(&out.Active)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return out, err
}

func (d Directory) SetUserActive(ctx context.Context, actor, subject string, active bool) (UserStatus, error) {
	if err := ValidateSubject(subject); err != nil {
		return UserStatus{}, err
	}
	err := d.write(ctx, actor, "user.manage", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE identity_users SET active=? WHERE id=? AND deleted_at IS NULL", active, subject)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrNotFound
		}
		return err
	})
	return UserStatus{Subject: subject, Active: active}, err
}
