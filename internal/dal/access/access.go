// Package access builds the SQL expressions that decide what a principal may
// see and change: resource grants at the nearest exclusive scope, workspace
// ownership, organization roles and the scopes and resource limit of the
// credential a request runs with. Drivers and content queries embed the expressions; every
// expression uses ? placeholders and returns its arguments in order.
package access

import (
	"encoding/json"
	"slices"
	"strings"

	"papergo/internal/dal/dialect"
)

// Credential is the access token a request runs with. Its scopes cap what
// the principal's roles allow, and its resources, when any, the content it
// reaches.
type Credential struct {
	ID     string
	Scopes []string
	// Resources limits content to these workspaces, lists and libraries:
	// what is in them, and them. Empty is unlimited.
	Resources []string
}

// Limited reports whether the credential reaches only some content.
func (c *Credential) Limited() bool { return c != nil && len(c.Resources) > 0 }

// Principal is who asks: a user and, for token requests, the credential.
type Principal struct {
	Subject    string
	Credential *Credential
}

// Action is what a principal asks to do with a resource.
type Action string

const (
	Read      Action = "read"
	ReadDraft Action = "read_draft"
	Write     Action = "write"
	Publish   Action = "publish"
	Manage    Action = "manage"
)

// Implied lists the grant actions that allow a.
func (a Action) Implied() []string {
	switch a {
	case Read:
		return []string{"read", "read_draft", "write", "publish", "manage"}
	case ReadDraft:
		return []string{"read_draft", "write", "publish", "manage"}
	case Write:
		return []string{"write", "manage"}
	case Publish:
		return []string{"publish", "manage"}
	default:
		return []string{string(a)}
	}
}

// Dialect writes the expressions for one engine.
type Dialect struct{ dialect.Dialect }

var (
	SQLite   = Dialect{dialect.SQLite}
	Postgres = Dialect{dialect.Postgres}
)

// For returns the access expressions of an engine.
func For(d dialect.Dialect) Dialect { return Dialect{d} }

// GroupIDs selects the groups a user belongs to, nested groups included.
// UNION deduplicates diamonds and bounds traversal even if an invalid cycle
// is inserted outside the API. It binds the subject once.
func (d Dialect) GroupIDs() string {
	return `WITH RECURSIVE effective_groups(id) AS (
 SELECT group_id FROM identity_memberships WHERE principal_type='user' AND principal_id=?
 UNION SELECT m.group_id FROM identity_memberships m JOIN effective_groups e
 ON m.principal_type='group' AND m.principal_id=e.id
) SELECT id FROM effective_groups`
}

// principal tests that the grant or assignment row alias names the subject
// or one of its groups, keeping user and group ID namespaces distinct.
func (d Dialect) principal(alias, subject string) (string, []any) {
	return `((` + alias + `.principal_type='user' AND ` + alias + `.principal_id=?) OR (` + alias + `.principal_type='group' AND ` + alias + `.principal_id IN (` + d.GroupIDs() + `)))`, []any{subject, subject}
}

// Scope tests that the principal holds an organization scope.
func (d Dialect) Scope(p Principal, scope string) (string, []any) {
	if p.Credential != nil && !slices.Contains(p.Credential.Scopes, scope) {
		return "FALSE", nil
	}
	query, args := d.ScopeExpression(p, "?")
	return query, append(args, scope)
}

