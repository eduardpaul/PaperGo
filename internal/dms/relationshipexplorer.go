package dms

import (
	"context"
	"errors"
	"strings"

	"papergo/internal/dal/content"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

type WorkspaceRelationshipsQuery struct {
	Type, ItemID, CollectionID, Scope, Direction, Search, Filter, After string
	Untyped                                                             bool
	Directed                                                            *bool
	Limit                                                               int
}

type WorkspaceRelationshipView = content.WorkspaceRelationship

type RelationshipNeighborhood struct {
	Nodes     []content.RelationshipEndpoint `json:"nodes"`
	Edges     []WorkspaceRelationshipView    `json:"edges"`
	Truncated bool                           `json:"truncated"`
}

func (s *Service) workspaceRelationshipQuery(ctx context.Context, subject, workspaceID string, in WorkspaceRelationshipsQuery) (content.RelationshipExploreQuery, bool, error) {
	out := content.RelationshipExploreQuery{WorkspaceID: workspaceID, ItemID: in.ItemID, CollectionID: in.CollectionID, Scope: in.Scope, Direction: in.Direction, Search: in.Search, After: in.After, Untyped: in.Untyped, Directed: in.Directed, Limit: pageSize(in.Limit) + 1, Reader: principal(ctx, subject)}
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return out, false, err
	}
	workspace, err := s.authorize(ctx, subject, workspaceID, "read")
	if err != nil {
		return out, false, err
	}
	if workspace.Kind != refs.KindWorkspace {
		return out, false, invalid("workspace_id must reference a workspace")
	}
	if in.Untyped && in.Type != "" {
		return out, false, invalid("untyped and type cannot be combined")
	}
	if err = content.ValidateRelationshipExplore(out); err != nil {
		return out, false, invalid(err.Error())
	}
	if in.CollectionID != "" {
		collection, err := s.authorize(ctx, subject, in.CollectionID, "read")
		if err != nil {
			return out, false, err
		}
		if collection.WorkspaceID != workspaceID || (collection.Kind != refs.KindList && collection.Kind != refs.KindLibrary) {
			return out, false, invalid("collection_id must reference a collection in this workspace")
		}
	}
	if in.ItemID != "" {
		item, err := s.Get(ctx, subject, in.ItemID)
		if err != nil {
			return out, false, err
		}
		if item.Kind != refs.KindItem {
			return out, false, invalid("item_id must reference an item")
		}
	}
	if in.Filter != "" {
		if out.Filter, err = relationshipFilter(in.Filter); err != nil {
			return out, false, err
		}
	}
	if in.Type != "" {
		predicate, found, err := s.resolveRelationshipTerm(ctx, subject, in.Type, false)
		if err != nil || !found {
			return out, false, err
		}
		out.TypeID = predicate.ID
	}
	return out, true, nil
}

func relationshipExploreError(err error) error {
	if errors.Is(err, dberr.ErrCheckViolation) {
		return invalid(err.Error())
	}
	return err
}

func (s *Service) WorkspaceRelationships(ctx context.Context, subject, workspaceID string, in WorkspaceRelationshipsQuery) (Page[WorkspaceRelationshipView], error) {
	return read(ctx, s, func(t *Service) (Page[WorkspaceRelationshipView], error) {
		empty := Page[WorkspaceRelationshipView]{Data: []WorkspaceRelationshipView{}}
		query, found, err := t.workspaceRelationshipQuery(ctx, subject, workspaceID, in)
		if err != nil || !found {
			return empty, err
		}
		rows, err := t.DAL.Content().ExploreWorkspaceRelationships(ctx, query)
		if err != nil {
			return empty, relationshipExploreError(err)
		}
		return entityPage(rows, in.Limit, func(e WorkspaceRelationshipView) string { return e.ID }), nil
	})
}

func (s *Service) WorkspaceRelationshipTypes(ctx context.Context, subject, workspaceID string, in WorkspaceRelationshipsQuery) (Page[content.WorkspaceRelationshipType], error) {
	return read(ctx, s, func(t *Service) (Page[content.WorkspaceRelationshipType], error) {
		empty := Page[content.WorkspaceRelationshipType]{Data: []content.WorkspaceRelationshipType{}}
		if err := t.RequireScope(ctx, subject, "taxonomy.read"); err != nil {
			return empty, err
		}
		query, found, err := t.workspaceRelationshipQuery(ctx, subject, workspaceID, in)
		if err != nil || !found {
			return empty, err
		}
		rows, err := t.DAL.Content().ExploreRelationshipTypes(ctx, query)
		if err != nil {
			return empty, err
		}
		return entityPage(rows, in.Limit, func(typ content.WorkspaceRelationshipType) string { return typ.SortKey + "\x1f" + typ.ID }), nil
	})
}

func (s *Service) WorkspaceRelationshipItems(ctx context.Context, subject, workspaceID string, in WorkspaceRelationshipsQuery) (Page[content.RelationshipEndpoint], error) {
	return read(ctx, s, func(t *Service) (Page[content.RelationshipEndpoint], error) {
		empty := Page[content.RelationshipEndpoint]{Data: []content.RelationshipEndpoint{}}
		query, found, err := t.workspaceRelationshipQuery(ctx, subject, workspaceID, in)
		if err != nil || !found {
			return empty, err
		}
		if strings.TrimSpace(query.Search) == "" && query.ItemID == "" {
			return empty, nil
		}
		rows, err := t.DAL.Content().ExploreRelationshipItems(ctx, query)
		if err != nil {
			return empty, err
		}
		return entityPage(rows, in.Limit, func(item content.RelationshipEndpoint) string { return item.SortKey + "\x1f" + item.ID }), nil
	})
}

func (s *Service) WorkspaceRelationshipNeighborhood(ctx context.Context, subject, workspaceID string, in WorkspaceRelationshipsQuery) (RelationshipNeighborhood, error) {
	return read(ctx, s, func(t *Service) (RelationshipNeighborhood, error) {
		out := RelationshipNeighborhood{Nodes: []content.RelationshipEndpoint{}, Edges: []WorkspaceRelationshipView{}}
		if in.ItemID == "" {
			return out, invalid("item_id is required for a neighborhood")
		}
		in.After = ""
		in.Limit = 50
		query, found, err := t.workspaceRelationshipQuery(ctx, subject, workspaceID, in)
		if err != nil || !found {
			return out, err
		}
		rows, err := t.DAL.Content().ExploreWorkspaceRelationships(ctx, query)
		if err != nil {
			return out, relationshipExploreError(err)
		}
		seen := map[string]bool{}
		for _, edge := range rows {
			newNodes := 0
			for _, item := range []content.RelationshipEndpoint{edge.SourceItem, edge.TargetItem} {
				if !seen[item.ID] {
					newNodes++
				}
			}
			if len(out.Edges) == 50 || len(out.Nodes)+newNodes > 25 {
				out.Truncated = true
				continue
			}
			for _, item := range []content.RelationshipEndpoint{edge.SourceItem, edge.TargetItem} {
				if !seen[item.ID] {
					seen[item.ID] = true
					out.Nodes = append(out.Nodes, item)
				}
			}
			out.Edges = append(out.Edges, edge)
		}
		return out, nil
	})
}
