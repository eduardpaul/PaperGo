package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
)

// MaxScopes bounds the per-scope decisions; a collection with more ACL scopes
// is checked row by row.
var MaxScopes = 4096

// inlineScopes is how many scopes a statement names as placeholders; longer
// lists travel as one JSON array.
const inlineScopes = 64

// Scopes is what a reader may do in each ACL scope of a collection's items
// and folders: every scope a row of the collection has, live or deleted, and
// the collection's own, which rows created at its top join.
type Scopes struct {
	// draft scopes are read with drafts, read scopes without, hidden
	// scopes not at all.
	draft, read, hidden []string
	// nullRows is whether rows without a scope exist; nullDraft and
	// nullReadable are what the reader may do with them.
	nullRows, nullDraft, nullReadable bool
}

// scopeKey names a cached decision: a collection and its reader.
type scopeKey struct{ collection, subject string }

// maxCachedScopes bounds the scopes the cached decisions hold together.
const maxCachedScopes = 200000

// keptScopes is a decision and the access revision it was made under.
type keptScopes struct {
	token  string
	scopes *Scopes
	size   int
}

// ScopeCache keeps the scope decisions of collections and their readers for
// as long as the access revision each was made under is current: the
// organization's and that of the collection's workspace. A store and its
// transactions share one.
type ScopeCache struct {
	mu      sync.Mutex
	size    int
	entries map[scopeKey]keptScopes
}

// NewScopeCache returns an empty cache.
func NewScopeCache() *ScopeCache { return &ScopeCache{entries: map[scopeKey]keptScopes{}} }

// get returns the decision made for key under the access revision token.
func (c *ScopeCache) get(token string, key scopeKey) (*Scopes, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept, ok := c.entries[key]
	if !ok || kept.token != token {
		return nil, false
	}
	return kept.scopes, true
}

// put keeps the decision made for key under the access revision token in
// place of an earlier one. When the decisions would hold too many scopes,
// all are dropped.
func (c *ScopeCache) put(token string, key scopeKey, s *Scopes) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size := 1
	if s != nil {
		size += len(s.draft) + len(s.read) + len(s.hidden)
	}
	if kept, ok := c.entries[key]; ok {
		c.size -= kept.size
	}
	if c.size+size > maxCachedScopes {
		c.size, c.entries = 0, map[scopeKey]keptScopes{}
	}
	c.size += size
	c.entries[key] = keptScopes{token, s, size}
}