// ScopeExpression tests that the principal holds the scope a trusted SQL
// expression names. Callers append the expression's own arguments after
// the returned ones.
func (d Dialect) ScopeExpression(p Principal, expression string) (string, []any) {
	principal, args := d.principal("acl_assignment", p.Subject)
	restriction := ""
	if p.Credential != nil {
		if len(p.Credential.Scopes) == 0 {
			return "FALSE", nil
		}
		marks := make([]string, len(p.Credential.Scopes))
		for i, scope := range p.Credential.Scopes {
			marks[i] = "?"
			args = append(args, scope)
		}
		restriction = " AND acl_scope.value IN (" + strings.Join(marks, ",") + ")"
	}
	roles := `(EXISTS (SELECT 1 FROM identity_users acl_user WHERE acl_user.id=? AND acl_user.active) AND EXISTS (SELECT 1 FROM identity_assignments acl_assignment JOIN identity_roles acl_role ON acl_role.id=acl_assignment.role_id CROSS JOIN ` + d.Elements("acl_role.scopes", "acl_scope") + ` WHERE ` + principal + restriction + ` AND acl_scope.value=` + expression + `))`
	args = append([]any{p.Subject}, args...)
	if p.Credential == nil {
		return roles, args
	}
	// A queued operation retains the credential ID and cap, so it can use the
	// same live integration grant without storing its secret or trusting
	// flags. The scope expression may contain placeholders; callers append
	// those after these arguments, so it stays in one outer comparison.
	roles = `(EXISTS (SELECT 1 FROM identity_users acl_user WHERE acl_user.id=? AND acl_user.active) AND EXISTS (SELECT 1 FROM identity_assignments acl_assignment JOIN identity_roles acl_role ON acl_role.id=acl_assignment.role_id CROSS JOIN ` + d.Elements("acl_role.scopes", "acl_scope") + ` WHERE ` + principal + restriction + ` AND acl_scope.value=acl_requested.value))`
	expires := `datetime(acl_token.expires_at)>CURRENT_TIMESTAMP`
	requested := d.JSONArg()
	if d.Postgres {
		expires = `acl_token.expires_at>CURRENT_TIMESTAMP`
	}
	integration := `EXISTS(SELECT 1 FROM access_tokens acl_token JOIN identity_users acl_holder ON acl_holder.id=acl_token.subject CROSS JOIN ` + d.Elements("acl_token.scopes", "acl_token_scope") + ` WHERE acl_token.id=? AND acl_token.subject=? AND acl_token.integration AND acl_token.revoked_at IS NULL AND (acl_token.expires_at IS NULL OR ` + expires + `) AND acl_holder.active AND acl_holder.deleted_at IS NULL AND acl_token_scope.value=acl_requested.value)`
	args = append(args, p.Credential.ID, p.Subject)
	raw, _ := json.Marshal(p.Credential.Scopes)
	return `(EXISTS(SELECT 1 FROM ` + d.Elements(requested, "acl_requested") + ` WHERE (` + roles + ` OR ` + integration + `) AND acl_requested.value=` + expression + `))`, append([]any{string(raw)}, args...)
}

// grantee tests that the grant row alias names the principal, directly, by
// group or by workspace role.
func (d Dialect) grantee(alias, subject string) (string, []any) {
	principal, args := d.principal(alias, subject)
	query := `(` + principal + ` OR (` + alias + `.principal_type IN ('workspace_visitor','workspace_member','workspace_owner') AND EXISTS(SELECT 1 FROM grants membership
 WHERE membership.resource_id=` + alias + `.principal_id AND membership.principal_type='user' AND membership.principal_id=? AND membership.effect='allow'
 AND membership.action=CASE ` + alias + `.principal_type WHEN 'workspace_visitor' THEN 'read' WHEN 'workspace_member' THEN 'write' ELSE 'manage' END)))`
	return query, append(args, subject)
}

func marks(action Action) (string, []any) {
	actions := action.Implied()
	out := make([]string, len(actions))
	args := make([]any, len(actions))
	for i, a := range actions {
		out[i] = "?"
		args[i] = a
	}
	return strings.Join(out, ","), args
}

// module tests the organization scope that content of a kind needs: reading
// workspaces needs workspace.read, changing items and folders list.write and
// everything else list.read.
func (d Dialect) module(kindColumn string, p Principal, action Action) (string, []any) {
	scope := "list.read"
	if action == Write || action == Publish {
		scope = "list.write"
	}
	return d.ScopeExpression(p, `CASE WHEN `+kindColumn+`='workspace' THEN 'workspace.read' WHEN `+kindColumn+` IN ('item','folder') THEN '`+scope+`' ELSE 'list.read' END`)
}

// Resource tests that the principal may take action on the live resource
// idColumn names. Additive grants apply at the nearest exclusive scope;
// inherited resources have no ACL of their own.
func (d Dialect) Resource(idColumn string, p Principal, action Action) (string, []any) {
	grantee, args := d.grantee("g", p.Subject)
	actions, actionArgs := marks(action)
	args = append(args, actionArgs...)
	admin, adminArgs := d.Scope(p, "workspace.manage")
	module, moduleArgs := d.module("acl.kind", p, action)
	limit, limitArgs := within("acl", p, action)
	query := `EXISTS (SELECT 1 FROM resources acl JOIN resources workspace ON workspace.id=acl.workspace_id AND workspace.deleted_at IS NULL
 WHERE acl.id=` + idColumn + ` AND acl.deleted_at IS NULL AND (EXISTS(SELECT 1 FROM grants g WHERE g.resource_id=acl.scope_id AND ` + grantee + ` AND g.effect='allow' AND g.action IN (` + actions + `))
 OR EXISTS(SELECT 1 FROM grants owner WHERE owner.resource_id=acl.workspace_id AND owner.principal_type='user' AND owner.principal_id=? AND owner.action='manage' AND owner.effect='allow')
 OR ` + admin + `) AND ` + module + limit + `)`
	args = append(args, p.Subject)
	args = append(args, adminArgs...)
	args = append(args, moduleArgs...)
	return query, append(args, limitArgs...)
}

