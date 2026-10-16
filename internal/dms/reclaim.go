package dms

import (
	"context"
	"time"

	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/itemrevision"
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
		row, e := t.Client.Blob.Query().Where(blob.ItemIDEQ(itemID), blob.IDEQ(blobID)).Only(ctx)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if row.Availability == blob.AvailabilityReclaimed {
			out = ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityReclaimed)}
			return nil
		}
		protected, reason, e := t.blobProtected(ctx, row)
		if e != nil {
			return e
		}
		if protected {
			if e = t.Client.Blob.UpdateOneID(row.ID).SetAvailability(blob.AvailabilityPending).ClearReclaimAfter().Exec(ctx); e != nil {
				return e
			}
			out = ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), Deferred: true, Reason: reason}
			return nil
		}
		if row.Availability == blob.AvailabilityPending && row.ReclaimAfter != nil {
			out = ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), ReclaimAfter: row.ReclaimAfter}
			return nil
		}
		due := time.Now().UTC().Add(ObjectGracePeriod)
		if e = t.Client.Blob.UpdateOneID(row.ID).SetAvailability(blob.AvailabilityPending).SetReclaimAfter(due).Exec(ctx); e != nil {
			return e
		}
		out = ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), ReclaimAfter: &due}
		return nil
	})
	return out, err
}

// blobProtected reports why a file version's bytes must stay. The caller is
// inside a transaction.
func (s *Service) blobProtected(ctx context.Context, row *ent.Blob) (bool, string, error) {
	item, err := s.Client.Resource.Get(ctx, row.ItemID)
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
		rev, err := s.Client.ItemRevision.Query().Where(itemrevision.IDEQ(*check.id)).Only(ctx)
		if ent.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, "", err
		}
		if rev.BlobID != nil && *rev.BlobID == row.ID {
			return true, check.reason, nil
		}
	}
	var staged bool
	if err = s.scan(ctx, []any{&staged}, `SELECT EXISTS(SELECT 1 FROM staged_files WHERE state='pending' AND (object_key=? OR EXISTS (SELECT 1 FROM json_each(sources) WHERE json_extract(value, '$.blob_id')=?)))`, row.ObjectKey, row.ID); err != nil {
		return false, "", err
	}
	if staged {
		return true, "staged", nil
	}
	var waiting bool
	if err = s.scan(ctx, []any{&waiting}, `SELECT EXISTS(SELECT 1 FROM workflow_waits WHERE status='open' AND json_type(data, '$.blobs')='array' AND EXISTS (SELECT 1 FROM json_each(json_extract(data, '$.blobs')) WHERE json_extract(value, '$.blob_id')=?))`, row.ID); err != nil {
		return false, "", err
	}
	if waiting {
		return true, "in use", nil
	}
	return false, "", nil
}

// SweepReclaims frees file versions whose grace period has ended and that
// nothing still protects. Shared bytes stay while another file needs them.
func (s *Service) SweepReclaims(ctx context.Context, store storage.Store, now time.Time) error {
	rows, err := s.Client.Blob.Query().Where(
		blob.AvailabilityEQ(blob.AvailabilityPending),
		blob.ReclaimAfterNotNil(),
		blob.ReclaimAfterLTE(now),
	).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = s.write(ctx, func(t *Service) error {
			current, err := t.Client.Blob.Get(ctx, row.ID)
			if ent.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if current.Availability != blob.AvailabilityPending || current.ReclaimAfter == nil || current.ReclaimAfter.After(now) {
				return nil
			}
			protected, _, err := t.blobProtected(ctx, current)
			if err != nil {
				return err
			}
			if protected {
				return t.Client.Blob.UpdateOneID(current.ID).ClearReclaimAfter().Exec(ctx)
			}
			if err = t.Client.Blob.UpdateOneID(current.ID).SetAvailability(blob.AvailabilityReclaimed).ClearReclaimAfter().Exec(ctx); err != nil {
				return err
			}
			return t.deleteUnreferencedObject(ctx, store, current.ObjectKey)
		}); err != nil {
			return err
		}
	}
	return nil
}
