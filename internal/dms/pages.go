package dms

import (
	"strconv"
	"time"

	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

// CompactItem is an item as compact queries return it: what tells it apart
// and what it holds, without the resource's metadata. An archived item says
// so, and the built-in columns a query selects stay, so a compact query
// answers the same questions as a full one. Its fields are those of a
// resource, under the same names; the contract calls it CompactResource.
type CompactItem struct {
	ID            string                 `json:"id"`
	WorkspaceID   string                 `json:"workspace_id"`
	Kind          refs.ResourceKind      `json:"kind"`
	Name          string                 `json:"name"`
	ParentID      *string                `json:"parent_id,omitempty"`
	ContentTypeID *string                `json:"content_type_id,omitempty"`
	Version       int                    `json:"version"`
	Values        map[string]any         `json:"values"`
	Keywords      []string               `json:"keywords,omitempty"`
	Archived      bool                   `json:"archived,omitempty"`
	ArchivedAt    *time.Time             `json:"archived_at,omitempty"`
	ArchivedBy    *string                `json:"archived_by,omitempty"`
	CreatedAt     *time.Time             `json:"created_at,omitempty"`
	CreatedBy     string                 `json:"created_by,omitempty"`
	UpdatedAt     *time.Time             `json:"updated_at,omitempty"`
	UpdatedBy     string                 `json:"updated_by,omitempty"`
	File          *resources.FileSummary `json:"file,omitempty"`
}

// compactItems writes rows as compact items with the built-in columns of a
// query's selection.
func compactItems(rows []*resources.Resource, selection []string) []CompactItem {
	out := make([]CompactItem, len(rows))
	for i, r := range rows {
		values := r.Values
		if values == nil {
			values = map[string]any{}
		}
		item := CompactItem{ID: r.ID, WorkspaceID: r.WorkspaceID, Kind: r.Kind, Name: r.Name, ParentID: r.ParentID, ContentTypeID: r.ContentTypeID, Version: r.Version, Values: values, Keywords: r.Keywords, Archived: r.Archived, ArchivedAt: r.ArchivedAt, ArchivedBy: r.ArchivedBy}
		for _, column := range selection {
			switch column {
			case "$created_at":
				item.CreatedAt = &r.CreatedAt
			case "$created_by":
				item.CreatedBy = r.CreatedBy
			case "$modified_at":
				item.UpdatedAt = &r.UpdatedAt
			case "$modified_by":
				item.UpdatedBy = r.UpdatedBy
			case "$file_type":
				item.File = r.File
			}
		}
		out[i] = item
	}
	return out
}

// CompactResult is a page of compact items.
type CompactResult struct {
	Data         []CompactItem
	NextCursor   string
	Total        *int
	TotalCapped  bool
	SearchCapped bool
}

func compactResult(p QueryResult) CompactResult {
	return CompactResult{compactItems(p.Data, p.selection), p.NextCursor, p.Total, p.TotalCapped, p.SearchCapped}
}

// CompactSmartFolderEntry is a smart folder's entry holding a compact item.
type CompactSmartFolderEntry struct {
	CollectionKind string      `json:"collection_kind"`
	WorkspaceID    string      `json:"workspace_id"`
	CollectionID   string      `json:"collection_id"`
	CollectionName string      `json:"collection_name"`
	Item           CompactItem `json:"item"`
}

// CompactSmartFolderResult is a page of a smart folder's compact items.
type CompactSmartFolderResult struct {
	Data        []CompactSmartFolderEntry
	NextCursor  string
	Total       *int
	TotalCapped bool
}

func compactSmartFolderResult(p SmartFolderResult) CompactSmartFolderResult {
	out := CompactSmartFolderResult{Data: make([]CompactSmartFolderEntry, len(p.Data)), NextCursor: p.NextCursor, Total: p.Total, TotalCapped: p.TotalCapped}
	for i, e := range p.Data {
		out.Data[i] = CompactSmartFolderEntry{e.CollectionKind, e.WorkspaceID, e.CollectionID, e.CollectionName, compactItems([]*resources.Resource{e.Item}, nil)[0]}
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
func (p CompactResult) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
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
func (p CompactSmartFolderResult) APIPage() (map[string]any, string) {
	result := map[string]any{"value": p.Data}
	if p.TotalCapped {
		result["total_capped"] = true
	}
	if p.Total != nil {
		result["total"] = p.Total
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
