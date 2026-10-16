package dms

import "strconv"

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
