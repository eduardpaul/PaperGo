package dms

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// maxQueryScopes bounds the per-scope decisions; a collection with more ACL
// scopes (many items with unique permissions) is checked row by row.
const maxQueryScopes = 64

// scopeAccess is what the caller may do in each ACL scope of a collection's
// items and folders. A nil scope stands for rows without one.
type scopeAccess struct {
	draft, read             []string
	nullDraft, nullReadable bool
	// live counts the scopes holding live rows; nullRows is whether rows
	// without a scope exist.
	live     int
	nullRows bool
}

// collectionScopes decides, once per ACL scope, whether the caller reads the
// collection's items there with or without drafts. Permissions depend only on
// a row's scope, its workspace and whether it is content, so one live row per
// scope stands for all of them. It returns nil for collections with too many
// scopes.
func (s *Service) collectionScopes(ctx context.Context, collectionID, subject string) (*scopeAccess, error) {
	// One statement: a loose index scan over (container_id, scope_id) reads one
	// entry per scope, one live row stands for each scope (and for rows without
	// one), and the caller's access is decided on it.
	draftSQL, draftArgs := permissionSQL(ctx, "reps.rep", subject, "read_draft")
	readSQL, readArgs := permissionSQL(ctx, "reps.rep", subject, "read")
	live := `(SELECT r.id FROM resources r WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND r.scope_id`
	args := []any{collectionID, collectionID, maxQueryScopes + 1, collectionID, collectionID}
	args = append(append(args, draftArgs...), readArgs...)
	rows, err := s.Client.QueryContext(ctx, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL),
listed AS (SELECT id FROM scopes WHERE id IS NOT NULL LIMIT ?),
reps AS (SELECT l.id AS scope,`+live+`=l.id LIMIT 1) AS rep FROM listed l
 UNION ALL SELECT NULL,`+live+` IS NULL LIMIT 1))
SELECT scope,rep IS NOT NULL,`+draftSQL+`,`+readSQL+` FROM reps`, args...)
	if err != nil {
		return nil, err
	}
	access := &scopeAccess{}
	listed := 0
	for rows.Next() {
		var scope sql.NullString
		var held, draft, read bool
		if err = rows.Scan(&scope, &held, &draft, &read); err != nil {
			break
		}
		if scope.Valid {
			listed++
		}
		if !held {
			continue
		}
		switch {
		case !scope.Valid:
			access.nullRows, access.nullDraft, access.nullReadable = true, draft, read || draft
		case draft:
			access.live++
			access.draft = append(access.draft, scope.String)
		case read:
			access.live++
			access.read = append(access.read, scope.String)
		default:
			access.live++
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if listed > maxQueryScopes {
		return nil, nil
	}
	return access, nil
}

// inScopes tests r.scope_id against a list of scopes, and rows without one.
func inScopes(ids []string, null bool) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i], args[i] = "?", id
	}
	text := "r.scope_id IN (" + strings.Join(marks, ",") + ")"
	if null {
		text = "(" + text + " OR r.scope_id IS NULL)"
	}
	return text, args
}

// sql returns the surface a row is read on and the condition for readable rows,
// with their arguments, for the surface asked: auto reads drafts where the
// caller may, head requires drafts, published needs read.
func (a *scopeAccess) sql(surface string) (string, string, [2][]any) {
	draft, draftArgs := inScopes(a.draft, a.nullDraft)
	all := append(append([]string{}, a.draft...), a.read...)
	readable, readableArgs := inScopes(all, a.nullReadable)
	switch surface {
	case "head":
		return "'head'", draft, [2][]any{nil, draftArgs}
	case "published":
		return "'published'", readable, [2][]any{nil, readableArgs}
	}
	return "CASE WHEN " + draft + " THEN 'head' ELSE 'published' END", readable, [2][]any{draftArgs, readableArgs}
}

// uniform reports the one surface the caller reads every live row on, when
// they read all of them the same way: then counts of the whole collection are
// theirs too.
func (a *scopeAccess) uniform(surface string) (string, bool) {
	allDraft := len(a.draft) == a.live && (!a.nullRows || a.nullDraft)
	allRead := len(a.draft)+len(a.read) == a.live && (!a.nullRows || a.nullReadable)
	switch surface {
	case "head":
		return "head", allDraft
	case "published":
		return "published", allRead
	}
	if allDraft {
		return "head", true
	}
	if len(a.draft) == 0 && allRead && !a.nullDraft {
		return "published", true
	}
	return "", false
}

// eligibleSQL holds a collection query's parts until a statement needs its
// eligible rows; then the caller's ACL scopes are decided once.
type eligibleSQL struct {
	collection, subject, surface string
	publishes                    bool
	ranks                        string
	rankArgs                     []any
	conditions                   string
	conditionArgs                []any
	built                        bool
	sql                          string
	args                         []any
}

// eligibleRows returns the SQL selecting q's eligible rows and its arguments,
// a fresh slice the caller may extend.
func (s *Service) eligibleRows(ctx context.Context, q compiledQuery) (string, []any, error) {
	e := q.eligible
	if e == nil {
		return q.sql, append([]any{}, q.args...), nil
	}
	if !e.built {
		access, err := s.collectionScopes(ctx, e.collection, e.subject)
		if err != nil {
			return "", nil, err
		}
		if access == nil {
			e.sql, e.args = e.rows(ctx)
		} else {
			// Permissions follow ACL scopes, and a collection has few: each
			// scope is decided once instead of probing grants for every row.
			selected, readable, scopeArgs := access.sql(e.surface)
			if !e.publishes {
				// Without publishing, head content is the published surface.
				selected, scopeArgs[0] = "'published'", nil
			}
			e.sql = "SELECT r.id," + e.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND " + readable + e.conditions
			e.args = append(append(append(append(append([]any{}, e.rankArgs...), scopeArgs[0]...), e.collection), scopeArgs[1]...), e.conditionArgs...)
		}
		e.built = true
	}
	return e.sql, append([]any{}, e.args...), nil
}

// rows selects the eligible rows checking permissions row by row: for
// collections with many ACL scopes, and for smart folders, whose collections
// share one statement shape.
func (e *eligibleSQL) rows(ctx context.Context) (string, []any) {
	selected, surfaceArgs := surfaceSQL(ctx, "r.id", e.subject, e.surface)
	args := append(append(append([]any{}, e.rankArgs...), surfaceArgs...), e.collection)
	text := "SELECT r.id," + e.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder')" + e.conditions
	args = append(args, e.conditionArgs...)
	// SQLite evaluates these terms in order: cheap index filters first, so the
	// ACL probe runs only on rows that already match.
	action := "read"
	if e.surface == "head" {
		action = "read_draft"
	}
	acl, aclArgs := permissionSQL(ctx, "r.id", e.subject, action)
	return text + " AND " + acl, append(args, aclArgs...)
}
