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
	// Durable item events and publication audit reference immutable content
	// snapshots independently of user history; retention removes history entries.
	return nil
}
