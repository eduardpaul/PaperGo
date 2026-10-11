package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
const MaxGroupDepth = 10

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
	{"organization.manage", false},
}

type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// GroupIDsSQL expands nested memberships upward. UNION deduplicates diamonds
// and bounds traversal even if an invalid cycle is inserted outside the API.
const GroupIDsSQL = `WITH RECURSIVE effective_groups(id) AS (
 SELECT group_id FROM identity_memberships WHERE principal_type='user' AND principal_id=?
 UNION SELECT m.group_id FROM identity_memberships m JOIN effective_groups e
 ON m.principal_type='group' AND m.principal_id=e.id
) SELECT id FROM effective_groups`

// PrincipalSQL binds two occurrences of subject, and keeps user/group ID
// namespaces distinct. It is shared by resource ACLs and organization roles.
func PrincipalSQL(alias string, subject string) (string, []any) {
	return `((` + alias + `.principal_type='user' AND ` + alias + `.principal_id=?) OR (` + alias + `.principal_type='group' AND ` + alias + `.principal_id IN (` + GroupIDsSQL + `)))`, []any{subject, subject}
}

func ScopeSQL(ctx context.Context, subject, scope string) (string, []any) {
	if !credentialAllows(ctx, scope) {
		return "0", nil
	}
	query, args := ScopeExpressionSQL(ctx, subject, "?")
	return query, append(args, scope)
}

// ScopeExpressionSQL accepts only a trusted SQL expression built by the caller.
func ScopeExpressionSQL(ctx context.Context, subject, expression string) (string, []any) {
	principal, args := PrincipalSQL("a", subject)
	restriction := ""
	if token, limited := ctx.Value(restTokenContextKey{}).(RESTTokenIdentity); limited {
		if len(token.Scopes) == 0 {
			return "0", nil
		}
		marks := make([]string, len(token.Scopes))
		for i, scope := range token.Scopes {
			marks[i] = "?"
			args = append(args, scope)
		}
		restriction = " AND sc.value IN (" + strings.Join(marks, ",") + ")"
	}
	return `(EXISTS (SELECT 1 FROM identity_users u WHERE u.id=? AND u.active=1) AND EXISTS (SELECT 1 FROM identity_assignments a JOIN identity_roles r ON r.id=a.role_id JOIN json_each(r.scopes) sc WHERE ` + principal + restriction + ` AND sc.value=` + expression + `))`, append([]any{subject}, args...)
}

func RequireScope(ctx context.Context, q Querier, subject, scope string) error {
	if !credentialAllows(ctx, scope) {
		return ErrForbidden
	}
	filter, args := ScopeSQL(ctx, subject, scope)
	var allowed bool
	if err := q.QueryRowContext(ctx, "SELECT "+filter, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func EffectiveScopes(ctx context.Context, q Querier, subject string) ([]string, error) {
	filter, args := PrincipalSQL("a", subject)
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT sc.value FROM identity_assignments a JOIN identity_roles r ON r.id=a.role_id JOIN json_each(r.scopes) sc WHERE EXISTS(SELECT 1 FROM identity_users u WHERE u.id=? AND u.active=1) AND `+filter+` ORDER BY sc.value`, append([]any{subject}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			return nil, err
		}
		if credentialAllows(ctx, scope) {
			out = append(out, scope)
		}
	}
	return out, rows.Err()
}
