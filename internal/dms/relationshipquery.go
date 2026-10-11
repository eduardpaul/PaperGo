package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/relationship"
	"papergo/ent/resource"
)

type LocatedItem struct {
	*ent.Resource
	WorkspaceName  string `json:"workspace_name"`
	CollectionID   string `json:"collection_id"`
	CollectionName string `json:"collection_name"`
}
type RelationshipView struct {
	*ent.Relationship
	RelatedItem LocatedItem `json:"related_item"`
}
type WorkspaceRelationshipView struct {
	*ent.Relationship
	SourceItem LocatedItem `json:"source_item"`
	TargetItem LocatedItem `json:"target_item"`
}

func (s *Service) locateRelationshipItem(ctx context.Context, subject, id string) (LocatedItem, error) {
	item, err := s.Get(ctx, subject, id)
	if err != nil {
		return LocatedItem{}, err
	}
	workspace, err := s.Client.Resource.Get(ctx, item.WorkspaceID)
	if err != nil {
		return LocatedItem{}, err
	}
	if item.ContainerID == nil {
		return LocatedItem{}, ErrNotFound
	}
	collection, err := s.Client.Resource.Get(ctx, *item.ContainerID)
	if err != nil {
		return LocatedItem{}, err
	}
	return LocatedItem{item, workspace.Name, collection.ID, collection.Name}, nil
}
func (s *Service) relationshipView(ctx context.Context, subject, itemID string, edge *ent.Relationship) (RelationshipView, error) {
	peer := edge.TargetID
	if peer == itemID {
		peer = edge.SourceID
	}
	located, err := s.locateRelationshipItem(ctx, subject, peer)
	return RelationshipView{edge, located}, err
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
func relationshipVisibility(subject string) func(*entsql.Selector) {
	return func(sel *entsql.Selector) {
		for _, field := range []string{relationship.FieldSourceID, relationship.FieldTargetID} {
			id := sel.C(field)
			acl, args := permissionSQL(id, subject, "read")
			visible, visibleArgs := visibleSQL(id, subject, "auto")
			sel.Where(entsql.ExprP(acl+" AND "+visible, append(args, visibleArgs...)...))
		}
	}
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
		if w.Kind != resource.KindWorkspace {
			return out, invalid("workspace_id must reference a workspace")
		}
		q := t.Client.Relationship.Query().Where(relationshipVisibility(subject))
		q.Where(func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP("EXISTS(SELECT 1 FROM resources peer WHERE peer.workspace_id=? AND peer.id IN ("+sel.C(relationship.FieldSourceID)+","+sel.C(relationship.FieldTargetID)+"))", workspaceID))
		})
		if typeValue != "" {
			predicate, err := t.resolveRelationshipTerm(ctx, subject, typeValue, false)
			if err != nil {
				return out, err
			}
			if predicate == nil {
				return out, nil
			}
			q.Where(relationship.TypeIDEQ(predicate.ID))
		}
		if directed != nil {
			q.Where(relationship.DirectedEQ(*directed))
		}
		if filter != "" {
			predicate, args, err := relationshipFilter(filter)
			if err != nil {
				return out, err
			}
			q.Where(func(sel *entsql.Selector) { sel.Where(entsql.ExprP(predicate, args...)) })
		}
		if after != "" {
			q.Where(relationship.IDGT(after))
		}
		rows, err := q.Order(ent.Asc(relationship.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		if err != nil {
			return out, err
		}
		page := entityPage(rows, limit, func(edge *ent.Relationship) string { return edge.ID })
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
			out.Data = append(out.Data, WorkspaceRelationshipView{edge, source, target})
		}
		return out, nil
	})
}
