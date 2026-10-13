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
	// A loose index scan over (container_id, scope_id) reads one entry per scope.
	rows, err := s.Client.QueryContext(ctx, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL)
SELECT id FROM scopes WHERE id IS NOT NULL LIMIT ?`, collectionID, collectionID, maxQueryScopes+1)
	if err != nil {
		return nil, err
	}
	scopes := []*string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		scopes = append(scopes, &id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(scopes) > maxQueryScopes {
		return nil, nil
	}
	scopes = append(scopes, nil)
	draftSQL, draftArgs := permissionSQL(ctx, "?", subject, "read_draft")
	readSQL, readArgs := permissionSQL(ctx, "?", subject, "read")
	access := &scopeAccess{}
	for _, scope := range scopes {
		match, matchArgs := "scope_id IS NULL", []any{collectionID}
		if scope != nil {
			match, matchArgs = "scope_id=?", []any{collectionID, *scope}
		}
		var row string
		err = s.scan(ctx, []any{&row}, "SELECT id FROM resources WHERE container_id=? AND "+match+" AND kind IN ('item','folder') AND deleted_at IS NULL LIMIT 1", matchArgs...)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var draft, read bool
		args := append(append(append([]any{row}, draftArgs...), row), readArgs...)
		if err = s.scan(ctx, []any{&draft, &read}, "SELECT "+draftSQL+","+readSQL, args...); err != nil {
			return nil, err
		}
		if scope == nil {
			access.nullRows = true
		} else {
			access.live++
		}
		switch {
		case scope == nil:
			access.nullDraft, access.nullReadable = draft, read || draft
		case draft:
			access.draft = append(access.draft, *scope)
		case read:
			access.read = append(access.read, *scope)
		}
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
