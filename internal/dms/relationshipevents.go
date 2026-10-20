package dms

import (
	"context"
	"time"

	"papergo/internal/dal/relationships"
)

// Relationship writes touch both live endpoints without recording a metadata
// revision. The event pins the endpoint's head for workflow term filters.
func (s *Service) touchRelationship(ctx context.Context, actor string, link relationships.Relationship, action string) error {
	now := time.Now().UTC()
	for _, id := range []string{link.SourceID, link.TargetID} {
		item, err := s.DAL.Resources().Resource(ctx, id)
		if err != nil {
			return err
		}
		if item.DeletedAt != nil {
			continue
		}
		if err = s.DAL.Resources().Touch(ctx, item.ID, item.Version, actor); err != nil {
			return err
		}
		if err = s.DAL.Surfaces().Touch(ctx, []string{id}, now, actor); err != nil {
			return err
		}
		data := map[string]any{"relationship_id": link.ID, "relationship_action": action, "peer_id": link.Peer(id), "changed_fields": []string{"$relationships"}}
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
