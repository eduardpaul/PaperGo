package dms

import (
	"context"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/storage"
)

// ReclaimResult is the outcome of asking for a file version's bytes back.
// Deferred means a current file, a published file, a staged file or an open
// wait still needs the version, so the bytes stay.
type ReclaimResult struct {
	BlobID       string
	Availability string
	Deferred     bool
	Reason       string
	ReclaimAfter *time.Time
}

// ReclaimBlob marks a file version pending. Eligible bytes are freed by
// SweepReclaims after ObjectGracePeriod. History metadata is kept.
func (s *Service) ReclaimBlob(ctx context.Context, subject, itemID, blobID string) (out ReclaimResult, err error) {
	err = s.write(ctx, func(t *Service) error {
		if _, e := t.canWriteFile(ctx, subject, itemID); e != nil {
			return e
		}
		row, e := t.DAL.Blobs().ItemBlob(ctx, itemID, blobID)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		out, e = t.reclaimBlob(ctx, &row)
		return e
	})
	return out, err
}

// reclaimBlob shares the grace period and protection rules between explicit
// reclamation and file replacement with collection versioning disabled.
func (s *Service) reclaimBlob(ctx context.Context, row *blobs.Blob) (ReclaimResult, error) {
	if row.Availability == blobs.AvailabilityReclaimed {
		return ReclaimResult{BlobID: row.ID, Availability: string(blobs.AvailabilityReclaimed)}, nil
	}
	protected, reason, err := s.blobProtected(ctx, row)
	if err != nil {
		return ReclaimResult{}, err
	}
	if protected {
		_, err = s.DAL.Blobs().SetAvailability(ctx, row.ID, blobs.AvailabilityPending, nil)
		return ReclaimResult{BlobID: row.ID, Availability: string(blobs.AvailabilityPending), Deferred: true, Reason: reason}, err
	}
	if row.Availability == blobs.AvailabilityPending && row.ReclaimAfter != nil {
		return ReclaimResult{BlobID: row.ID, Availability: string(blobs.AvailabilityPending), ReclaimAfter: row.ReclaimAfter}, nil
	}
	due := time.Now().UTC().Add(ObjectGracePeriod)
	_, err = s.DAL.Blobs().SetAvailability(ctx, row.ID, blobs.AvailabilityPending, &due)
	return ReclaimResult{BlobID: row.ID, Availability: string(blobs.AvailabilityPending), ReclaimAfter: &due}, err
}

// blobProtected reports why a file version's bytes must stay. The caller is
// inside a transaction.
func (s *Service) blobProtected(ctx context.Context, row *blobs.Blob) (bool, string, error) {
	item, err := s.DAL.Resources().Resource(ctx, row.ItemID)
	if err != nil {
		return false, "", err
	}
	for _, check := range []struct {
		id     *string
		reason string
	}{
		{item.HeadRevisionID, "current"},
		{item.PublishedRevisionID, "published"},
	} {
		if check.id == nil {
			continue
		}
		rev, err := s.DAL.Revisions().Revision(ctx, *check.id)
		if dberr.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, "", err
		}
		if rev.BlobID != nil && *rev.BlobID == row.ID {
			return true, check.reason, nil
		}
	}
	if staged, err := s.DAL.Blobs().Staged(ctx, *row); err != nil || staged {
		return staged, "staged", err
	}
	if leased, err := s.DAL.Blobs().Leased(ctx, row.ID); err != nil || leased {
		return leased, "in use", err
	}
	return false, "", nil
}

// SweepReclaims frees file versions whose grace period has ended and that
// nothing still protects. Shared bytes stay while another file needs them.
func (s *Service) SweepReclaims(ctx context.Context, store storage.Store, now time.Time) error {
	after := ""
	for {
		rows, err := s.DAL.Blobs().Blobs(ctx, blobs.BlobFilter{AfterID: after, DueBy: &now, Limit: 100})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.ID
			if err = s.write(ctx, func(t *Service) error {
				files := t.DAL.Blobs()
				current, err := files.Blob(ctx, row.ID)
				if dberr.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if current.Availability != blobs.AvailabilityPending || current.ReclaimAfter != nil && current.ReclaimAfter.After(now) {
					return nil
				}
				protected, _, err := t.blobProtected(ctx, &current)
				if err != nil {
					return err
				}
				if protected {
					_, err = files.SetAvailability(ctx, current.ID, blobs.AvailabilityPending, nil)
					return err
				}
				if current.ReclaimAfter == nil {
					due := now.Add(ObjectGracePeriod)
					_, err = files.SetAvailability(ctx, current.ID, blobs.AvailabilityPending, &due)
					return err
				}
				if _, err = files.SetAvailability(ctx, current.ID, blobs.AvailabilityReclaimed, nil); err != nil {
					return err
				}
				return t.deleteUnreferencedObject(ctx, store, current.ObjectKey)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
