package httpapi

import (
	"context"
	"net/http"
	"papergo/internal/dms"

	"github.com/danielgtaylor/huma/v2"
)

type TemplatePath struct {
	TemplateID string `path:"templateID" doc:"Schema template ID."`
}

func queryResult(r dms.QueryResult, err error) (*Body[QueryResult], error) {
	if err != nil {
		return nil, err
	}
	data, err := convert(r.Data, resourceConv)
	return &Body[QueryResult]{QueryResult{Data: data, NextCursor: r.NextCursor, Total: r.Total}}, err
}
func groups(p dms.Page[dms.QueryGroup], err error) (*Body[Page[dms.QueryGroup]], error) {
	return page(p, err, func(g dms.QueryGroup) (dms.QueryGroup, error) { return g, nil })
}

func (a *API) registerFoundation(api huma.API) {
	register(a, api, huma.Operation{OperationID: "listContentTypes", Method: http.MethodGet, Path: "/v1/resources/{id}/content-types", Summary: "List collection content types", Tags: []string{"Content types"}},
		func(ctx context.Context, in *struct{ CollectionPath }) (*Body[[]ContentType], error) {
			out, err := a.DMS.ContentTypes(ctx, subject(ctx), in.ID)
			if err != nil {
				return nil, err
			}
			types, err := convert(out, contentTypeOut)
			return &Body[[]ContentType]{types}, err
		})
	register(a, api, huma.Operation{OperationID: "createContentType", Method: http.MethodPost, Path: "/v1/resources/{id}/content-types", Summary: "Create a collection content type (manage required)", Description: "Uses the collection ETag. At most 32 content types per collection, with exactly one default.", Tags: []string{"Content types"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			CollectionPath
			precondition
			Body dms.ContentTypeInput
		}) (*Tagged[ContentType], error) {
			out, err := a.DMS.CreateContentType(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, contentTypeOut)
		})
	register(a, api, huma.Operation{OperationID: "getContentType", Method: http.MethodGet, Path: "/v1/content-types/{id}", Summary: "Read a content type", Tags: []string{"Content types"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[ContentType], error) {
			out, err := a.DMS.GetContentType(ctx, subject(ctx), in.ID)
			return tagged(out, err, contentTypeOut)
		})
	register(a, api, huma.Operation{OperationID: "updateContentType", Method: http.MethodPut, Path: "/v1/content-types/{id}", Summary: "Replace content type fields and rules (manage required)", Description: "Uses the content type ETag. Replacements validate existing heads against the proposed fields and rules atomically.", Tags: []string{"Content types"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.ContentTypeInput
		}) (*Tagged[ContentType], error) {
			out, err := a.DMS.UpdateContentType(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, contentTypeOut)
		})

	register(a, api, huma.Operation{OperationID: "bulkItems", Method: http.MethodPost, Path: "/v1/resources/{id}/bulk", Summary: "Apply an atomic batch of item operations", Description: "1..100 item creates, updates/moves, publishes, unpublishes or deletes in one list/library and one transaction. Requires collection read and per-operation write/publish permissions. Existing items require a positive current version in the body and may occur only once. No collection If-Match is required. Folder operations and blob transfers are excluded. Create parent_id defaults to the collection; all targets and destinations stay in it. All revisions, projections, relationships and audit effects roll back on any failure, which reports operation_index when applicable. No retry deduplication. Request body limit: 1 MiB.", Tags: []string{"Items"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			Body dms.BulkRequest
		}) (*Body[dms.BulkResponse], error) {
			out, err := a.DMS.Bulk(ctx, subject(ctx), in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			return &Body[dms.BulkResponse]{out}, nil
		})
	register(a, api, huma.Operation{OperationID: "queryItems", Method: http.MethodPost, Path: "/v1/resources/{id}/query", Summary: "Query a collection", Tags: []string{"Queries"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			Body dms.QueryRequest
		}) (*Body[QueryResult], error) {
			return queryResult(a.DMS.Query(ctx, subject(ctx), in.ID, in.Body))
		})
	register(a, api, huma.Operation{OperationID: "queryItemGroups", Method: http.MethodPost, Path: "/v1/resources/{id}/query/groups", Summary: "Count query matches grouped by query.group_by", Tags: []string{"Queries"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			Body dms.QueryRequest
		}) (*Body[Page[dms.QueryGroup]], error) {
			return groups(a.DMS.QueryGroups(ctx, subject(ctx), in.ID, in.Body))
		})

	register(a, api, huma.Operation{OperationID: "listTemplates", Method: http.MethodGet, Path: "/v1/workspaces/{id}/templates", Summary: "List workspace schema templates", Tags: []string{"Templates"}},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Paging
		}) (*Body[Page[SchemaTemplate]], error) {
			out, err := a.DMS.Templates(ctx, subject(ctx), in.ID, in.After, in.Limit)
			return page(out, err, templateOut)
		})
	register(a, api, huma.Operation{OperationID: "createTemplate", Method: http.MethodPost, Path: "/v1/workspaces/{id}/templates", Summary: "Create a schema template (workspace manage required)", Tags: []string{"Templates"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Body dms.TemplateInput
		}) (*Tagged[SchemaTemplate], error) {
			out, err := a.DMS.CreateTemplate(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, templateOut)
		})
	register(a, api, huma.Operation{OperationID: "getTemplate", Method: http.MethodGet, Path: "/v1/templates/{id}", Summary: "Read a schema template", Tags: []string{"Templates"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[SchemaTemplate], error) {
			out, err := a.DMS.Template(ctx, subject(ctx), in.ID)
			return tagged(out, err, templateOut)
		})
	register(a, api, huma.Operation{OperationID: "updateTemplate", Method: http.MethodPut, Path: "/v1/templates/{id}", Summary: "Replace a schema template", Tags: []string{"Templates"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.TemplateInput
		}) (*Tagged[SchemaTemplate], error) {
			out, err := a.DMS.UpdateTemplate(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, templateOut)
		})
	register(a, api, huma.Operation{OperationID: "applyTemplate", Method: http.MethodPost, Path: "/v1/resources/{id}/templates/{templateID}/apply", Summary: "Adopt a template's fields and rules into a content type", Description: "Uses the collection ETag. Applies compatible shared catalog field definitions and merges rules by key into the selected content type, recording one frozen collection schema. Other type memberships are retained; existing content is never rewritten.", Tags: []string{"Templates"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			TemplatePath
			precondition
			Body dms.ApplyTemplateInput
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.ApplyTemplate(ctx, subject(ctx), in.ID, in.TemplateID, in.Version, in.Body.TemplateVersion, in.Body.ContentTypeID)
			return tagged(out, err, resourceConv)
		})

	register(a, api, huma.Operation{OperationID: "listViews", Method: http.MethodGet, Path: "/v1/resources/{id}/views", Summary: "List saved views of a collection", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			Paging
		}) (*Body[Page[ListView]], error) {
			out, err := a.DMS.Views(ctx, subject(ctx), in.ID, in.After, in.Limit)
			return page(out, err, viewOut)
		})
	register(a, api, huma.Operation{OperationID: "createView", Method: http.MethodPost, Path: "/v1/resources/{id}/views", Summary: "Create a saved view (collection manage required)", Description: "Saved views grant no access; queries run with the caller's permissions.", Tags: []string{"Views"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			CollectionPath
			Body dms.ViewInput
		}) (*Tagged[ListView], error) {
			out, err := a.DMS.CreateView(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, viewOut)
		})
	register(a, api, huma.Operation{OperationID: "getView", Method: http.MethodGet, Path: "/v1/views/{id}", Summary: "Read a saved view", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[ListView], error) {
			out, err := a.DMS.View(ctx, subject(ctx), in.ID)
			return tagged(out, err, viewOut)
		})
	register(a, api, huma.Operation{OperationID: "updateView", Method: http.MethodPut, Path: "/v1/views/{id}", Summary: "Replace a saved view", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.ViewInput
		}) (*Tagged[ListView], error) {
			out, err := a.DMS.UpdateView(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, viewOut)
		})
	register(a, api, huma.Operation{OperationID: "deleteView", Method: http.MethodDelete, Path: "/v1/views/{id}", Summary: "Delete a saved view", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
		}) (*Empty, error) {
			if err := a.DMS.DeleteView(ctx, subject(ctx), in.ID, in.Version); err != nil {
				return nil, err
			}
			return &Empty{}, nil
		})
	register(a, api, huma.Operation{OperationID: "queryView", Method: http.MethodPost, Path: "/v1/views/{id}/query", Summary: "Run a saved view's query", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct {
			EntityPath
			Body dms.ViewQueryRequest
		}) (*Body[QueryResult], error) {
			return queryResult(a.DMS.QueryView(ctx, subject(ctx), in.ID, in.Body))
		})
	register(a, api, huma.Operation{OperationID: "queryViewGroups", Method: http.MethodPost, Path: "/v1/views/{id}/query/groups", Summary: "Count a saved view's matches by its group_by field", Tags: []string{"Views"}},
		func(ctx context.Context, in *struct {
			EntityPath
			Body dms.ViewQueryRequest
		}) (*Body[Page[dms.QueryGroup]], error) {
			return groups(a.DMS.QueryViewGroups(ctx, subject(ctx), in.ID, in.Body))
		})

	register(a, api, huma.Operation{OperationID: "listTermSets", Method: http.MethodGet, Path: "/v1/workspaces/{id}/term-sets", Summary: "List workspace term sets", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Paging
		}) (*Body[Page[TermSet]], error) {
			out, err := a.DMS.TermSets(ctx, subject(ctx), in.ID, in.After, in.Limit)
			return page(out, err, termSetOut)
		})
	register(a, api, huma.Operation{OperationID: "createTermSet", Method: http.MethodPost, Path: "/v1/workspaces/{id}/term-sets", Summary: "Create a term set (workspace manage required)", Tags: []string{"Taxonomy"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Body dms.TermSetInput
		}) (*Tagged[TermSet], error) {
			out, err := a.DMS.CreateTermSet(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, termSetOut)
		})
	register(a, api, huma.Operation{OperationID: "getTermSet", Method: http.MethodGet, Path: "/v1/term-sets/{id}", Summary: "Read a term set", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[TermSet], error) {
			out, err := a.DMS.TermSet(ctx, subject(ctx), in.ID)
			return tagged(out, err, termSetOut)
		})
	register(a, api, huma.Operation{OperationID: "updateTermSet", Method: http.MethodPut, Path: "/v1/term-sets/{id}", Summary: "Replace a term set", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.TermSetInput
		}) (*Tagged[TermSet], error) {
			out, err := a.DMS.UpdateTermSet(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, termSetOut)
		})
	register(a, api, huma.Operation{OperationID: "listTerms", Method: http.MethodGet, Path: "/v1/term-sets/{id}/terms", Summary: "List or search the terms of a set", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct {
			EntityPath
			Search string `query:"q" maxLength:"255" doc:"Case-insensitive match on names, synonyms and labels."`
			Paging
		}) (*Body[Page[Term]], error) {
			out, err := a.DMS.Terms(ctx, subject(ctx), in.ID, in.Search, in.After, in.Limit)
			return page(out, err, termOut)
		})
	register(a, api, huma.Operation{OperationID: "createTerm", Method: http.MethodPost, Path: "/v1/term-sets/{id}/terms", Summary: "Create a term", Tags: []string{"Taxonomy"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			EntityPath
			Body dms.TermInput
		}) (*Tagged[Term], error) {
			out, err := a.DMS.CreateTerm(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, termOut)
		})
	register(a, api, huma.Operation{OperationID: "getTerm", Method: http.MethodGet, Path: "/v1/terms/{id}", Summary: "Read a term", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[Term], error) {
			out, err := a.DMS.Term(ctx, subject(ctx), in.ID)
			return tagged(out, err, termOut)
		})
	register(a, api, huma.Operation{OperationID: "updateTerm", Method: http.MethodPut, Path: "/v1/terms/{id}", Summary: "Replace a term", Tags: []string{"Taxonomy"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.TermInput
		}) (*Tagged[Term], error) {
			out, err := a.DMS.UpdateTerm(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, termOut)
		})

	register(a, api, huma.Operation{OperationID: "listRelationshipTypes", Method: http.MethodGet, Path: "/v1/workspaces/{id}/relationship-types", Summary: "List workspace relationship types", Tags: []string{"Relationship types"}},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Paging
		}) (*Body[Page[RelationshipType]], error) {
			out, err := a.DMS.RelationshipTypes(ctx, subject(ctx), in.ID, in.After, in.Limit)
			return page(out, err, relationshipTypeOut)
		})
	register(a, api, huma.Operation{OperationID: "createRelationshipType", Method: http.MethodPost, Path: "/v1/workspaces/{id}/relationship-types", Summary: "Create a relationship type (workspace manage required)", Tags: []string{"Relationship types"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Body dms.RelationshipTypeInput
		}) (*Tagged[RelationshipType], error) {
			out, err := a.DMS.CreateRelationshipType(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, relationshipTypeOut)
		})
	register(a, api, huma.Operation{OperationID: "getRelationshipType", Method: http.MethodGet, Path: "/v1/relationship-types/{id}", Summary: "Read a relationship type", Tags: []string{"Relationship types"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Tagged[RelationshipType], error) {
			out, err := a.DMS.RelationshipType(ctx, subject(ctx), in.ID)
			return tagged(out, err, relationshipTypeOut)
		})
	register(a, api, huma.Operation{OperationID: "updateRelationshipType", Method: http.MethodPut, Path: "/v1/relationship-types/{id}", Summary: "Replace a relationship type", Tags: []string{"Relationship types"}},
		func(ctx context.Context, in *struct {
			EntityPath
			precondition
			Body dms.RelationshipTypeInput
		}) (*Tagged[RelationshipType], error) {
			out, err := a.DMS.UpdateRelationshipType(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, relationshipTypeOut)
		})
}
