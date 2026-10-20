package identity

import (
	"context"
	"errors"
	"papergo/internal/dal"
)

var (
	ErrForbidden          = errors.New("identity access denied")
	ErrNotFound           = errors.New("identity entry not found")
	ErrPreconditionFailed = errors.New("identity precondition failed")
	ErrLastAdministrator  = errors.New("an organization needs at least one administrator")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }
func invalid(message string) error       { return &ValidationError{message} }

const AdministratorRole = "administrator"
const MemberRole = "member"

type Scope struct {
	Name             string `json:"name"`
	GrantedToMembers bool   `json:"granted_to_members"`
}

// Module scopes permit operations. Resource ACLs still decide which content a
// caller can access; workspace.manage is the explicit organization-wide override.
var Catalog = []Scope{
	{"workspace.read", true}, {"workspace.create", true}, {"workspace.manage", false},
	{"list.read", true}, {"list.write", true},
	{"document.read", true}, {"document.write", true},
	{"contentType.read", true}, {"contentType.manage", false},
	{"taxonomy.read", true}, {"taxonomy.manage", false},
	{"user.read", true}, {"user.manage", false},
	{"group.read", true}, {"group.manage", false},
	{"role.read", false}, {"role.manage", false},
	{"organization.manage", false}, {"audit.read", false},
}

func RequireScope(ctx context.Context, store *dal.Store, subject, scope string) error {
	if !credentialAllows(ctx, scope) {
		return ErrForbidden
	}
	scopes, err := EffectiveScopes(ctx, store, subject)
	if err != nil {
		return err
	}
	for _, v := range scopes {
		if v == scope {
			return nil
		}
	}
	return ErrForbidden
}
func EffectiveScopes(ctx context.Context, store *dal.Store, subject string) ([]string, error) {
	tokenID := ""
	if token, ok := AccessTokenContext(ctx); ok {
		tokenID = token.ID
	}
	scopes, err := store.Directory().EffectiveScopes(ctx, subject, tokenID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, scope := range scopes {
		if credentialAllows(ctx, scope) {
			out = append(out, scope)
		}
	}
	return out, nil
}
