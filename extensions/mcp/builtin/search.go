package builtin

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"

	"papergo/extensions/mcp"
)

// searchIndexRole is the workflow role of PaperGo's optional search: the
// API serves search only when its catalog has it.
const searchIndexRole = "search.index"

func search() mcp.Tool {
	const name = "search"
	return mcp.Tool{
		Name:  name,
		Title: "Search",
		Description: `Searches the items the caller can read across workspaces, best match first, by names, values, keywords and the text of their files.
query takes words (all must match), "phrases", a OR b, -word or NOT word, and prefix*. mode, when given, is keyword (words), semantic (meaning, when PaperGo has an embedding model) or hybrid (both); omitted, PaperGo chooses. The result's mode is the mode the search ran in.
Each hit has its collection_id and workspace_id, a snippet around the first match with highlights as byte ranges, the file page the snippet is on, how it matched and its rank. Use get_item for an item's values.
total counts matches before current permissions are rechecked; total_estimated means it may be an estimate.`,
		InputSchema: object([]string{"query"},
			property("query", `{"type": "string", "minLength": 1, "maxLength": 256, "pattern": "\\S", "description": "What to search for."}`),
			property("workspace_id", idProperty("Only this workspace.")),
			property("collection_id", idProperty("Only this list or library.")),
			property("mode", `{"type": "string", "enum": ["keyword", "semantic", "hybrid"]}`),
			property("surface", surfaceProperty),
			property("limit", limitProperty(maxSearchLimit, defaultSearchLimit)),
			property("cursor", cursorProperty)),
		OutputSchema: pageSchema(`{"type": "object", "description": "A hit, as PaperGo's SearchHit."}`,
			property("mode", `{"type": "string", "enum": ["keyword", "semantic", "hybrid"]}`),
			property("total", `{"type": "integer"}`),
			property("total_estimated", `{"type": "boolean"}`)),
		Scopes:      []string{scopeListRead},
		Annotations: mcp.Annotations{ReadOnly: true, Idempotent: true},
		Available: func(ctx context.Context, c mcp.Caller) (bool, error) {
			var catalog struct {
				Roles []struct {
					Key string `json:"key"`
				} `json:"roles"`
			}
			if err := get(ctx, c, "/v1/workflow-catalog", nil, &catalog); err != nil {
				return false, err
			}
			return slices.ContainsFunc(catalog.Roles, func(r struct {
				Key string `json:"key"`
			}) bool {
				return r.Key == searchIndexRole
			}), nil
		},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				Query        string `json:"query"`
				WorkspaceID  string `json:"workspace_id"`
				CollectionID string `json:"collection_id"`
				Mode         string `json:"mode"`
				Surface      string `json:"surface"`
				Limit        *int   `json:"limit"`
				Cursor       string `json:"cursor"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			p, err := newPaging(name, c, args)
			if err != nil {
				return nil, err
			}
			token, err := p.resume(in.Cursor)
			if err != nil {
				return nil, err
			}
			query := url.Values{"q": {in.Query}, "facets": {"false"}, "kind": {"item"}, "$top": {limit(in.Limit, defaultSearchLimit)}}
			for k, v := range map[string]string{"workspace_id": in.WorkspaceID, "collection_id": in.CollectionID, "mode": in.Mode, "surface": in.Surface, "$skiptoken": token} {
				if v != "" {
					query.Set(k, v)
				}
			}
			const path = "/v1/search"
			var res struct {
				Value          []json.RawMessage `json:"value"`
				Total          json.Number       `json:"total"`
				TotalEstimated bool              `json:"total_estimated"`
				Mode           string            `json:"mode"`
				NextLink       *string           `json:"@odata.nextLink"`
			}
			if err := get(ctx, c, path, query, &res); err != nil {
				return nil, err
			}
			next, err := p.next(res.NextLink, path, query)
			if err != nil {
				return nil, err
			}
			total := res.Total
			if total == "" {
				total = "0"
			}
			return encode(struct {
				list[json.RawMessage]
				Mode           string      `json:"mode"`
				Total          json.Number `json:"total"`
				TotalEstimated bool        `json:"total_estimated"`
			}{list[json.RawMessage]{Value: nonNil(res.Value), NextCursor: next}, res.Mode, total, res.TotalEstimated})
		},
	}
}
