package content

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
)

// SearchScopes decides, once per permission scope of the organization's
// items and folders, whether a reader reads drafts there or published
// content. Permissions depend only on a row's scope, its workspace and
// whether it is content, so one live row stands for each scope. The empty
// scope stands for rows without one.
func (r *Repo) SearchScopes(ctx context.Context, reader access.Principal) (draft, read []string, err error) {
	draftSQL, draftArgs := r.acl.Resource("reps.rep", reader, access.ReadDraft)
	readSQL, readArgs := r.acl.Resource("reps.rep", reader, access.Read)
	live := `SELECT r.id FROM resources r WHERE r.kind IN ('item','folder') AND r.deleted_at IS NULL AND r.scope_id`
	draft, read = []string{}, []string{}
	err = r.each(ctx, func(rows scan.Rows) error {
		var scope string
		var drafts, readable bool
		if err := rows.Scan(&scope, &drafts, &readable); err != nil {
			return err
		}
		if drafts {
			draft = append(draft, scope)
		}
		if drafts || readable {
			read = append(read, scope)
		}
		return nil
	}, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL),
reps AS (SELECT s.id AS scope, (`+live+`=s.id LIMIT 1) AS rep FROM scopes s WHERE s.id IS NOT NULL
 UNION ALL SELECT NULL, (`+live+` IS NULL LIMIT 1))
SELECT coalesce(scope,''), `+draftSQL+`, `+readSQL+` FROM reps WHERE rep IS NOT NULL`, append(draftArgs, readArgs...)...)
	return draft, read, err
}

// SearchState is what a reader may see of a live item or folder.
type SearchState struct {
	// Publishes is whether its collection keeps a separate head surface.
	Publishes bool
	// Head and Published are whether it has those surfaces.
	Head, Published bool
	// Draft and Read are whether the reader may read its drafts or read it.
	Draft, Read bool
}

// SearchStates returns the states of the live items and folders of ids.
func (r *Repo) SearchStates(ctx context.Context, reader access.Principal, ids []string) (map[string]SearchState, error) {
	out := map[string]SearchState{}
	if len(ids) == 0 {
		return out, nil
	}
	draftSQL, draftArgs := r.acl.Resource("r.id", reader, access.ReadDraft)
	readSQL, readArgs := r.acl.Resource("r.id", reader, access.Read)
	raw, _ := json.Marshal(ids)
	err := r.each(ctx, func(rows scan.Rows) error {
		var id string
		var st SearchState
		if err := rows.Scan(&id, &st.Publishes, &st.Head, &st.Published, &st.Draft, &st.Read); err != nil {
			return err
		}
		out[id] = st
		return nil
	}, `SELECT r.id, c.publishing_enabled,
 EXISTS(SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface='head'),
 EXISTS(SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface='published'), `+draftSQL+`, `+readSQL+` FROM resources r JOIN resources c ON c.id=r.container_id
 WHERE r.id IN (SELECT value FROM `+r.d.Elements(r.d.JSONArg(), "ids")+`) AND r.kind IN ('item','folder') AND r.deleted_at IS NULL`, append(append(draftArgs, readArgs...), string(raw))...)
	return out, err
}

// TermHolders selects the items of a workspace, or of one collection, or
// outside some collections, whose surfaces hold any of a set of terms.
type TermHolders struct {
	WorkspaceID, CollectionID string
	Skip, TermIDs             []string
	AfterID                   string
	Limit                     int
}

// TermHolders returns, in ID order, the items h selects.
func (r *Repo) TermHolders(ctx context.Context, h TermHolders) ([]string, error) {
	if len(h.TermIDs) == 0 {
		return []string{}, nil
	}
	conds := []string{"p.workspace_id=?", "p.item_id>?"}
	args := []any{h.WorkspaceID, h.AfterID}
	if h.CollectionID != "" {
		conds, args = append(conds, "p.container_id=?"), append(args, h.CollectionID)
	}
	if len(h.Skip) > 0 {
		raw, _ := json.Marshal(h.Skip)
		conds, args = append(conds, "p.container_id NOT IN (SELECT value FROM "+r.d.Elements(r.d.JSONArg(), "skipped")+")"), append(args, string(raw))
	}
	held := make([]string, len(h.TermIDs))
	for i, id := range h.TermIDs {
		held[i] = r.d.Contains(r.d.Text("p.payload"), "?")
		args = append(args, `"`+id+`"`)
	}
	conds = append(conds, "("+strings.Join(held, " OR ")+")")
	return r.strings(ctx, `SELECT DISTINCT p.item_id FROM item_surfaces p WHERE `+strings.Join(conds, " AND ")+` ORDER BY p.item_id LIMIT ?`, append(args, h.Limit)...)
}
