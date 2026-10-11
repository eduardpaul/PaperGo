package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/itemhistory"
	"papergo/ent/itemrevision"
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
		if err = s.Client.ItemHistory.Create().SetRevisionID(revision.ID).SetChangedFields(changed).Exec(ctx); err != nil {
			return err
		}
		retained, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(revision.ItemID), itemrevision.HasHistory()).Order(ent.Desc(itemrevision.FieldRevisionNumber)).Offset(collection.MaxVersions).All(ctx)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(retained))
		for _, old := range retained {
			ids = append(ids, old.ID)
		}
		if len(ids) > 0 {
			if _, err = s.Client.ItemHistory.Delete().Where(itemhistory.RevisionIDIn(ids...)).Exec(ctx); err != nil {
				return err
			}
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

func (s *Service) collectRevisions(ctx context.Context, itemID string) error {
	_, err := s.Client.ExecContext(ctx, `DELETE FROM item_revisions AS v
WHERE (?='' OR v.item_id=?)
AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.head_revision_id=v.id OR r.published_revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM item_surfaces p WHERE p.revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM item_histories h WHERE h.revision_id=v.id)
AND NOT EXISTS (SELECT 1 FROM domain_events e JOIN item_revisions event_revision
 ON event_revision.id=json_extract(e.data,'$.revision_id')
 WHERE e.dispatched_at IS NULL AND event_revision.item_id=v.item_id
 AND (event_revision.id=v.id OR event_revision.revision_number=v.revision_number+1))`, itemID, itemID)
	return err
}
