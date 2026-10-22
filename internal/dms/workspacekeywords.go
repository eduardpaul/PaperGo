package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	Archive      refs.ArchiveSelection
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
	if !in.Archive.Valid() {
		return Page[WorkspaceKeywordCount]{}, invalid("archive must be active, archived or all")
	}
	query := content.WorkspaceKeywords{Archive: in.Archive.Default(), Reader: principal(ctx, subject), WorkspaceID: workspaceID, Prefix: in.Prefix, Limit: pageSize(in.Limit) + 1}
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
	normalized := in
	normalized.After, normalized.Limit = "", 0
	normalized.Archive = in.Archive.Default()
	raw, _ := json.Marshal(struct {
		Subject, Workspace string
		Query              WorkspaceKeywordsQuery
	}{subject, workspaceID, normalized})
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	if in.After != "" {
		var c browseCursor
		raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(in.After, "kw1."))
		if len(in.After) > 4096 || !strings.HasPrefix(in.After, "kw1.") || e != nil || json.Unmarshal(raw, &c) != nil || c.Query != fingerprint {
			return Page[WorkspaceKeywordCount]{}, invalid("keyword cursor does not match query or caller")
		}
		query.AfterName, query.AfterID, _ = strings.Cut(c.ID, "\x1f")
	}
	counts, err := s.DAL.Content().WorkspaceKeywords(ctx, query)
	if err != nil {
		return Page[WorkspaceKeywordCount]{}, err
	}
	out := make([]WorkspaceKeywordCount, len(counts))
	for i, c := range counts {
		out[i] = WorkspaceKeywordCount{Keyword: c.ID, Name: c.Name, Count: c.Count}
	}
	return entityPage(out, in.Limit, func(entry WorkspaceKeywordCount) string {
		raw, _ := json.Marshal(browseCursor{fingerprint, strings.ToLower(entry.Name) + "\x1f" + entry.Keyword})
		return "kw1." + base64.RawURLEncoding.EncodeToString(raw)
	}), nil
}
