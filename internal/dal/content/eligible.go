package content

import (
	"context"
	"database/sql"
	"strings"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
)

// maxScopes bounds the per-scope decisions; a collection with more ACL scopes
// (many items with unique permissions) is checked row by row.
const maxScopes = 64

// Scopes is what a reader may do in each ACL scope of a collection's items
// and folders. A nil scope stands for rows without one.
type Scopes struct {
	draft, read             []string
	nullDraft, nullReadable bool
	// live counts the scopes holding live rows; nullRows is whether rows
	// without a scope exist.
	live     int
	nullRows bool
}

// Scopes decides, once per ACL scope, whether a reader reads a collection's
// items there with or without drafts. Permissions depend only on a row's
// scope, its workspace and whether it is content, so one live row per scope
// stands for all of them. It returns nil for collections with too many
// scopes.
func (r *Repo) Scopes(ctx context.Context, collectionID string, reader access.Principal) (*Scopes, error) {
	// One statement: a loose index scan over (container_id, scope_id) reads
	// one entry per scope, one live row stands for each scope (and for rows
	// without one), and the reader's access is decided on it.
	draftSQL, draftArgs := r.acl.Resource("reps.rep", reader, access.ReadDraft)
	readSQL, readArgs := r.acl.Resource("reps.rep", reader, access.Read)
	live := `(SELECT r.id FROM resources r WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND r.scope_id`
	args := []any{collectionID, collectionID, maxScopes + 1, collectionID, collectionID}
	args = append(append(args, draftArgs...), readArgs...)
	out := &Scopes{}
	listed := 0
	err := r.each(ctx, func(rows scan.Rows) error {
		var scope sql.NullString
		var held, draft, read bool
		if err := rows.Scan(&scope, &held, &draft, &read); err != nil {
			return err
		}
		if scope.Valid {
			listed++
		}
		if !held {
			return nil
		}
		switch {
		case !scope.Valid:
			out.nullRows, out.nullDraft, out.nullReadable = true, draft, read || draft
		case draft:
			out.live++
			out.draft = append(out.draft, scope.String)
		case read:
			out.live++
			out.read = append(out.read, scope.String)
		default:
			out.live++
		}
		return nil
	}, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL),
listed AS (SELECT id FROM scopes WHERE id IS NOT NULL LIMIT ?),
reps AS (SELECT l.id AS scope,`+live+`=l.id LIMIT 1) AS rep FROM listed l
 UNION ALL SELECT NULL,`+live+` IS NULL LIMIT 1))
SELECT scope,rep IS NOT NULL,`+draftSQL+`,`+readSQL+` FROM reps`, args...)
	if err != nil {
		return nil, err
	}
	if listed > maxScopes {
		return nil, nil
	}
	return out, nil
}

// inScopes tests r.scope_id against a list of scopes, and rows without one.
func inScopes(ids []string, null bool) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	text := "FALSE"
	if len(ids) > 0 {
		text = "r.scope_id IN (" + marks(len(ids)) + ")"
	}
	if null {
		text = "(" + text + " OR r.scope_id IS NULL)"
	}
	return text, args
}

// sql returns the surface a row is read on and the condition for readable
// rows, with their arguments, for the surface asked: auto reads drafts
// where the reader may, head requires drafts, published needs read.
func (a *Scopes) sql(surface string) (string, string, [2][]any) {
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

// Uniform reports the one surface the reader reads every live row on, when
// they read all of them the same way: then counts of the whole collection
// are theirs too.
func (a *Scopes) Uniform(surface string) (string, bool) {
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

// eligible returns the statement selecting q's eligible rows and its
// arguments, a fresh slice the caller may extend.
func (r *Repo) eligible(ctx context.Context, q *Query) (string, []any, error) {
	if !q.built {
		scopes, err := r.Scopes(ctx, q.collection, q.reader)
		if err != nil {
			return "", nil, err
		}
		if scopes == nil {
			q.sql, q.args = r.rows(q)
		} else {
			// Permissions follow ACL scopes, and a collection has few: each
			// scope is decided once instead of probing grants for every row.
			selected, readable, scopeArgs := scopes.sql(q.surface)
			if !q.publishes {
				// Without publishing, head content is the published surface.
				selected, scopeArgs[0] = "'published'", nil
			}
			q.sql = "SELECT r.id," + q.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND " + readable + q.conds
			q.args = append(append(append(append(append([]any{}, q.rankArgs...), scopeArgs[0]...), q.collection), scopeArgs[1]...), q.condArgs...)
		}
		q.built = true
	}
	return q.sql, append([]any{}, q.args...), nil
}

// rows selects q's eligible rows checking permissions row by row: for
// collections with many ACL scopes, and for unions of collections.
func (r *Repo) rows(q *Query) (string, []any) {
	return r.rowsIn(q, []any{q.collection})
}

// rowsIn selects the eligible rows of q's shape in several collections.
func (r *Repo) rowsIn(q *Query, collections []any) (string, []any) {
	selected, surfaceArgs := r.acl.Surface("r.id", q.reader, q.surface)
	args := append(append(append([]any{}, q.rankArgs...), surfaceArgs...), collections...)
	var text strings.Builder
	text.WriteString("SELECT r.id," + q.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id IN (" + marks(len(collections)) + ") AND r.kind IN ('item','folder')" + q.conds)
	args = append(args, q.condArgs...)
	// Cheap index filters come first, so the ACL probe runs only on rows
	// that already match.
	action := access.Read
	if q.surface == "head" {
		action = access.ReadDraft
	}
	allowed, allowedArgs := r.acl.Resource("r.id", q.reader, action)
	text.WriteString(" AND " + allowed)
	return text.String(), append(args, allowedArgs...)
}
