package database

import "context"

// EnsureSchema initializes an empty database from the selected DAL master.
func (d *Database) EnsureSchema(ctx context.Context) error {
	exists, err := d.DAL.HasTables(ctx)
	if err != nil {
		return err
	}
	if !exists {
		if err := d.DAL.Migrate(ctx); err != nil {
			return err
		}
	}
	return d.CheckSchema(ctx)
}
