package dms

import "context"

// LeaseOperationBlobs pins the bytes an operation accepted until it ends, so
// it processes those bytes even when the item gets a newer file.
func (s *Service) LeaseOperationBlobs(ctx context.Context, operationID string, ids []string) error {
	return s.DAL.Blobs().LeaseOperation(ctx, operationID, ids)
}
