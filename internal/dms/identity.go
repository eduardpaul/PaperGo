package dms

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
)

func (s *Service) Directory() identity.Directory { return identity.Directory{DB: s.db} }
func (s *Service) RequireScope(ctx context.Context, subject, scope string) error {
	if s.homeProvisioning {
		return nil
	}
	q := dal.NewStore(s.db)
	if s.transaction {
		q = dal.NewStore(s.db).WithTx(s.tx)
	}
	err := identity.RequireScope(ctx, q, subject, scope)
	if errors.Is(err, identity.ErrForbidden) {
		return ErrForbidden
	}
	return err
}

func (s *Service) requireTaxonomy(ctx context.Context, subject, action string) error {
	scope := "taxonomy.read"
	if action == "manage" {
		scope = "taxonomy.manage"
	}
	return s.RequireScope(ctx, subject, scope)
}
func (s *Service) auditTaxonomy(ctx context.Context, subject, action string, details map[string]any) error {
	return s.RecordEntity(ctx, subject, action, AuditTarget{}, details)
}
func (s *Service) contentTypeResource(ctx context.Context, subject, id, action string) (*resources.Resource, error) {
	scope := "contentType.read"
	if action == "manage" {
		scope = "contentType.manage"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.authorize(ctx, subject, id, action)
}
func (s *Service) schemaWorkspace(ctx context.Context, subject, id, action string) (*resources.Resource, error) {
	scope := "contentType.read"
	if action == "manage" {
		scope = "contentType.manage"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return nil, err
	}
	return s.workspace(ctx, subject, id, action)
}
