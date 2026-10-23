package builtin

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"papergo/extensions/mcp"
	"papergo/internal/dal/refs"
)

// searchIndexRole is the workflow role of PaperGo's optional search: the
// API serves search only when its catalog has it.
const searchIndexRole = "search.index"

// Marks around the matches in a hit's snippet.
const (
	markOpen  = "«"
	markClose = "»"
	// maxNamed bounds the workspaces and collections a page names.
	maxNamed = 20
)

func search(l links) mcp.Tool {
	const name = "search"
	return mcp.Tool{
		Name:  name,
		Title: "Search",
		Description: `Searches the items the caller can read across workspaces, best match first, by names, values, keywords and the text of their files.
query takes words (all must match), "phrases", a OR b, -word or NOT word, and prefix*. mode, when given, is keyword (words), semantic (meaning, when PaperGo has an embedding model) or hybrid (both); omitted, PaperGo chooses. The result's mode is the mode the search ran in.
Each hit has its workspace and collection with their names, a snippet around the first match with the matches marked «like this», the file page the snippet is on, how it matched, its rank and where it opens in PaperGo (url). Use get_item for an item's values and get_item_text for its whole text.
total counts matches before current permissions are rechecked; total_estimated means it may be an estimate.`,
		InputSchema: object([]string{"query"},
			property("query", `{"type": "string", "minLength": 1, "maxLength": 256, "pattern": "\\S", "description": "What to search for."}`),
			property("workspace_id", idProperty("Only this workspace.")),
			property("collection_id", idProperty("Only this list or library.")),
			property("mode", `{"type": "string", "enum": ["keyword", "semantic", "hybrid"]}`),
			property("surface", surfaceProperty),
			property("limit", limitProperty(maxSearchLimit, defaultSearchLimit)),
			property("cursor", cursorProperty)),
		OutputSchema: pageSchema(hitSchema,
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
				Value          []hit       `json:"value"`
				Total          json.Number `json:"total"`
				TotalEstimated bool        `json:"total_estimated"`
				Mode           string      `json:"mode"`
				NextLink       *string     `json:"@odata.nextLink"`
			}
			if err := get(ctx, c, path, query, &res); err != nil {
				return nil, err
			}
			next, err := p.next(res.NextLink, path, query)
			if err != nil {
				return nil, err
			}
			if res.Value == nil {
				res.Value = []hit{}
			}
			if err := nameHits(ctx, c, l, res.Value); err != nil {
				return nil, err
			}
			total := res.Total
			if total == "" {
				total = "0"
			}
			return encode(struct {
				list[hit]
				Mode           string      `json:"mode"`
				Total          json.Number `json:"total"`
				TotalEstimated bool        `json:"total_estimated"`
			}{list[hit]{Value: res.Value, NextCursor: next}, res.Mode, total, res.TotalEstimated})
		},
	}
}

// hit is a search hit as the tool returns it: the API's, with names and a
// link, and its highlights marked in the snippet.
type hit struct {
	ID             string            `json:"id"`
	Kind           refs.ResourceKind `json:"kind"`
	Surface        string            `json:"surface"`
	WorkspaceID    string            `json:"workspace_id"`
	WorkspaceName  string            `json:"workspace_name,omitempty"`
	CollectionID   string            `json:"collection_id"`
	CollectionName string            `json:"collection_name,omitempty"`
	ContentTypeID  string            `json:"content_type_id,omitempty"`
	Name           string            `json:"name"`
	Snippet        string            `json:"snippet,omitempty"`
	Highlights     [][2]int          `json:"highlights,omitempty"`
	Page           *int              `json:"page,omitempty"`
	MatchedBy      []string          `json:"matched_by"`
	Rank           json.Number       `json:"rank"`
	ModifiedAt     string            `json:"modified_at"`
	ModifiedBy     string            `json:"modified_by"`
	URL            *string           `json:"url,omitempty"`
}

const hitSchema = `{"type": "object", "required": ["id", "kind", "workspace_id", "collection_id", "name", "matched_by", "rank", "modified_at", "modified_by"], "properties": {
	"id": {"type": "string"}, "kind": {"type": "string", "enum": ["item", "folder"]}, "surface": {"type": "string", "enum": ["head", "published"]},
	"workspace_id": {"type": "string"}, "workspace_name": {"type": "string"}, "collection_id": {"type": "string"}, "collection_name": {"type": "string"},
	"content_type_id": {"type": "string"}, "name": {"type": "string"},
	"snippet": {"type": "string", "description": "Text around the first match, with the matches marked «like this»."},
	"page": {"type": "integer", "description": "The page of the file the snippet is on, when the file's text matched."},
	"matched_by": {"type": "array", "items": {"type": "string", "enum": ["keyword", "semantic"]}},
	"rank": {"type": "number"}, "modified_at": {"type": "string"}, "modified_by": {"type": "string"},
	"url": {"type": "string", "description": "Where the item opens in PaperGo's web app."}}}`

// nameHits marks the hits' highlights, links them and names their
// workspaces and collections, reading each once and leaving out what the
// caller may not read.
func nameHits(ctx context.Context, c mcp.Caller, l links, hits []hit) error {
	names := map[string]string{}
	for i := range hits {
		h := &hits[i]
		h.Snippet, h.Highlights = mark(h.Snippet, h.Highlights), nil
		h.URL = l.at(h.Kind, h.WorkspaceID, h.CollectionID, nil, h.ID)
		for _, id := range []string{h.WorkspaceID, h.CollectionID} {
			if _, ok := names[id]; ok || id == "" || len(names) >= 2*maxNamed {
				continue
			}
			r, err := readResource(ctx, c, id, "")
			switch {
			case skippable(err):
				names[id] = ""
			case err != nil:
				return err
			default:
				names[id] = r.Name
			}
		}
		h.WorkspaceName, h.CollectionName = names[h.WorkspaceID], names[h.CollectionID]
	}
	return nil
}

// mark puts marks around the byte ranges of s, skipping any that are out of
// order or split a character.
func mark(s string, ranges [][2]int) string {
	var b strings.Builder
	at := 0
	for _, r := range ranges {
		start, end := r[0], r[1]
		if start < at || end <= start || end > len(s) || !utf8.RuneStart(s[start]) || end < len(s) && !utf8.RuneStart(s[end]) {
			continue
		}
		b.WriteString(s[at:start])
		b.WriteString(markOpen + s[start:end] + markClose)
		at = end
	}
	b.WriteString(s[at:])
	return b.String()
}
