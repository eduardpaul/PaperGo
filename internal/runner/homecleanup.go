package runner

import (
	"context"
	"database/sql"
)

func (r *Runner) purgeHomeRunPayloads(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	return r.durable.PurgeWorkspacePayloads(ctx, tx, workspaceID)
}
func (r *Runner) protectRunPayloads(ctx context.Context) error {
	return r.durable.ProtectPurgedRuns(ctx, r.dms.DAL)
}
