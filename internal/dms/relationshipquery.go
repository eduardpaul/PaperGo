package dms

import (
	"context"
	"papergo/internal/dal/resources"

	"papergo/internal/dal/relationships"
)

type LocatedItem struct {
	*resources.Resource
	WorkspaceName  string `json:"workspace_name"`
	CollectionID   string `json:"collection_id"`
	CollectionName string `json:"collection_name"`
}
type RelationshipView struct {
	RelationshipRecord
	RelatedItem LocatedItem `json:"related_item"`
}

func (s *Service) locateRelationshipItem(ctx context.Context, subject, id string) (LocatedItem, error) {
	item, err := s.Get(ctx, subject, id)
	if err != nil {
		return LocatedItem{}, err
	}
	workspace, err := s.DAL.Resources().Resource(ctx, item.WorkspaceID)
	if err != nil {
		return LocatedItem{}, err
	}
	if item.ContainerID == nil {
		return LocatedItem{}, ErrNotFound
	}
	collection, err := s.DAL.Resources().Resource(ctx, *item.ContainerID)
	if err != nil {
		return LocatedItem{}, err
	}
	return LocatedItem{item, workspace.Name, collection.ID, collection.Name}, nil
}
func (s *Service) relationshipView(ctx context.Context, subject, itemID string, edge relationships.Relationship) (RelationshipView, error) {
	located, err := s.locateRelationshipItem(ctx, subject, edge.Peer(itemID))
	return RelationshipView{viewRelationship(edge), located}, err
}
func (s *Service) Relationship(ctx context.Context, subject, itemID, edgeID string) (RelationshipView, error) {
	return read(ctx, s, func(t *Service) (RelationshipView, error) {
		if _, err := t.Get(ctx, subject, itemID); err != nil {
			return RelationshipView{}, err
		}
		edge, err := t.itemLink(ctx, itemID, edgeID)
		if err != nil {
			return RelationshipView{}, err
		}
		return t.relationshipView(ctx, subject, itemID, edge)
	})
}
