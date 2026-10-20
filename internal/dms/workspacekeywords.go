package dms

import (
	"context"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
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
	if w.Kind != refs.KindWorkspace {
		return Page[WorkspaceKeywordCount]{}, invalid("keywords belong to a workspace")
	}
	query := content.WorkspaceKeywords{Reader: principal(ctx, subject), WorkspaceID: workspaceID, Prefix: in.Prefix, Limit: pageSize(in.Limit) + 1}
	if in.CollectionID != "" {
		c, err := s.authorize(ctx, subject, in.CollectionID, "read")
		if err != nil {
			return Page[WorkspaceKeywordCount]{}, err
		}
		if c.WorkspaceID != workspaceID || c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
			return Page[WorkspaceKeywordCount]{}, invalid("collection_id must be a list or library of the workspace")
		}
		query.CollectionID = in.CollectionID
	}
	if in.After != "" {
		query.AfterName, query.AfterID, _ = strings.Cut(in.After, "\x1f")
	}
	counts, err := s.DAL.Content().WorkspaceKeywords(ctx, query)
	if err != nil {
		return Page[WorkspaceKeywordCount]{}, err
	}
	out := make([]WorkspaceKeywordCount, len(counts))
	for i, c := range counts {
		out[i] = WorkspaceKeywordCount{Keyword: c.ID, Name: c.Name, Count: c.Count}
	}
	return entityPage(out, in.Limit, func(entry WorkspaceKeywordCount) string { return strings.ToLower(entry.Name) + "\x1f" + entry.Keyword }), nil
}
