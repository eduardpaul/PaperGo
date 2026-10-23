package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"papergo/extensions/mcp"
	"papergo/internal/dal/refs"
)

// list is a page of tool results.
type list[T any] struct {
	Value      []T     `json:"value"`
	NextCursor *string `json:"next_cursor"`
}

// listing reads a page of resources at path, continuing the call's cursor,
// and keeps the ones keep accepts.
func listing(ctx context.Context, c mcp.Caller, tool string, args json.RawMessage, path string, n *int, cursor string, keep func(resource) bool) ([]resource, *string, error) {
	p, err := newPaging(tool, c, args)
	if err != nil {
		return nil, nil, err
	}
	token, err := p.resume(cursor)
	if err != nil {
		return nil, nil, err
	}
	query := url.Values{"$top": {limit(n, defaultLimit)}}
	if token != "" {
		query.Set("$skiptoken", token)
	}
	var pg page
	if err := get(ctx, c, path, query, &pg); err != nil {
		return nil, nil, err
	}
	out, err := listed(pg, keep)
	if err != nil {
		return nil, nil, err
	}
	next, err := p.next(pg.NextLink, path, query)
	return out, next, err
}

func listWorkspaces(l links) mcp.Tool {
	const name = "list_workspaces"
	return mcp.Tool{
		Name:  name,
		Title: "List workspaces",
		Description: "Lists the PaperGo workspaces the caller can read, by name. A workspace holds collections: lists of items and libraries of documents. " +
			"Use list_collections with a workspace's id to see its collections.",
		InputSchema:  object(nil, property("limit", limitProperty(maxLimit, defaultLimit)), property("cursor", cursorProperty)),
		OutputSchema: pageSchema(workspaceSchema),
		Scopes:       []string{scopeWorkspaceRead},
		Annotations:  mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				Limit  *int   `json:"limit"`
				Cursor string `json:"cursor"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			rs, next, err := listing(ctx, c, name, args, "/v1/workspaces", in.Limit, in.Cursor, nil)
			if err != nil {
				return nil, err
			}
			out := list[workspace]{Value: make([]workspace, 0, len(rs)), NextCursor: next}
			for _, r := range rs {
				out.Value = append(out.Value, r.workspace(l))
			}
			return encode(out)
		},
	}
}

func listCollections(l links) mcp.Tool {
	const name = "list_collections"
	return mcp.Tool{
		Name:  name,
		Title: "List collections",
		Description: "Lists the collections of a workspace the caller can read: lists, which hold items, and libraries, which hold documents. " +
			"Use get_collection_schema for a collection's fields and content types, and query_items for its entries.",
		InputSchema: object([]string{"workspace_id"},
			property("workspace_id", idProperty("The workspace, as list_workspaces returns it.")),
			property("limit", limitProperty(maxLimit, defaultLimit)), property("cursor", cursorProperty)),
		OutputSchema: pageSchema(collectionSchema),
		Scopes:       []string{scopeWorkspaceRead, scopeItemRead},
		Annotations:  mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				WorkspaceID string `json:"workspace_id"`
				Limit       *int   `json:"limit"`
				Cursor      string `json:"cursor"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			ws, err := readResource(ctx, c, in.WorkspaceID, "")
			if err != nil {
				return nil, err
			}
			if ws.Kind != refs.KindWorkspace {
				return nil, wrongKind(in.WorkspaceID, ws.Kind, string(refs.KindWorkspace))
			}
			rs, next, err := listing(ctx, c, name, args, resourcePath(in.WorkspaceID, "children"), in.Limit, in.Cursor,
				func(r resource) bool { return isCollection(r.Kind) })
			if err != nil {
				return nil, err
			}
			out := list[collection]{Value: make([]collection, 0, len(rs)), NextCursor: next}
			for _, r := range rs {
				out.Value = append(out.Value, r.collection(l))
			}
			return encode(out)
		},
	}
}

func getCollectionSchema(l links) mcp.Tool {
	return mcp.Tool{
		Name:  "get_collection_schema",
		Title: "Get a collection's schema",
		Description: "Describes a list or library as new and updated entries must fit it: its settings, the fields entries may hold (key, label, type, " +
			"whether required, choices, decimal scale, whether indexed for query_items filters and sorts), the content types attached to it " +
			"with the field IDs each uses, its validation rules and which is the default for new entries, and its saved views: the ways people " +
			"look at it, such as \"My open invoices\", which query_items runs with view_id. Published entries may still hold values of an earlier schema.",
		InputSchema: object([]string{"collection_id"}, property("collection_id", idProperty("The list or library."))),
		OutputSchema: json.RawMessage(`{"type": "object", "required": ["collection", "fields", "content_types", "views"], "properties": {
			"collection": ` + collectionSchema + `,
			"fields": {"type": "array", "items": {"type": "object"}, "description": "Field definitions, as PaperGo's FieldDefinition."},
			"content_types": {"type": "array", "items": {"type": "object"}, "description": "Attached content types, as PaperGo's CollectionContentType, with is_default."},
			"views": {"type": "array", "items": {"type": "object"}, "description": "Saved views, as PaperGo's ListView: id, name, layout, columns, query, is_default."}}}`),
		Scopes:      []string{scopeItemRead, scopeContentTypeRead},
		Annotations: mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				CollectionID string `json:"collection_id"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			before, err := readResource(ctx, c, in.CollectionID, "")
			if err != nil {
				return nil, err
			}
			if !isCollection(before.Kind) {
				return nil, wrongKind(in.CollectionID, before.Kind, string(refs.KindList), string(refs.KindLibrary))
			}
			var fields page
			if err := get(ctx, c, resourcePath(in.CollectionID, "fields"), nil, &fields); err != nil {
				return nil, err
			}
			var types []json.RawMessage
			if err := get(ctx, c, resourcePath(in.CollectionID, "content-types"), nil, &types); err != nil {
				return nil, err
			}
			var views page
			if err := get(ctx, c, resourcePath(in.CollectionID, "views"), url.Values{"$top": {fmt.Sprint(maxLimit)}}, &views); err != nil {
				return nil, err
			}
			after, err := readResource(ctx, c, in.CollectionID, "")
			if err != nil {
				return nil, err
			}
			if after.Version != before.Version || !sameID(after.SchemaHeadID, before.SchemaHeadID) {
				return nil, &mcp.Failure{Code: mcp.CodeSchemaChanged, Message: "The collection's schema changed while it was read; call get_collection_schema again.", Retryable: true}
			}
			return encode(struct {
				Collection   collection        `json:"collection"`
				Fields       []json.RawMessage `json:"fields"`
				ContentTypes []json.RawMessage `json:"content_types"`
				Views        []json.RawMessage `json:"views"`
			}{after.collection(l), nonNil(fields.Value), nonNil(types), nonNil(views.Value)})
		},
	}
}

func sameID(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func nonNil(v []json.RawMessage) []json.RawMessage {
	if v == nil {
		return []json.RawMessage{}
	}
	return v
}

// workspace is a workspace as the tools return it.
type workspace struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Version     int      `json:"version"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	URL         *string  `json:"url,omitempty"`
}

func (r resource) workspace(l links) workspace {
	out := workspace{ID: r.ID, Name: r.Name, Description: r.Description, Keywords: r.Keywords, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		URL: l.workspace(r.ID)}
	if out.Keywords == nil {
		out.Keywords = []string{}
	}
	return out
}
