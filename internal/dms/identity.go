package dms

import (
	"context"
	"errors"
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