// within tests that the resources row alias is inside the credential's
// resource limit: a listed workspace, list or library, or what they hold. A
// listed collection's workspace may be read, so clients can reach the
// collection through it, but nothing more. It is empty without a limit.
func within(alias string, p Principal, action Action) (string, []any) {
	if !p.Credential.Limited() {
		return "", nil
	}
	marks := make([]string, len(p.Credential.Resources))
	ids := make([]any, len(p.Credential.Resources))
	for i, id := range p.Credential.Resources {
		marks[i], ids[i] = "?", id
	}
	set := `(` + strings.Join(marks, ",") + `)`
	// Workspaces have no container: coalesce keeps the test false, not null,
	// where it is selected as a flag.
	query := ` AND COALESCE(` + alias + `.workspace_id IN ` + set + ` OR ` + alias + `.container_id IN ` + set + ` OR ` + alias + `.id IN ` + set
	args := append(append(append([]any{}, ids...), ids...), ids...)
	if action == Read {
		query += ` OR (` + alias + `.kind='workspace' AND ` + alias + `.id IN (SELECT limited.workspace_id FROM resources limited WHERE limited.id IN ` + set + `))`
		args = append(args, ids...)
	}
	return query + `, FALSE)`, args
}

// Rows tests that the principal may take action on the resources row alias.
// It filters by the row's own scope_id, which the browse indexes cover, so
// unreadable rows are skipped without a table lookup. The row's liveness is
// the caller's to test.
func (d Dialect) Rows(alias string, p Principal, action Action) (string, []any) {
	grantee, args := d.grantee("g", p.Subject)
	actions, actionArgs := marks(action)
	args = append(args, actionArgs...)
	args = append(args, p.Subject)
	admin, adminArgs := d.Scope(p, "workspace.manage")
	args = append(args, adminArgs...)
	module, moduleArgs := d.module(alias+".kind", p, action)
	args = append(args, moduleArgs...)
	limit, limitArgs := within(alias, p, action)
	args = append(args, limitArgs...)
	return `((` + alias + `.scope_id IN (SELECT g.resource_id FROM grants g WHERE ` + grantee + ` AND g.effect='allow' AND g.action IN (` + actions + `)) OR ` + alias + `.workspace_id IN (SELECT g.resource_id FROM grants g JOIN resources w ON w.id=g.resource_id WHERE g.principal_type='user' AND g.principal_id=? AND g.effect='allow' AND g.action='manage' AND w.kind='workspace' AND w.deleted_at IS NULL) OR ` + admin + `) AND ` + module + limit + `)`, args
}

// Publishes tests whether the collection of the content row idColumn names
// publishes explicitly, which is when its items keep a separate head
// surface.
func (d Dialect) Publishes(idColumn string) string {
	return "EXISTS(SELECT 1 FROM resources si JOIN resources sc ON sc.id=si.container_id WHERE si.id=" + idColumn + " AND sc.publishing_enabled)"
}

// Surface selects the surface the principal reads the content row idColumn
// on: head, published or auto, the head when the principal may read drafts.
// Collections without publishing keep one surface, the published one, which
// is also their head.
func (d Dialect) Surface(idColumn string, p Principal, surface string) (string, []any) {
	switch surface {
	case "published":
		return "'published'", nil
	case "head":
		return "CASE WHEN " + d.Publishes(idColumn) + " THEN 'head' ELSE 'published' END", nil
	}
	query, args := d.Resource(idColumn, p, ReadDraft)
	return "CASE WHEN " + d.Publishes(idColumn) + " AND " + query + " THEN 'head' ELSE 'published' END", args
}

// Visible tests that the content row idColumn names has the surface the
// principal reads. When an item has both surfaces either selection is
// visible, so the cheap index probes short-circuit the read_draft walk.
func (d Dialect) Visible(idColumn string, p Principal, surface string) (string, []any) {
	selected, args := d.Surface(idColumn, p, surface)
	both := ""
	if surface != "head" && surface != "published" {
		both = ` OR EXISTS (SELECT 1 FROM item_surfaces vh WHERE vh.item_id=vr.id AND vh.surface='head') AND EXISTS (SELECT 1 FROM item_surfaces vp WHERE vp.item_id=vr.id AND vp.surface='published')`
	}
	return `EXISTS (SELECT 1 FROM resources vr WHERE vr.id=` + idColumn + ` AND (vr.kind<>'item'` + both + ` OR EXISTS (SELECT 1 FROM item_surfaces vs WHERE vs.item_id=vr.id AND vs.surface=` + selected + `)))`, args
}
