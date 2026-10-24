package database

import (
	"context"

	"papergo/internal/dal"
)

// UpgradeWith runs Upgrade with the given migrations instead of the
// embedded ones.
func (d *Database) UpgradeWith(ctx context.Context, steps []dal.Migration) (Upgraded, error) {
	return d.upgrade(ctx, steps)
}
