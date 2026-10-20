package dms

import (
	"context"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
)

func validateHistorySettings(collection bool, mode *string, count *int) error {
	if mode == nil && count == nil {
		return nil
	}
	if !collection {
		return invalid("versioning and max_versions belong to collections")
	}
	if mode != nil && *mode != "off" && *mode != "major" {
		return invalid("versioning must be off or major")
	}
	if count != nil && (*count < 1 || *count > 500) {
		return invalid("max_versions must be between 1 and 500")
	}
	return nil
}

func (s *Service) retainHistory(ctx context.Context, collection *resources.Resource, revision, previous *revisions.Revision) error {
	if collection.Versioning == resources.VersioningMajor {
		changed, err := revisionChanges(revision, previous)
		if err != nil {
			return err
		}
		if err = s.DAL.Revisions().Retain(ctx, *revision, changed, collection.MaxVersions); err != nil {
			return err
		}
	}
	return nil
}

// CollectRevisions removes snapshots with no live surface, retained history or
// queued event. Publication records contain their own immutable snapshots.
// Queued events also keep the preceding revision for term-change triggers.
func (s *Service) CollectRevisions(ctx context.Context) error {
	return s.write(ctx, func(t *Service) error { return t.collectRevisions(ctx, "") })
}

// CollectItemRevisions collects the snapshots of these items only. Dispatching
// events releases the revisions they kept, so the dispatcher collects just the
// items of the batch it dispatched instead of sweeping every revision.
func (s *Service) CollectItemRevisions(ctx context.Context, itemIDs []string) error {
	return s.write(ctx, func(t *Service) error {
		seen := map[string]bool{}
		for _, id := range itemIDs {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if err := t.collectRevisions(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// collectRevisions deletes unreferenced snapshots of one item, or of every
// item when itemID is empty.
func (s *Service) collectRevisions(ctx context.Context, itemID string) error {
	return s.DAL.Revisions().Collect(ctx, itemID)
}
