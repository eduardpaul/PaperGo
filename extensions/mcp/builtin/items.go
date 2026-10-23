package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"papergo/extensions/mcp"
	"papergo/internal/dal/refs"
)

const queryDescription = `Queries the items and folders of a list or library the caller can read, on the chosen surface. Archived entries are left out unless query.archive asks for them.

query narrows and orders them:
- filter: one condition {"field", "op", "value"} or a group {"and": [...]}, {"or": [...]}, {"not": {...}}; at most 32 nodes, 6 deep. Custom fields must be indexed (see get_collection_schema); for words in fields that are not, use search. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by, $archived_at, $archived_by. op is eq, ne, gt, gte, lt, lte, in (value is an array of up to 50), contains, under (a term and its descendants), missing or present (no value). Term and keywords values are term IDs; list_terms finds them. value_ref instead of value: me, today, yesterday, tomorrow, weekStart, weekEnd, monthStart, monthEnd, last7Days, next7Days, last30Days, next30Days.
- sort: up to 8 {"field", "direction": "asc" | "desc"}; custom fields must be indexed. Default $id ascending.
- content_type_id, parent_id (directly in a folder; the collection's id for its top level), ancestor_id (anywhere below a folder), search (words in names and searchable values), keyword (a keyword term ID or label), archive (active, the default; archived; or all).

Examples:
{"filter": {"and": [{"field": "status", "op": "eq", "value": "Open"}, {"field": "amount", "op": "gte", "value": 12345678901234567890.25}]}, "sort": [{"field": "$modified_at", "direction": "desc"}]}
{"filter": {"field": "assignee", "op": "eq", "value_ref": "me"}}
{"filter": {"field": "due", "op": "lt", "value_ref": "today"}, "parent_id": "<folder id>"}

view_id runs one of the collection's saved views (get_collection_schema lists them) instead of query: its filter, sort and columns, as people see it in PaperGo. Navigation views browse terms and cannot be run here; filter with under instead.

select limits the custom values returned to these field keys ($keywords for keywords); [] returns none. Values keep every digit, and labels names the terms, people and linked items they refer to. Pass next_cursor as cursor, with the same other arguments, for the next page.`

var queryItemsSchema = json.RawMessage(`{"type": "object", "additionalProperties": false, "required": ["collection_id"],
	"$defs": {"filter": {"type": "object", "additionalProperties": false, "properties": {
		"and": {"type": "array", "minItems": 1, "maxItems": 32, "items": {"$ref": "#/$defs/filter"}},
		"or": {"type": "array", "minItems": 1, "maxItems": 32, "items": {"$ref": "#/$defs/filter"}},
		"not": {"$ref": "#/$defs/filter"},
		"field": {"type": "string", "minLength": 1},
		"op": {"type": "string", "enum": ["eq", "ne", "gt", "gte", "lt", "lte", "in", "contains", "under", "missing", "present"]},
		"value": {"description": "The literal compared: a string, number, boolean or, for in, an array."},
		"value_ref": {"type": "string", "enum": ["me", "today", "yesterday", "tomorrow", "weekStart", "weekEnd", "monthStart", "monthEnd", "last7Days", "next7Days", "last30Days", "next30Days"]}}}},
	"properties": {
		"collection_id": ` + idProperty("The list or library.") + `,
		"query": {"type": "object", "additionalProperties": false, "properties": {
			"filter": {"$ref": "#/$defs/filter"},
			"sort": {"type": "array", "maxItems": 8, "items": {"type": "object", "additionalProperties": false, "required": ["field"], "properties": {
				"field": {"type": "string", "minLength": 1}, "direction": {"type": "string", "enum": ["asc", "desc"]}}}},
			"content_type_id": ` + idProperty("Only entries of this attached content type.") + `,
			"parent_id": ` + idProperty("Only entries directly in this folder, or at the collection's top level when it is the collection.") + `,
			"ancestor_id": ` + idProperty("Only entries anywhere below this folder. Not with parent_id.") + `,
			"search": {"type": "string", "minLength": 1, "maxLength": 256, "description": "Words the entries match."},
			"keyword": {"type": "string", "minLength": 1, "maxLength": 64, "description": "A keyword term ID or label."},
			"archive": {"type": "string", "enum": ["active", "archived", "all"], "default": "active", "description": "Archived entries, which are left out by default."}}},
		"view_id": ` + idProperty("A saved view of the collection to run instead of query.") + `,
		"select": {"type": "array", "maxItems": 50, "uniqueItems": true, "items": {"type": "string", "minLength": 1}, "description": "Field keys whose values to return; [] returns none, omitted all."},
		"surface": ` + surfaceProperty + `,
		"limit": ` + limitProperty(maxLimit, defaultLimit) + `,
		"cursor": ` + cursorProperty + `}}`)

