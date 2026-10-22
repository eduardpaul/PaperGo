package zvecstore

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"papergo/internal/dal/refs"
	"papergo/internal/search"
)

// All user strings are encoded, including scopes and empty IDs. Native
// filter literals contain only fixed words or hex: quotes and backslashes
// can never alter zvec's grammar or be interpreted inconsistently.
func encode(value string) string        { return "x" + hex.EncodeToString([]byte(value)) }
func equal(column, value string) string { return column + " = '" + value + "'" }
func in(column string, values []string) string {
	if len(values) == 0 {
		return "row_kind IS NULL"
	}
	return column + " IN ('" + strings.Join(values, "', '") + "')"
}
func encoded(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = encode(v)
	}
	return out
}
func conjunction(parts []string) string { return "(" + strings.Join(parts, ") AND (") + ")" }

func nativeFilter(f search.Filter) (string, error) {
	draft, read := in("scope_id", encoded(f.Access.Draft)), in("scope_id", encoded(f.Access.Read))
	drafts := "views IN (1, 3) AND (" + draft + ")"
	public := "views IN (2, 3)"
	var visibility string
	switch f.Surface {
	case search.SurfaceHead:
		visibility = drafts
	case search.SurfacePublished:
		visibility = public + " AND ((" + read + ") OR (" + draft + "))"
	case "", search.SurfaceAuto:
		visibility = "(" + drafts + ") OR (" + public + " AND (" + read + ") AND NOT (" + draft + "))"
	default:
		return "", errors.New("invalid search surface")
	}
	// zvec has NOT IN, but not a general NOT of a filter expression.
	if len(f.Access.Draft) == 0 {
		visibility = strings.ReplaceAll(visibility, "NOT (row_kind IS NULL)", "row_kind IS NOT NULL")
	} else {
		visibility = strings.ReplaceAll(visibility, "NOT ("+draft+")", strings.Replace(draft, " IN ", " NOT IN ", 1))
	}
	if !f.Archive.Valid() {
		return "", errors.New("invalid archive selector")
	}
	parts := []string{visibility}
	if f.Archive == refs.ArchiveActive {
		parts = append(parts, "archived = 0")
	}
	// Active documents store archived_at as 0, so a date bound also selects
	// archived documents.
	if f.Archive == refs.ArchiveArchived || !f.ArchivedFrom.IsZero() || !f.ArchivedTo.IsZero() {
		parts = append(parts, "archived = 1")
	}
	if f.ArchivedBy != "" {
		parts = append(parts, equal("archived_by", encode(f.ArchivedBy)))
	}
	if !f.ArchivedFrom.IsZero() {
		parts = append(parts, "archived_at >= "+strconv.FormatInt(f.ArchivedFrom.UnixNano(), 10))
	}
	if !f.ArchivedTo.IsZero() {
		parts = append(parts, "archived_at <= "+strconv.FormatInt(f.ArchivedTo.UnixNano(), 10))
	}
	if len(f.Kinds) > 0 {
		parts = append(parts, in("kind", encoded(f.Kinds)))
	}
	for column, value := range map[string]string{"workspace_id": f.WorkspaceID, "collection_id": f.CollectionID, "content_type_id": f.ContentTypeID, "created_by": f.CreatedBy, "modified_by": f.ModifiedBy} {
		if value != "" {
			parts = append(parts, equal(column, encode(value)))
		}
	}
	if len(f.TermIDs) > 0 {
		tokens := encoded(f.TermIDs)
		for i := range tokens {
			tokens[i] = "t" + tokens[i]
		}
		parts = append(parts, "tokens CONTAIN_ANY ('"+strings.Join(tokens, "', '")+"')")
	}
	if f.Keyword != "" {
		parts = append(parts, "tokens CONTAIN_ANY ('g"+encode(f.Keyword)+"')")
	}
	if !f.ModifiedFrom.IsZero() {
		parts = append(parts, "modified_at >= "+strconv.FormatInt(f.ModifiedFrom.UnixNano(), 10))
	}
	if !f.ModifiedTo.IsZero() {
		parts = append(parts, "modified_at < "+strconv.FormatInt(f.ModifiedTo.UnixNano(), 10))
	}
	return conjunction(parts), nil
}

// Exact field filters run against the native source-record catalog, then
// restrict document_key before ANN/FTS top-k. This preserves multi-value
// semantics and 96-bit decimal ordering without floating point or adding
// STRING columns (zvec only allows numeric columns to be added later).
func fieldMatch(d search.Document, f *search.FieldFilter, depth int, nodes *int) (bool, error) {
	*nodes++
	if *nodes > 64 || depth > 8 {
		return false, errors.New("invalid field filter")
	}
	if f.Not != nil {
		ok, err := fieldMatch(d, f.Not, depth+1, nodes)
		return !ok, err
	}
	if f.And != nil || f.Or != nil {
		list := f.And
		all := true
		if f.Or != nil {
			list = f.Or
			all = false
		}
		if len(list) == 0 {
			return false, errors.New("empty field filter group")
		}
		result := all
		for _, child := range list {
			ok, err := fieldMatch(d, &child, depth+1, nodes)
			if err != nil {
				return false, err
			}
			if all {
				result = result && ok
			} else {
				result = result || ok
			}
		}
		return result, nil
	}
	values := []search.FieldValue{}
	for _, field := range d.Fields {
		if field.Key == f.Key && field.Type == f.Type && field.Scale == f.Scale {
			values = append(values, field.Values...)
		}
	}
	if f.Op == "present" {
		return len(values) > 0, nil
	}
	if f.Op == "missing" {
		return len(values) == 0, nil
	}
	if !slices.Contains([]string{"eq", "ne", "in", "gt", "gte", "lt", "lte"}, f.Op) || len(f.Values) == 0 || f.Op != "ne" && f.Op != "in" && len(f.Values) != 1 {
		return false, fmt.Errorf("invalid field filter operator %q", f.Op)
	}
	matched := false
	for _, held := range values {
		for _, query := range f.Values {
			comparison := strings.Compare(held.Text, query.Text)
			if search.IntegerType(f.Type) {
				comparison = 0
				if held.Int < query.Int {
					comparison = -1
				} else if held.Int > query.Int {
					comparison = 1
				}
			}
			switch f.Op {
			case "eq", "ne", "in":
				matched = matched || comparison == 0
			case "gt":
				matched = matched || comparison > 0
			case "gte":
				matched = matched || comparison >= 0
			case "lt":
				matched = matched || comparison < 0
			case "lte":
				matched = matched || comparison <= 0
			}
		}
	}
	if f.Op == "ne" {
		return len(values) > 0 && !matched, nil
	}
	return matched, nil
}
