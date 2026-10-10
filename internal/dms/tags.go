package dms

import (
	"context"

	"papergo/ent/resource"
)

// TagCount is a tag of the workspace's vocabulary and how many resources the
// caller can read carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// TagsQuery narrows the tag vocabulary: CollectionID to one list or library,
// Prefix to tags starting with it.
type TagsQuery struct {
	CollectionID string
	Prefix       string
	After        string
	Limit        int
}

// Tags returns the tags used in a workspace, sorted, with how many resources
// carry them. Tags are free text labels people add to any resource; keywords
// are taxonomy terms (see keywords.go). Only resources the caller may read count, and items count
// with the surface the caller sees (head for draft readers, else published),
// so the vocabulary never reveals what the caller cannot see.
func (s *Service) Tags(ctx context.Context, subject, workspaceID string, in TagsQuery) (Page[TagCount], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[TagCount], error) { return t.Tags(ctx, subject, workspaceID, in) })
	}
	w, err := s.authorize(ctx, subject, workspaceID, "read")
	if err != nil {
		return Page[TagCount]{}, err
	}
	if w.Kind != resource.KindWorkspace {
		return Page[TagCount]{}, invalid("tags belong to a workspace")
	}
	scope, scopeArgs := "r.workspace_id=?", []any{workspaceID}
	if in.CollectionID != "" {
		c, err := s.authorize(ctx, subject, in.CollectionID, "read")
		if err != nil {
			return Page[TagCount]{}, err
		}
		if c.WorkspaceID != workspaceID || c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
			return Page[TagCount]{}, invalid("collection_id must be a list or library of the workspace")
		}
		scope, scopeArgs = "r.container_id=?", []any{in.CollectionID}
	}
	match, matchArgs := "1", []any{}
	if in.Prefix != "" {
		// A range on the tag index; U+10FFFF sorts after every other character.
		match, matchArgs = "t.tag>=? AND t.tag<?", []any{in.Prefix, in.Prefix + "\U0010FFFF"}
	}
	if in.After != "" {
		match += " AND t.tag>?"
		matchArgs = append(matchArgs, in.After)
	}
	readable, readArgs := permissionSQL("r.id", subject, "read")
	selected, selectedArgs := surfaceSQL("r.id", subject, "auto")
	query := `SELECT tag, count(*) FROM (
		SELECT t.tag AS tag FROM resource_tags t JOIN resources r ON r.id=t.resource_id
		WHERE ` + match + ` AND r.kind<>'item' AND r.deleted_at IS NULL AND ` + scope + ` AND ` + readable + `
		UNION ALL
		SELECT t.tag FROM item_surface_tags t JOIN item_surfaces p ON p.id=t.surface_id JOIN resources r ON r.id=p.item_id
		WHERE ` + match + ` AND r.deleted_at IS NULL AND ` + scope + ` AND p.surface=` + selected + ` AND ` + readable + `
	) GROUP BY tag ORDER BY tag LIMIT ?`
	args := append(append(append(append([]any{}, matchArgs...), scopeArgs...), readArgs...), matchArgs...)
	args = append(append(append(append(args, scopeArgs...), selectedArgs...), readArgs...), pageSize(in.Limit)+1)
	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return Page[TagCount]{}, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err = rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return Page[TagCount]{}, err
		}
		out = append(out, tc)
	}
	if err = rows.Err(); err != nil {
		return Page[TagCount]{}, err
	}
	return entityPage(out, in.Limit, func(tc TagCount) string { return tc.Tag }), nil
}
