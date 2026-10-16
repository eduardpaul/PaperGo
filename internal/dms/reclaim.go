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
		out, e = t.reclaimBlob(ctx, row)
		return e
	})
	return out, err
}

// reclaimBlob shares the grace period and protection rules between explicit
// reclamation and file replacement with collection versioning disabled.
func (s *Service) reclaimBlob(ctx context.Context, row *ent.Blob) (ReclaimResult, error) {
	if row.Availability == blob.AvailabilityReclaimed {
		return ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityReclaimed)}, nil
	}
	protected, reason, err := s.blobProtected(ctx, row)
	if err != nil {
		return ReclaimResult{}, err
	}
	if protected {
		err = s.Client.Blob.UpdateOneID(row.ID).SetAvailability(blob.AvailabilityPending).ClearReclaimAfter().Exec(ctx)
		return ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), Deferred: true, Reason: reason}, err
	}
	if row.Availability == blob.AvailabilityPending && row.ReclaimAfter != nil {
		return ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), ReclaimAfter: row.ReclaimAfter}, nil
	}
	due := time.Now().UTC().Add(ObjectGracePeriod)
	err = s.Client.Blob.UpdateOneID(row.ID).SetAvailability(blob.AvailabilityPending).SetReclaimAfter(due).Exec(ctx)
	return ReclaimResult{BlobID: row.ID, Availability: string(blob.AvailabilityPending), ReclaimAfter: &due}, err
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
	if err = s.scan(ctx, []any{&waiting}, `SELECT EXISTS(SELECT 1 FROM blob_leases WHERE blob_id=?)`, row.ID); err != nil {
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
	after := ""
	for {
		rows, err := s.Client.Blob.Query().Where(
			blob.IDGT(after), blob.AvailabilityEQ(blob.AvailabilityPending),
			blob.Or(blob.ReclaimAfterIsNil(), blob.ReclaimAfterLTE(now)),
		).Order(ent.Asc(blob.FieldID)).Limit(100).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.ID
			if err = s.write(ctx, func(t *Service) error {
				current, err := t.Client.Blob.Get(ctx, row.ID)
				if ent.IsNotFound(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if current.Availability != blob.AvailabilityPending || current.ReclaimAfter != nil && current.ReclaimAfter.After(now) {
					return nil
				}
				protected, _, err := t.blobProtected(ctx, current)
				if err != nil {
					return err
				}
				if protected {
					return t.Client.Blob.UpdateOneID(current.ID).ClearReclaimAfter().Exec(ctx)
				}
				if current.ReclaimAfter == nil {
					return t.Client.Blob.UpdateOneID(current.ID).SetReclaimAfter(now.Add(ObjectGracePeriod)).Exec(ctx)
				}
				if err = t.Client.Blob.UpdateOneID(current.ID).SetAvailability(blob.AvailabilityReclaimed).ClearReclaimAfter().Exec(ctx); err != nil {
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
