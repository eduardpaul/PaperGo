package dms

import (
	"context"
	"time"

	"papergo/ent"
	"papergo/ent/itemsurface"
)

// Relationship writes touch both live endpoints without recording a metadata
// revision. The event pins the endpoint's head for workflow term filters.
func (s *Service) touchRelationship(ctx context.Context, actor string, link *ent.Relationship, action string) error {
	now := time.Now().UTC()
	for _, id := range []string{link.SourceID, link.TargetID} {
		item, err := s.Client.Resource.Get(ctx, id)
		if err != nil {
			return err
		}
		if item.DeletedAt != nil {
			continue
		}
		if err = s.Client.Resource.UpdateOne(item).AddVersion(1).SetUpdatedAt(now).SetUpdatedBy(actor).Exec(ctx); err != nil {
			return err
		}
		if err = s.Client.ItemSurface.Update().Where(itemsurface.ItemIDEQ(id)).SetModifiedAt(queryTime(now)).SetModifiedBy(actor).Exec(ctx); err != nil {
			return err
		}
		peer := link.TargetID
		if peer == id {
			peer = link.SourceID
		}
		data := map[string]any{"relationship_id": link.ID, "relationship_action": action, "peer_id": peer, "changed_fields": []string{"$relationships"}}
		if item.HeadRevisionID != nil {
			data["revision_id"] = *item.HeadRevisionID
		}
		if item.ContentTypeID != nil {
			data["content_type_id"] = *item.ContentTypeID
		}
		s.emit(ctx, EventItemUpdated, actor, item.WorkspaceID, item.ContainerID, id, data)
	}
	return nil
}
