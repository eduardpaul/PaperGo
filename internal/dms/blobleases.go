package dms

import "context"

// LeaseBlobs pins bytes for a durable activity until consumption.
func (s *Service) LeaseBlobs(ctx context.Context, owner, runID string, ids []string) error {
	return s.DAL.Blobs().Lease(ctx, owner, runID, ids)
}
func (s *Service) ReleaseBlobLeases(ctx context.Context, owner string) error {
	return s.DAL.Blobs().Release(ctx, owner)
}
func (s *Service) ReleaseRunBlobLeases(ctx context.Context, runID string) error {
	return s.DAL.Blobs().ReleaseRun(ctx, runID)
}