// navigationLayout is the layout of views that browse terms, which need
// the terms to open and so cannot run as a plain query.
const navigationLayout = "navigation"

func queryItems(l links) mcp.Tool {
	const name = "query_items"
	return mcp.Tool{
		Name:         name,
		Title:        "Query items",
		Description:  queryDescription,
		InputSchema:  queryItemsSchema,
		OutputSchema: pageSchema(itemSchema, property("search_capped", `{"type": "boolean", "description": "The search words matched more than 10,000 entries and only the first 10,000 are paged; narrow the search."}`)),
		Scopes:       []string{scopeListRead},
		Annotations:  mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				CollectionID string          `json:"collection_id"`
				Query        json.RawMessage `json:"query"`
				ViewID       string          `json:"view_id"`
				Select       json.RawMessage `json:"select"`
				Surface      string          `json:"surface"`
				Limit        *int            `json:"limit"`
				Cursor       string          `json:"cursor"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			if in.ViewID != "" && in.Query != nil {
				return nil, mcp.Fail(mcp.CodeInvalidArguments, "give query or view_id, not both")
			}
			p, err := newPaging(name, c, args)
			if err != nil {
				return nil, err
			}
			token, err := p.resume(in.Cursor)
			if err != nil {
				return nil, err
			}
			col, err := readResource(ctx, c, in.CollectionID, "")
			if err != nil {
				return nil, err
			}
			if !isCollection(col.Kind) {
				return nil, wrongKind(in.CollectionID, col.Kind, string(refs.KindList), string(refs.KindLibrary))
			}
			path := resourcePath(in.CollectionID, "query")
			if in.ViewID != "" {
				if path, err = viewPath(ctx, c, in.ViewID, in.CollectionID); err != nil {
					return nil, err
				}
			}
			body, err := encode(struct {
				Query   json.RawMessage `json:"query,omitempty"`
				Select  json.RawMessage `json:"select,omitempty"`
				Surface string          `json:"surface,omitempty"`
			}{in.Query, in.Select, in.Surface})
			if err != nil {
				return nil, err
			}
			query := url.Values{"$top": {limit(in.Limit, defaultLimit)}}
			if token != "" {
				query.Set("$skiptoken", token)
			}
			res, err := c.Backend().Do(ctx, mcp.Request{Method: http.MethodPost, Path: path, Query: query, Body: body})
			if err != nil {
				return nil, unindexed(ctx, c, in.CollectionID, err)
			}
			var result struct {
				page
				SearchCapped bool `json:"search_capped"`
			}
			if err := unmarshal(res.Body, &result); err != nil {
				return nil, err
			}
			rs, err := listed(result.page, isEntryResource)
			if err != nil {
				return nil, err
			}
			next, err := p.next(result.NextLink, path, query)
			if err != nil {
				return nil, err
			}
			out := struct {
				list[item]
				SearchCapped bool `json:"search_capped,omitempty"`
			}{list[item]{Value: make([]item, 0, len(rs)), NextCursor: next}, result.SearchCapped}
			for _, r := range rs {
				out.Value = append(out.Value, r.item(l, false))
			}
			if err := label(ctx, c, &in.CollectionID, out.Value); err != nil {
				return nil, err
			}
			return encode(out)
		},
	}
}

// viewPath is where a saved view of the collection runs.
func viewPath(ctx context.Context, c mcp.Caller, viewID, collectionID string) (string, error) {
	var view struct {
		ContainerID string `json:"container_id"`
		Layout      string `json:"layout"`
	}
	if err := get(ctx, c, "/v1/views/"+url.PathEscape(viewID), nil, &view); err != nil {
		return "", err
	}
	switch {
	case view.ContainerID != collectionID:
		return "", mcp.Fail(mcp.CodeInvalidArguments, "view_id is a view of another collection; get_collection_schema lists this one's views")
	case view.Layout == navigationLayout:
		return "", mcp.Fail(mcp.CodeInvalidArguments, "this view browses terms; filter its fields with the under operator instead")
	}
	return "/v1/views/" + url.PathEscape(viewID) + "/query", nil
}

// unindexed adds to a refused query which fields it may use instead.
func unindexed(ctx context.Context, c mcp.Caller, collectionID string, err error) error {
	var api *mcp.APIError
	if !errors.As(err, &api) || !strings.Contains(api.Detail, "be indexed") && !strings.Contains(api.Detail, "field index is") {
		return err
	}
	f := mcp.FailureOf(err)
	if f.Code != mcp.CodeInvalidArguments {
		return err
	}
	fs, ferr := fields(ctx, c, collectionID)
	if ferr != nil {
		return f
	}
	if keys := queryable(fs); len(keys) > 0 {
		f.Message += ". Fields that filters and sorts can use: " + strings.Join(keys, ", ") + "; for words in other fields, use query.search"
	} else {
		f.Message += ". No custom field can be filtered or sorted yet; use query.search for words, or the system fields"
	}
	return f
}

func isEntryResource(r resource) bool { return isEntry(r.Kind) }

func getItem(l links) mcp.Tool {
	return mcp.Tool{
		Name:  "get_item",
		Title: "Get an item",
		Description: "Reads one item or folder with all its custom values, the names of the terms, people and linked items they refer to (labels), " +
			"what the caller may do with it (access) and where it opens in PaperGo (url). Its version is what update_item requires. " +
			"For the text of a document's file, use get_item_text.",
		InputSchema: object([]string{"item_id"},
			property("item_id", idProperty("The item or folder.")), property("surface", surfaceProperty)),
		OutputSchema: json.RawMessage(itemSchema),
		Scopes:       []string{scopeListRead},
		Annotations:  mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				ItemID  string `json:"item_id"`
				Surface string `json:"surface"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			r, err := readResource(ctx, c, in.ItemID, in.Surface)
			if err != nil {
				return nil, err
			}
			if !isEntry(r.Kind) {
				return nil, wrongKind(in.ItemID, r.Kind, string(refs.KindItem), string(refs.KindFolder))
			}
			out := r.item(l, true)
			if err := label(ctx, c, r.ContainerID, []item{out}); err != nil {
				return nil, err
			}
			return encode(out)
		},
	}
}