// Scopes decides, once per ACL scope, whether a reader reads a collection's
// items there with or without drafts. Permissions depend only on a row's
// scope, its workspace and its collection, so the decision needs no row. It
// returns nil for collections with too many scopes.
//
// Decisions are kept per collection and reader under the access revisions
// of the organization and of the collection's workspace: they read the
// directory, the workspace's grants and the scopes its rows have, and every
// change to those replaces one of the revisions. A row created with
// inherited permissions, or moved or restored within its scope, leaves them
// alone: the collection already has its scope. A credential's reach can end
// with time, so its decisions are made every time.
func (r *Repo) Scopes(ctx context.Context, collectionID string, reader access.Principal) (*Scopes, error) {
	cached := r.scopes != nil && reader.Credential == nil
	key := scopeKey{collectionID, reader.Subject}
	token := ""
	if cached {
		// The revision is read before the decisions, so decisions never
		// sit under a revision older than what they read.
		var err error
		if token, err = access.Revision(ctx, r.exec, r.d, collectionID); err != nil {
			return nil, err
		}
		if out, ok := r.scopes.get(token, key); ok {
			return out, nil
		}
	}
	// One statement: a loose index scan over (container_id, scope_id) reads
	// one entry per scope, and the reader's access is decided for each, for
	// the collection's own scope and for rows without one.
	draftSQL, draftArgs := r.acl.Content("decided.id", collectionID, reader, access.ReadDraft)
	readSQL, readArgs := r.acl.Content("decided.id", collectionID, reader, access.Read)
	args := []any{collectionID, collectionID, MaxScopes + 1, collectionID, collectionID}
	args = append(append(args, draftArgs...), readArgs...)
	out := &Scopes{}
	listed := 0
	err := r.each(ctx, func(rows scan.Rows) error {
		var scope sql.NullString
		var draft, read bool
		if err := rows.Scan(&scope, &listed, &out.nullRows, &draft, &read); err != nil {
			return err
		}
		switch {
		case !scope.Valid:
			out.nullDraft, out.nullReadable = draft, read || draft
		case draft:
			out.draft = append(out.draft, scope.String)
		case read:
			out.read = append(out.read, scope.String)
		default:
			out.hidden = append(out.hidden, scope.String)
		}
		return nil
	}, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL),
listed AS (SELECT id FROM scopes WHERE id IS NOT NULL LIMIT ?),
decided AS (SELECT id FROM listed UNION SELECT scope_id FROM resources WHERE id=? UNION SELECT NULL)
SELECT id,(SELECT count(*) FROM listed),EXISTS(SELECT 1 FROM resources WHERE container_id=? AND scope_id IS NULL),`+draftSQL+`,`+readSQL+` FROM decided`, args...)
	if err != nil {
		return nil, err
	}
	if listed > MaxScopes {
		out = nil
	}
	if cached {
		r.scopes.put(token, key, out)
	}
	return out, nil
}

// inScopes tests r.scope_id against a list of scopes, and rows without one.
func (r *Repo) inScopes(ids []string, null bool) (string, []any) {
	text, args := "FALSE", []any(nil)
	switch {
	case len(ids) > inlineScopes:
		raw, _ := json.Marshal(ids)
		text, args = "r.scope_id IN (SELECT value FROM "+r.d.Elements(r.d.JSONArg(), "scoped")+")", []any{string(raw)}
	case len(ids) > 0:
		args = make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		text = "r.scope_id IN (" + marks(len(ids)) + ")"
	}
	if null {
		text = "(" + text + " OR r.scope_id IS NULL)"
	}
	return text, args
}

// drafts is the condition of the rows the reader reads with drafts. A
// reader who reads rows without a scope does so as a workspace owner or
// administrator, whom no scope's grants hold back: they read every row.
func (r *Repo) drafts(a *Scopes) (string, []any) {
	if a.nullDraft {
		return "TRUE", nil
	}
	return r.inScopes(a.draft, false)
}

// readable is the condition of the rows the reader reads at all.
func (r *Repo) readable(a *Scopes) (string, []any) {
	if a.nullReadable {
		return "TRUE", nil
	}
	return r.inScopes(append(append([]string{}, a.draft...), a.read...), false)
}

// published is the condition of the rows the reader reads without drafts.
func (r *Repo) published(a *Scopes) (string, []any) {
	if a.nullDraft {
		return "FALSE", nil
	}
	return r.inScopes(a.read, a.nullReadable)
}

// scoped returns the surface a row is read on and the condition for readable
// rows, with their arguments, for the surface asked: auto reads drafts
// where the reader may, head requires drafts, published needs read.
func (r *Repo) scoped(a *Scopes, surface string) (string, string, [2][]any) {
	draft, draftArgs := r.drafts(a)
	readable, readableArgs := r.readable(a)
	switch surface {
	case "head":
		return "'head'", draft, [2][]any{nil, draftArgs}
	case "published":
		return "'published'", readable, [2][]any{nil, readableArgs}
	}
	return "CASE WHEN " + draft + " THEN 'head' ELSE 'published' END", readable, [2][]any{draftArgs, readableArgs}
}

// Uniform reports the one surface the reader reads every live row of the
// collection on, when they read all of them the same way: then counts of
// the whole collection are theirs too. Scopes the reader reads another way
// may hold no live row; only then are rows looked at.
func (r *Repo) Uniform(ctx context.Context, collectionID string, a *Scopes, surface string) (string, bool, error) {
	// none reports that no live row sits in the scopes, or without one.
	none := func(null bool, lists ...[]string) (bool, error) {
		ids := []string{}
		for _, list := range lists {
			ids = append(ids, list...)
		}
		null = null && a.nullRows
		if len(ids) == 0 && !null {
			return true, nil
		}
		in, args := r.inScopes(ids, null)
		var found bool
		err := r.scan(ctx, []any{&found}, "SELECT EXISTS(SELECT 1 FROM resources r WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND "+in+")", append([]any{collectionID}, args...)...)
		return !found, err
	}
	if surface != "published" {
		ok, err := none(!a.nullDraft, a.read, a.hidden)
		if err != nil || ok || surface == "head" {
			return "head", ok, err
		}
	}
	// Published everywhere: readable rows only, and for auto none of them
	// with drafts.
	others := [][]string{a.hidden}
	null := !a.nullReadable
	if surface != "published" {
		others, null = append(others, a.draft), null || a.nullDraft
	}
	ok, err := none(null, others...)
	return "published", ok, err
}

// eligible returns the statement selecting q's eligible rows, with the ID
// of the surface each is read on, and its arguments, a fresh slice the
// caller may extend.
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
			selected, readable, scopeArgs := r.scoped(scopes, q.surface)
			if !q.publishes {
				// Without publishing, head content is the published surface.
				selected, scopeArgs[0] = "'published'", nil
			}
			q.sql = "SELECT r.id,p.id AS surface_id," + q.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND " + readable + q.conds
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
	text.WriteString("SELECT r.id,p.id AS surface_id," + q.ranks + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id IN (" + marks(len(collections)) + ") AND r.kind IN ('item','folder')" + q.conds)
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
