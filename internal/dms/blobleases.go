package dms

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// LeaseBlobs pins bytes for a durable activity, until its completion is consumed.
func (s *Service) LeaseBlobs(ctx context.Context, owner, runID string, ids []string) error {
	for _, id := range ids {
		if _, err := s.Client.ExecContext(ctx, `INSERT INTO blob_leases(id,created_at,owner_id,run_id,blob_id) VALUES(?,?,?,?,?) ON CONFLICT(owner_id,blob_id) DO NOTHING`, uuid.NewString(), time.Now().UTC(), owner, runID, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) ReleaseBlobLeases(ctx context.Context, owner string) error {
	_, err := s.Client.ExecContext(ctx, `DELETE FROM blob_leases WHERE owner_id=?`, owner)
	return err
}
func (s *Service) ReleaseRunBlobLeases(ctx context.Context, runID string) error {
	_, err := s.Client.ExecContext(ctx, `DELETE FROM blob_leases WHERE run_id=?`, runID)
	return err
}