func createItem(l links) mcp.Tool {
	return mcp.Tool{
		Name:  "create_item",
		Title: "Create an item",
		Description: "Creates an item in a list, a library or a folder of one, with custom values that must fit its content type (see get_collection_schema). " +
			"Without content_type_id the collection's default type applies. In a collection with publishing, the item starts as a draft (draft is true): " +
			"tell the person that others see it only once it is published in PaperGo. " +
			"Give an id of your own to make the call safe to repeat: when an item with that id already exists there, it is returned unchanged with created false " +
			"and the new arguments are not applied. Creates no file content.",
		InputSchema: object([]string{"parent_id", "name"},
			property("parent_id", idProperty("The list, library or folder to create the item in.")),
			property("name", `{"type": "string", "minLength": 1, "maxLength": 1024, "description": "The item's title."}`),
			property("id", idProperty("The new item's ID, for repeating the call safely. Omitted, PaperGo picks one.")),
			property("content_type_id", idProperty("A content type attached to the collection.")),
			property("values", valuesProperty),
			property("keywords", keywordsProperty)),
		OutputSchema: json.RawMessage(`{"type": "object", "required": ["created"], "allOf": [` + itemSchema + `], "properties": {
			"created": {"type": "boolean", "description": "false when an item with the given id already existed and was returned unchanged."}}}`),
		Scopes:      []string{scopeListRead, scopeListWrite},
		Annotations: mcp.Annotations{},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				ParentID      string          `json:"parent_id"`
				Name          string          `json:"name"`
				ID            string          `json:"id,omitempty"`
				ContentTypeID string          `json:"content_type_id,omitempty"`
				Values        json.RawMessage `json:"values,omitempty"`
				Keywords      json.RawMessage `json:"keywords,omitempty"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			parent, err := readResource(ctx, c, in.ParentID, "")
			if err != nil {
				return nil, err
			}
			if !isCollection(parent.Kind) && parent.Kind != refs.KindFolder {
				return nil, wrongKind(in.ParentID, parent.Kind, string(refs.KindList), string(refs.KindLibrary), string(refs.KindFolder))
			}
			body, err := encode(struct {
				Kind refs.ResourceKind `json:"kind"`
				ID   string            `json:"id,omitempty"`
				Name string            `json:"name"`
				// The remaining arguments pass through as given.
				ContentTypeID string          `json:"content_type_id,omitempty"`
				Values        json.RawMessage `json:"values,omitempty"`
				Keywords      json.RawMessage `json:"keywords,omitempty"`
			}{refs.KindItem, in.ID, in.Name, in.ContentTypeID, in.Values, in.Keywords})
			if err != nil {
				return nil, err
			}
			res, err := c.Backend().Do(ctx, mcp.Request{Method: http.MethodPost, Path: resourcePath(in.ParentID, "children"), Body: body})
			if err != nil {
				return nil, err
			}
			var r resource
			if err := unmarshal(res.Body, &r); err != nil {
				return nil, err
			}
			out := []item{r.item(l, false)}
			if err := label(ctx, c, r.ContainerID, out); err != nil {
				return nil, err
			}
			return encode(struct {
				item
				Created bool `json:"created"`
			}{out[0], res.Status == http.StatusCreated})
		},
	}
}

func updateItem(l links) mcp.Tool {
	return mcp.Tool{
		Name:  "update_item",
		Title: "Update an item",
		Description: "Changes an item's or folder's name, custom values or keywords; give at least one. values changes only the fields it names, and null clears a field. " +
			"keywords replaces every keyword. version is the entry's version from get_item, query_items or an earlier update; when the entry changed since, " +
			"the update fails with version_conflict and changes nothing: read it again and decide whether to apply the change to the new version. " +
			"In a collection with publishing, the change is a draft until published (draft is true): tell the person that others see it only once it is published in PaperGo.",
		InputSchema: object([]string{"item_id", "version"},
			property("item_id", idProperty("The item or folder.")),
			property("version", `{"type": "integer", "minimum": 1, "description": "The entry's version the change applies to."}`),
			property("name", `{"type": "string", "minLength": 1, "maxLength": 1024}`),
			property("values", valuesProperty),
			property("keywords", keywordsProperty)),
		OutputSchema: json.RawMessage(itemSchema),
		Scopes:       []string{scopeListRead, scopeListWrite},
		Annotations:  mcp.Annotations{},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				ItemID   string          `json:"item_id"`
				Version  int             `json:"version"`
				Name     string          `json:"name,omitempty"`
				Values   json.RawMessage `json:"values,omitempty"`
				Keywords json.RawMessage `json:"keywords,omitempty"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			if in.Name == "" && in.Values == nil && in.Keywords == nil {
				return nil, mcp.Fail(mcp.CodeInvalidArguments, "give at least one of name, values and keywords")
			}
			r, err := readResource(ctx, c, in.ItemID, "")
			if err != nil {
				return nil, err
			}
			if !isEntry(r.Kind) {
				return nil, wrongKind(in.ItemID, r.Kind, string(refs.KindItem), string(refs.KindFolder))
			}
			body, err := encode(struct {
				Name     string          `json:"name,omitempty"`
				Values   json.RawMessage `json:"values,omitempty"`
				Keywords json.RawMessage `json:"keywords,omitempty"`
			}{in.Name, in.Values, in.Keywords})
			if err != nil {
				return nil, err
			}
			res, err := c.Backend().Do(ctx, mcp.Request{Method: http.MethodPatch, Path: resourcePath(in.ItemID), Body: body, IfMatch: in.Version})
			if err != nil {
				return nil, err
			}
			var updated resource
			if err := unmarshal(res.Body, &updated); err != nil {
				return nil, err
			}
			out := updated.item(l, true)
			if err := label(ctx, c, updated.ContainerID, []item{out}); err != nil {
				return nil, err
			}
			return encode(out)
		},
	}
}
