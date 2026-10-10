package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/identityuser"
	"papergo/internal/identity"
)

// Person IDs are enrolled OIDC subjects, the organization directory's user IDs.
func normalizePersonRef(_ *ent.FieldDefinition, value any) (any, error) {
	id, ok := value.(string)
	if !ok {
		return nil, invalid("expected a user ID")
	}
	if err := identity.ValidateSubject(id); err != nil {
		return nil, invalid("expected a user ID")
	}
	return id, nil
}

func (s *Service) validatePersonValues(ctx context.Context, values []any, old map[string]bool) error {
	for _, value := range values {
		id := value.(string)
		if old[id] {
			continue
		}
		exists, err := s.Client.IdentityUser.Query().Where(identityuser.IDEQ(id), identityuser.ActiveEQ(true)).Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return invalid("person requires an active user of the organization")
		}
	}
	return nil
}

func (s *Service) People(ctx context.Context, subject string) ([]string, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	return s.Client.IdentityUser.Query().Where(identityuser.ActiveEQ(true)).Order(ent.Asc(identityuser.FieldID)).IDs(ctx)
}
