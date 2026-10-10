package dms

import (
	"context"
	"errors"
	"papergo/ent"
	"papergo/internal/identity"
)

func (s *Service) Directory() identity.Directory { return identity.Directory{DB: s.db} }
func (s *Service) RequireScope(ctx context.Context, subject, scope string) error {
	var q identity.Querier = s.db
	if s.transaction {
		q = s.tx
	}
	err := identity.RequireScope(ctx, q, subject, scope)
	if errors.Is(err, identity.ErrForbidden) {
		return ErrForbidden
	}
	return err
}

func (s *Service) taxonomyWorkspace(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	scope := "taxonomy.read"
	if action == "manage" {
		scope = "taxonomy.manage"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.workspace(ctx, subject, id, action)
}
func (s *Service) contentTypeResource(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	scope := "contentType.read"
	if action == "manage" {
		scope = "contentType.manage"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.authorize(ctx, subject, id, action)
}
func (s *Service) listWorkspace(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	scope := "list.read"
	if action == "manage" {
		scope = "list.write"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.workspace(ctx, subject, id, action)
}
func (s *Service) schemaWorkspace(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	scope := "contentType.read"
	if action == "manage" {
		scope = "contentType.manage"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.workspace(ctx, subject, id, action)
}
