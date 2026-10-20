package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
)

// HasAdministrator reports whether startup has already created the
// administrator role. A later configuration change does not add another one.
func HasAdministrator(ctx context.Context, db *sql.DB) (bool, error) {
	return dal.NewStore(db).Directory().RoleExists(ctx, AdministratorRole)
}

// SetInitialPasswordHash stores hash for subject when that account has no
// local password yet. A second start leaves an existing password unchanged.
func (d Directory) SetInitialPasswordHash(ctx context.Context, subject, hash string) error {
	var existing string
	var err error
	existing, err = dal.NewStore(d.DB).Directory().LocalPasswordHash(ctx, subject)
	if dberr.IsNotFound(err) {
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
	return dal.NewStore(d.DB).Directory().InitializeLocalPassword(ctx, subject, hash)
}
