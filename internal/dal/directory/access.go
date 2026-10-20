package directory

import (
	"context"
	"time"
)

// EffectiveGroups expands nested memberships upward and deduplicates diamonds.
func (r *Repo) EffectiveGroups(ctx context.Context, subject string) ([]string, error) {
	return r.read.DirectoryEffectiveGroups(ctx, subject)
}
func (r *Repo) HasAdministrator(ctx context.Context, roleID string) (bool, error) {
	return r.read.DirectoryHasAdministrator(ctx, roleID)
}

// EffectiveScopes includes live role and integration grants. The application
// intersects them with the credential's scope cap.
func (r *Repo) EffectiveScopes(ctx context.Context, subject, tokenID string) ([]string, error) {
	return r.read.DirectoryEffectiveScopes(ctx, subject, tokenID, time.Now().UTC())
}
