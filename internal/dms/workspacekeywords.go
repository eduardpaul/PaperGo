package dms

import (
	"context"
	"papergo/ent/resource"
	"strings"
)

// WorkspaceKeywordCount is a shared keyword used on readable content.
type WorkspaceKeywordCount struct {
	Keyword string `json:"keyword"`
	Name    string `json:"name"`
	Count   int    `json:"count"`
}

type WorkspaceKeywordsQuery struct {
	CollectionID string
	Prefix       string
	After        string
	Limit        int
}

// WorkspaceKeywords counts each readable resource once per keyword, including
// keywords fields and the intrinsic keywords, on the caller's visible surface.
func (s *Service) WorkspaceKeywords(ctx context.Context, subject, workspaceID string, in WorkspaceKeywordsQuery) (Page[WorkspaceKeywordCount], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[WorkspaceKeywordCount], error) {
			return t.WorkspaceKeywords(ctx, subject, workspaceID, in)
		})
	}
	w, err := s.authorize(ctx, subject, workspaceID, "read")
	if err != nil {
		return Page[WorkspaceKeywordCount]{}, err
	}
	if w.Kind != resource.KindWorkspace {
		return Page[WorkspaceKeywordCount]{}, invalid("keywords belong to a workspace")
	}
	scope, args := "r.workspace_id=?", []any{workspaceID}
	if in.CollectionID != "" {
		c, err := s.authorize(ctx, subject, in.CollectionID, "read")
		if err != nil {
			return Page[WorkspaceKeywordCount]{}, err
		}
		if c.WorkspaceID != workspaceID || c.Kind != resource.KindList && c.Kind != resource.KindLibrary {
			return Page[WorkspaceKeywordCount]{}, invalid("collection_id must be a list or library of the workspace")
		}
		scope, args = "r.container_id=?", []any{in.CollectionID}
	}
	readable, readArgs := permissionSQL(ctx, "r.id", subject, "read")
	selected, surfaceArgs := surfaceSQL(ctx, "r.id", subject, "auto")
	query := `WITH usage AS (
 SELECT r.id,coalesce(t.merged_into_id,t.id) keyword FROM resources r JOIN resource_keywords k ON k.resource_id=r.id JOIN terms t ON t.id=k.keyword
 WHERE r.kind NOT IN ('item','folder') AND r.deleted_at IS NULL AND ` + scope + ` AND ` + readable + `
 UNION
 SELECT r.id,coalesce(t.merged_into_id,t.id) FROM resources r JOIN item_surfaces p ON p.item_id=r.id JOIN terms t ON t.id IN (` + surfaceKeywordsSQL + `)
 WHERE r.deleted_at IS NULL AND ` + scope + ` AND p.surface=` + selected + ` AND ` + readable + `
 ) SELECT t.id,t.name,count(*) FROM usage u JOIN terms t ON t.id=u.keyword WHERE 1`
	values := append(append([]any{}, args...), readArgs...)
	values = append(values, args...)
	values = append(values, surfaceArgs...)
	values = append(values, readArgs...)
	if in.Prefix != "" {
		query += " AND instr(t.normalized_name,?)=1"
		values = append(values, strings.ToLower(in.Prefix))
	}
	if in.After != "" {
		name, id, _ := strings.Cut(in.After, "\x1f")
		query += " AND (t.normalized_name,t.id)>(?,?)"
		values = append(values, name, id)
	}
	query += " GROUP BY t.id ORDER BY t.normalized_name,t.id LIMIT ?"
	values = append(values, pageSize(in.Limit)+1)
	rows, err := s.Client.QueryContext(ctx, query, values...)
	if err != nil {
		return Page[WorkspaceKeywordCount]{}, err
	}
	defer rows.Close()
	out := []WorkspaceKeywordCount{}
	for rows.Next() {
		var entry WorkspaceKeywordCount
		if err = rows.Scan(&entry.Keyword, &entry.Name, &entry.Count); err != nil {
			return Page[WorkspaceKeywordCount]{}, err
		}
		out = append(out, entry)
	}
	if err = rows.Err(); err != nil {
		return Page[WorkspaceKeywordCount]{}, err
	}
	return entityPage(out, in.Limit, func(entry WorkspaceKeywordCount) string { return strings.ToLower(entry.Name) + "\x1f" + entry.Keyword }), nil
}
