package httpapi

import (
	"context"
	"net/http"
	"papergo/internal/dms"

	"github.com/danielgtaylor/huma/v2"
)

type LinkPath struct {
	LinkID string `path:"linkID" doc:"Relationship ID."`
}

func (a *API) registerItems(api huma.API) {
	register(a, api, huma.Operation{OperationID: "listRelationships", Method: http.MethodGet, Path: "/v1/items/{id}/relationships", Summary: "Read accessible incoming or outgoing item relationships", Tags: []string{"Relationships"}},
		func(ctx context.Context, in *struct {
			ItemPath
			Direction string `query:"direction" enum:"outgoing,incoming" default:"outgoing"`
			Name      string `query:"name" doc:"Relationship type key."`
			Paging
		}) (*Body[Page[Relationship]], error) {
			out, err := a.DMS.Relationships(ctx, subject(ctx), in.ID, in.Direction, in.Name, in.After, in.Limit)
			return page(out, err, infallible(relationshipOut))
		})
	register(a, api, huma.Operation{OperationID: "createRelationship", Method: http.MethodPost, Path: "/v1/items/{id}/relationships", Summary: "Link this item to another item of the workspace", Tags: []string{"Relationships"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			ItemPath
			Body dms.CreateRelationship
		}) (*Tagged[Relationship], error) {
			out, err := a.DMS.Link(ctx, subject(ctx), in.ID, in.Body)
			return tagged(out, err, infallible(relationshipOut))
		})
	register(a, api, huma.Operation{OperationID: "updateRelationship", Method: http.MethodPatch, Path: "/v1/items/{id}/relationships/{linkID}", Summary: "Replace relationship metadata", Tags: []string{"Relationships"}},
		func(ctx context.Context, in *struct {
			ItemPath
			LinkPath
			precondition
			Body dms.UpdateRelationship
		}) (*Tagged[Relationship], error) {
			out, err := a.DMS.UpdateRelationship(ctx, subject(ctx), in.ID, in.LinkID, in.Version, in.Body)
			return tagged(out, err, infallible(relationshipOut))
		})
	register(a, api, huma.Operation{OperationID: "deleteRelationship", Method: http.MethodDelete, Path: "/v1/items/{id}/relationships/{linkID}", Summary: "Delete an outgoing relationship", Description: "Uses the relationship ETag.", Tags: []string{"Relationships"}},
		func(ctx context.Context, in *struct {
			ItemPath
			LinkPath
			precondition
		}) (*Empty, error) {
			if err := a.DMS.UnlinkVersion(ctx, subject(ctx), in.ID, in.LinkID, in.Version); err != nil {
				return nil, err
			}
			return &Empty{}, nil
		})

	register(a, api, huma.Operation{OperationID: "publishItem", Method: http.MethodPost, Path: "/v1/items/{id}/publications", Summary: "Publish the current head (explicit mode only); consumes a resource lock version", Tags: []string{"Lifecycle"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			ItemPath
			precondition
		}) (*Tagged[Publication], error) {
			out, err := a.DMS.Publish(ctx, subject(ctx), in.ID, in.Version)
			if err != nil {
				return nil, err
			}
			return &Tagged[Publication]{ETag: etag(in.Version + 1), Body: publicationOut(out)}, nil
		})
	register(a, api, huma.Operation{OperationID: "listPublications", Method: http.MethodGet, Path: "/v1/items/{id}/publications", Summary: "Read immutable publication events", Description: "Draft readers see lifecycle history. Ordinary readers see publish events for the current published revision only.", Tags: []string{"Lifecycle"}},
		func(ctx context.Context, in *struct {
			ItemPath
			AfterVersion int `query:"after_version" minimum:"0" doc:"Exclusive resource-version cursor."`
			Limit        int `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum results."`
		}) (*Body[Unpaged[Publication]], error) {
			out, err := a.DMS.Publications(ctx, subject(ctx), in.ID, in.AfterVersion, in.Limit)
			return unpaged(out, err, infallible(publicationOut))
		})
	register(a, api, huma.Operation{OperationID: "unpublishItem", Method: http.MethodPost, Path: "/v1/items/{id}/unpublish", Summary: "Unpublish while retaining revisions (publish required, explicit mode only)", Tags: []string{"Lifecycle"}},
		func(ctx context.Context, in *struct {
			ItemPath
			precondition
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.Unpublish(ctx, subject(ctx), in.ID, in.Version)
			return tagged(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "listRevisions", Method: http.MethodGet, Path: "/v1/items/{id}/revisions", Summary: "Read immutable item history with schema snapshots (read_draft required)", Tags: []string{"Lifecycle"}},
		func(ctx context.Context, in *struct {
			ItemPath
			RevisionPaging
		}) (*Body[Unpaged[ItemRevision]], error) {
			out, err := a.DMS.Revisions(ctx, subject(ctx), in.ID, in.AfterRevision, in.Limit)
			return unpaged(out, err, revisionOut)
		})
	register(a, api, huma.Operation{OperationID: "getItemSchema", Method: http.MethodGet, Path: "/v1/items/{id}/schema", Summary: "Read the schema of the selected visible item revision", Tags: []string{"Lifecycle"}},
		func(ctx context.Context, in *struct {
			ItemPath
			SurfaceQuery
		}) (*Body[SchemaRevision], error) {
			out, err := a.DMS.ItemSchema(ctx, subject(ctx), in.ID, in.Surface)
			if err != nil {
				return nil, err
			}
			schema, err := schemaRevisionOut(out)
			return &Body[SchemaRevision]{schema}, err
		})
}
