package identity

import (
	"context"
	"database/sql"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
)

type UserStatus struct {
	Subject string `json:"subject"`
	Active  bool   `json:"active"`
}

func (d Directory) UserStatus(ctx context.Context, actor, subject string) (UserStatus, error) {
	if err := RequireScope(ctx, dal.NewStore(d.DB), actor, "user.read"); err != nil {
		return UserStatus{}, err
	}
	out := UserStatus{Subject: subject}
	var err error
	user, e := dal.NewStore(d.DB).Directory().User(ctx, subject)
	out.Active, err = user.Active, e
	if dberr.IsNotFound(err) {
		err = ErrNotFound
	}
	return out, err
}

func (d Directory) SetUserActive(ctx context.Context, actor, subject string, active bool) (UserStatus, error) {
	if err := ValidateSubject(subject); err != nil {
		return UserStatus{}, err
	}
	err := d.write(ctx, actor, "user.manage", func(tx *sql.Tx) error {
		return auditedIdentity(ctx, d.store(tx), actor, directory.EntityUser, directory.ChangeStatus, subject, func() error {
			err := d.store(tx).Directory().SetUserActive(ctx, subject, active)
			if dberr.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		})
	})
	return UserStatus{Subject: subject, Active: active}, err
}
