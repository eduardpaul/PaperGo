package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/itemhistory"
	"papergo/ent/resource"
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

func (s *Service) retainHistory(ctx context.Context, collection *ent.Resource, revision, previous *ent.ItemRevision) error {
	if collection.Versioning == resource.VersioningMajor {
		changed, err := revisionChanges(revision, previous)
		if err != nil {
			return err
		}
		last, err := s.currentHistoryNumber(ctx, revision.ItemID)
		if err != nil {
			return err
		}
		if err = s.Client.ItemHistory.Create().SetRevisionID(revision.ID).SetItemID(revision.ItemID).SetNumber(last + 1).SetChangedFields(changed).Exec(ctx); err != nil {
			return err
		}
		_, err = s.Client.ItemHistory.Delete().Where(itemhistory.ItemIDEQ(revision.ItemID), itemhistory.NumberLT(last+2-collection.MaxVersions)).Exec(ctx)
		if err != nil {
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

// collectRevisions deletes unreferenced snapshots of one item, or of every item
// when itemID is empty. The item filter is its own statement so SQLite seeks
// the item's revisions instead of scanning them all on every write.
func (s *Service) collectRevisions(ctx context.Context, itemID string) error {
	scope, args := "1=1", []any{}
	if itemID != "" {
		scope, args = "v.item_id=?", []any{itemID}
	}
	_, err := s.Client.ExecContext(ctx, `DELETE FROM item_revisions AS v
WHERE `+scope+`
AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.head_revision_id=v.id OR r.published_revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM item_surfaces p WHERE p.revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM item_histories h WHERE h.revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM domain_events e JOIN item_revisions event_revision
 ON event_revision.id=json_extract(e.data,'$.revision_id')
 WHERE e.dispatched_at IS NULL AND event_revision.item_id=v.item_id
 AND (event_revision.id=v.id OR event_revision.revision_number=v.revision_number+1))`, args...)
	return err
}
