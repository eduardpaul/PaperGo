package dms

import (
	"context"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
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
type WorkspaceRelationshipView struct {
	RelationshipRecord
	SourceItem LocatedItem `json:"source_item"`
	TargetItem LocatedItem `json:"target_item"`
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
func (s *Service) WorkspaceRelationships(ctx context.Context, subject, workspaceID, typeValue string, directed *bool, filter, after string, limit int) (Page[WorkspaceRelationshipView], error) {
	return read(ctx, s, func(t *Service) (Page[WorkspaceRelationshipView], error) {
		out := Page[WorkspaceRelationshipView]{Data: []WorkspaceRelationshipView{}}
		if err := t.RequireScope(ctx, subject, "list.read"); err != nil {
			return out, err
		}
		w, err := t.authorize(ctx, subject, workspaceID, "read")
		if err != nil {
			return out, err
		}
		if w.Kind != refs.KindWorkspace {
			return out, invalid("workspace_id must reference a workspace")
		}
		query := content.Links{Reader: principal(ctx, subject), WorkspaceID: workspaceID, Directed: directed, AfterID: after}
		if typeValue != "" {
			predicate, found, err := t.resolveRelationshipTerm(ctx, subject, typeValue, false)
			if err != nil {
				return out, err
			}
			if !found {
				return out, nil
			}
			query.TypeID = predicate.ID
		}
		if filter != "" {
			if query.Filter, err = relationshipFilter(filter); err != nil {
				return out, err
			}
		}
		page, err := t.visibleRelationships(ctx, query, limit)
		if err != nil {
			return out, err
		}
		out.NextCursor = page.NextCursor
		for _, edge := range page.Data {
			source, err := t.locateRelationshipItem(ctx, subject, edge.SourceID)
			if err != nil {
				return out, err
			}
			target, err := t.locateRelationshipItem(ctx, subject, edge.TargetID)
			if err != nil {
				return out, err
			}
			out.Data = append(out.Data, WorkspaceRelationshipView{viewRelationship(edge), source, target})
		}
		return out, nil
	})
}
