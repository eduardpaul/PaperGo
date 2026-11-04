package dms

import (
	"strconv"

	"papergo/internal/dal/resources"
)

// CompactItem is an item as compact queries return it: what tells it apart
// and what it holds, without the resource's metadata. Its fields are the
// required fields of a resource and a few more, so it reads as a resource.
type CompactItem struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Kind          string         `json:"kind"`
	Name          string         `json:"name"`
	ParentID      *string        `json:"parent_id,omitempty"`
	ContentTypeID *string        `json:"content_type_id,omitempty"`
	Version       int            `json:"version"`
	Values        map[string]any `json:"values"`
	Keywords      []string       `json:"keywords,omitempty"`
}

func compactItems(rows []*resources.Resource) []CompactItem {
	out := make([]CompactItem, len(rows))
	for i, r := range rows {
		values := r.Values
		if values == nil {
			values = map[string]any{}
		}
		out[i] = CompactItem{ID: r.ID, WorkspaceID: r.WorkspaceID, Kind: string(r.Kind), Name: r.Name, ParentID: r.ParentID, ContentTypeID: r.ContentTypeID, Version: r.Version, Values: values, Keywords: r.Keywords}
	}
	return out
}

func numberedPage[T any](rows []T, limit int, number func(T) int) Page[T] {
	result := Page[T]{Data: rows}
	size := pageSize(limit)
	if len(rows) > size {
		result.Data = rows[:size]
		result.NextCursor = strconv.Itoa(number(rows[size-1]))
	}
	return result
}

// APIPage exposes data and an opaque continuation to the HTTP layer, which
// constructs links from the request URL without coupling services to HTTP.
func (p Page[T]) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
	if p.SearchCapped {
		result["search_capped"] = true
	}
	return result, p.NextCursor
}
func (p QueryResult) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
	if p.Compact {
		result["value"] = compactItems(p.Data)
	}
	if p.Total != nil {
		result["total"] = p.Total
	}
	if p.TotalCapped {
		result["total_capped"] = true
	}
	if p.SearchCapped {
		result["search_capped"] = true
	}
	return result, p.NextCursor
}
func (p Navigation) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data, "field": p.Field, "term_set_id": p.TermSetID, "include_subterms": p.IncludeSubterms, "hide_empty": p.HideEmpty}
	if p.ParentID != "" {
		result["parent_id"] = p.ParentID
	}
	if p.NoValueCount != nil {
		result["no_value_count"] = *p.NoValueCount
	}
	return result, p.NextCursor
}
func (p SmartFolderResult) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
	if p.TotalCapped {
		result["total_capped"] = true
	}
	if p.Total != nil {
		result["total"] = p.Total
	}
	return result, p.NextCursor
}
func (p SmartFolderGroupsResult) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
	if p.Field != "" {
		result["field"] = p.Field
	}
	if p.By != "" {
		result["by"] = p.By
	}
	return result, p.NextCursor
}
