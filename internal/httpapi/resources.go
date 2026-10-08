package httpapi

import (
	"context"
	"net/http"
	"papergo/ent"
	"papergo/internal/dms"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Operation inputs embed these parameter groups; exported so Huma finds them.
type ResourcePath struct {
	ID string `path:"id" doc:"Resource ID."`
}
type ItemPath struct {
	ID string `path:"id" doc:"Item ID."`
}
type WorkspacePath struct {
	ID string `path:"id" doc:"Workspace ID."`
}
type CollectionPath struct {
	ID string `path:"id" doc:"List or library ID."`
}
type FieldPath struct {
	FieldID string `path:"fieldID" doc:"Field definition ID."`
}
type EntityPath struct {
	ID string `path:"id" doc:"Entity ID."`
}
type Paging struct {
	After string `query:"after" doc:"Opaque cursor from next_cursor of the previous page."`
	Limit int    `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum results."`
}
type RevisionPaging struct {
	AfterRevision int `query:"after_revision" minimum:"0" doc:"Exclusive revision-number cursor."`
	Limit         int `query:"limit" minimum:"1" maximum:"100" default:"50" doc:"Maximum results."`
}
type SurfaceQuery struct {
	Surface string `query:"surface" enum:"auto,head,published" default:"auto" doc:"Auto chooses head for draft readers and published for ordinary readers. Head requires draft access."`
}
type BrowseQuery struct {
	Search      string `query:"q" doc:"Full-text search over the visible surface."`
	Tag         string `query:"tag"`
	FilterField string `query:"filter_field" pattern:"^[a-z][a-z0-9_]{0,63}$" doc:"Opt-in indexed field key; requires a list/library/folder parent scope."`
	FilterOp    string `query:"filter_op" enum:"eq,gt,gte,lt,lte" doc:"Typed comparison, eq when omitted. Boolean supports eq only."`
	FilterValue string `query:"filter_value" doc:"Typed value, parsed without floating-point conversion for integer and decimal fields."`
}

// Operation outputs.
type Body[T any] struct{ Body T }
type Tagged[T any] struct {
	ETag string `header:"ETag" doc:"Quoted version to send as If-Match on the next change."`
	Body T
}
type Created[T any] struct {
	Location string `header:"Location" doc:"URL of the created resource."`
	ETag     string `header:"ETag" doc:"Quoted version to send as If-Match on the next change."`
	Body     T
}
type TaggedEmpty struct {
	ETag string `header:"ETag" doc:"Quoted collection version after the change."`
}
type Empty struct{}

// versioned models carry the lock version that their ETag exposes.
type versioned interface{ etag() string }

func (r Resource) etag() string         { return etag(r.Version) }
func (r Relationship) etag() string     { return etag(r.Version) }
func (t SchemaTemplate) etag() string   { return etag(t.Version) }
func (t TermSet) etag() string          { return etag(t.Version) }
func (t Term) etag() string             { return etag(t.Version) }
func (t RelationshipType) etag() string { return etag(t.Version) }
func (v ListView) etag() string         { return etag(v.Version) }
func (t ContentType) etag() string      { return etag(t.Version) }

func tagged[E any, D versioned](e E, err error, conv func(E) (D, error)) (*Tagged[D], error) {
	if err != nil {
		return nil, err
	}
	d, err := conv(e)
	if err != nil {
		return nil, err
	}
	return &Tagged[D]{ETag: d.etag(), Body: d}, nil
}
func page[E, D any](p dms.Page[E], err error, conv func(E) (D, error)) (*Body[Page[D]], error) {
	if err != nil {
		return nil, err
	}
	data, err := convert(p.Data, conv)
	if err != nil {
		return nil, err
	}
	return &Body[Page[D]]{Page[D]{Data: data, NextCursor: p.NextCursor}}, nil
}
func unpaged[E, D any](items []E, err error, conv func(E) (D, error)) (*Body[Unpaged[D]], error) {
	if err != nil {
		return nil, err
	}
	data, err := convert(items, conv)
	if err != nil {
		return nil, err
	}
	return &Body[Unpaged[D]]{Unpaged[D]{Data: data}}, nil
}
func createdResource(r *ent.Resource, err error) (*Created[Resource], error) {
	if err != nil {
		return nil, err
	}
	return &Created[Resource]{Location: "/v1/resources/" + r.ID, ETag: etag(r.Version), Body: resourceOut(r)}, nil
}

var resourceConv = infallible(resourceOut)

func (a *API) registerResources(api huma.API) {
	public := []map[string][]string{}
	register(a, api, huma.Operation{OperationID: "getLiveness", Method: http.MethodGet, Path: "/health/live", Summary: "Process liveness", Tags: []string{"System"}, Security: public},
		func(ctx context.Context, _ *struct{}) (*Body[Health], error) {
			return &Body[Health]{Health{Status: "ok"}}, nil
		})
	register(a, api, huma.Operation{OperationID: "getReadiness", Method: http.MethodGet, Path: "/health/ready", Summary: "Database and migration readiness", Tags: []string{"System"}, Security: public},
		func(ctx context.Context, _ *struct{}) (*Body[Health], error) {
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := a.Ready(ctx); err != nil {
				return nil, newProblem(ctx, 503, "unavailable", "service is not ready")
			}
			return &Body[Health]{Health{Status: "ok"}}, nil
		})

	register(a, api, huma.Operation{OperationID: "listWorkspaces", Method: http.MethodGet, Path: "/v1/workspaces", Summary: "List visible workspaces", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct{ Paging }) (*Body[Page[Resource]], error) {
			out, err := a.DMS.Browse(ctx, subject(ctx), dms.Browse{After: in.After, Limit: in.Limit})
			return page(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "createWorkspace", Method: http.MethodPost, Path: "/v1/workspaces", Summary: "Create a workspace with creator manage permission", Tags: []string{"Resources"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			Body struct {
				Name string   `json:"name" minLength:"1" maxLength:"255"`
				Tags []string `json:"tags,omitempty" maxItems:"50" uniqueItems:"true"`
			}
		}) (*Created[Resource], error) {
			return createdResource(a.DMS.Create(ctx, subject(ctx), "", dms.CreateResource{Kind: "workspace", Name: in.Body.Name, Tags: in.Body.Tags}))
		})
	register(a, api, huma.Operation{OperationID: "browseResources", Method: http.MethodGet, Path: "/v1/resources", Summary: "Browse or search a workspace or parent scope", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct {
			WorkspaceID string `query:"workspace_id" doc:"Workspace to browse; or give parent_id."`
			ParentID    string `query:"parent_id" doc:"Parent whose direct children are listed."`
			Paging
			SurfaceQuery
			BrowseQuery
		}) (*Body[Page[Resource]], error) {
			out, err := a.DMS.Browse(ctx, subject(ctx), dms.Browse{WorkspaceID: in.WorkspaceID, ParentID: in.ParentID, Search: in.Search, Tag: in.Tag, After: in.After, Limit: in.Limit, Surface: in.Surface, FilterField: in.FilterField, FilterOp: in.FilterOp, FilterValue: in.FilterValue})
			return page(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "getResource", Method: http.MethodGet, Path: "/v1/resources/{id}", Summary: "Read resource metadata", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct {
			ResourcePath
			SurfaceQuery
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.GetSurface(ctx, subject(ctx), in.ID, in.Surface)
			return tagged(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "updateResource", Method: http.MethodPatch, Path: "/v1/resources/{id}", Summary: "Update a resource; values replaces the whole custom-value object", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct {
			ResourcePath
			precondition
			Body dms.UpdateResource
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.Update(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "deleteResource", Method: http.MethodDelete, Path: "/v1/resources/{id}", Summary: "Delete a folder or item with everything below it (write required on all of it)", Description: "Deleted resources become retained tombstones: they disappear from every read, search, query and relationship, their names become free, and their revisions, publications and audit events are kept. Deletion cannot be undone.", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct {
			ResourcePath
			precondition
		}) (*Empty, error) {
			return &Empty{}, a.DMS.Delete(ctx, subject(ctx), in.ID, in.Version)
		})
	register(a, api, huma.Operation{OperationID: "listChildren", Method: http.MethodGet, Path: "/v1/resources/{id}/children", Summary: "Browse visible direct children", Tags: []string{"Resources"}},
		func(ctx context.Context, in *struct {
			ResourcePath
			Paging
			SurfaceQuery
			BrowseQuery
		}) (*Body[Page[Resource]], error) {
			out, err := a.DMS.Browse(ctx, subject(ctx), dms.Browse{ParentID: in.ID, Search: in.Search, Tag: in.Tag, After: in.After, Limit: in.Limit, Surface: in.Surface, FilterField: in.FilterField, FilterOp: in.FilterOp, FilterValue: in.FilterValue})
			return page(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "createChild", Method: http.MethodPost, Path: "/v1/resources/{id}/children", Summary: "Create a list, library, folder or item below a resource", Tags: []string{"Resources"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			ResourcePath
			Body dms.CreateResource
		}) (*Created[Resource], error) {
			return createdResource(a.DMS.Create(ctx, subject(ctx), in.ID, in.Body))
		})
	register(a, api, huma.Operation{OperationID: "getPermissions", Method: http.MethodGet, Path: "/v1/resources/{id}/permissions", Summary: "Read explicit and effective permissions (manage required)", Tags: []string{"Permissions"}},
		func(ctx context.Context, in *struct{ ResourcePath }) (*Body[dms.Permissions], error) {
			out, err := a.DMS.Permissions(ctx, subject(ctx), in.ID)
			if err != nil {
				return nil, err
			}
			return &Body[dms.Permissions]{out}, nil
		})
	register(a, api, huma.Operation{OperationID: "setPermissions", Method: http.MethodPut, Path: "/v1/resources/{id}/permissions", Summary: "Replace permissions and inheritance (manage required)", Tags: []string{"Permissions"}},
		func(ctx context.Context, in *struct {
			ResourcePath
			precondition
			Body dms.Permissions
		}) (*Tagged[Resource], error) {
			out, err := a.DMS.SetPermissions(ctx, subject(ctx), in.ID, in.Version, in.Body)
			return tagged(out, err, resourceConv)
		})
	register(a, api, huma.Operation{OperationID: "listWorkspaceAudit", Method: http.MethodGet, Path: "/v1/workspaces/{id}/audit", Summary: "Read workspace audit events (manage required)", Tags: []string{"Audit"}},
		func(ctx context.Context, in *struct {
			WorkspacePath
			Paging
		}) (*Body[Page[AuditEvent]], error) {
			out, err := a.DMS.Audit(ctx, subject(ctx), in.ID, in.After, in.Limit)
			return page(out, err, infallible(auditOut))
		})

	register(a, api, huma.Operation{OperationID: "listFields", Method: http.MethodGet, Path: "/v1/resources/{id}/fields", Summary: "Read the collection field catalog", Tags: []string{"Fields"}},
		func(ctx context.Context, in *struct{ CollectionPath }) (*Body[Unpaged[FieldDefinition]], error) {
			out, err := a.DMS.Fields(ctx, subject(ctx), in.ID)
			return unpaged(out, err, infallible(fieldOut))
		})
	register(a, api, huma.Operation{OperationID: "createField", Method: http.MethodPost, Path: "/v1/resources/{id}/fields", Summary: "Add a field definition (manage required)", Tags: []string{"Fields"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct {
			CollectionPath
			Body dms.CreateField
		}) (*Body[FieldDefinition], error) {
			out, err := a.DMS.CreateField(ctx, subject(ctx), in.ID, in.Body)
			if err != nil {
				return nil, err
			}
			return &Body[FieldDefinition]{fieldOut(out)}, nil
		})
	register(a, api, huma.Operation{OperationID: "updateField", Method: http.MethodPatch, Path: "/v1/resources/{id}/fields/{fieldID}", Summary: "Evolve field validation and index configuration (manage required)", Description: "Uses the collection ETag. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.", Tags: []string{"Fields"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			FieldPath
			precondition
			Body dms.UpdateField
		}) (*Tagged[FieldDefinition], error) {
			out, err := a.DMS.UpdateField(ctx, subject(ctx), in.ID, in.FieldID, in.Version, in.Body)
			if err != nil {
				return nil, err
			}
			return &Tagged[FieldDefinition]{ETag: etag(in.Version + 1), Body: fieldOut(out)}, nil
		})
	register(a, api, huma.Operation{OperationID: "deleteField", Method: http.MethodDelete, Path: "/v1/resources/{id}/fields/{fieldID}", Summary: "Remove a field from the active schema (manage required)", Description: "Uses the collection ETag. Rejects dependencies in rules and saved views. Removes memberships, query indexes and business key claims; immutable revisions retain their original fields and values. Removed keys cannot be reused. Later edits prune carried values, while explicit replacement values reject removed keys.", Tags: []string{"Fields"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			FieldPath
			precondition
		}) (*TaggedEmpty, error) {
			if err := a.DMS.DeleteField(ctx, subject(ctx), in.ID, in.FieldID, in.Version); err != nil {
				return nil, err
			}
			return &TaggedEmpty{ETag: etag(in.Version + 1)}, nil
		})
	register(a, api, huma.Operation{OperationID: "listSchemaRevisions", Method: http.MethodGet, Path: "/v1/resources/{id}/schemas", Summary: "Read immutable collection schema history (manage required)", Tags: []string{"Fields"}},
		func(ctx context.Context, in *struct {
			CollectionPath
			RevisionPaging
		}) (*Body[Unpaged[SchemaRevision]], error) {
			out, err := a.DMS.Schemas(ctx, subject(ctx), in.ID, in.AfterRevision, in.Limit)
			return unpaged(out, err, schemaRevisionOut)
		})

	register(a, api, huma.Operation{OperationID: "listWebDAVCredentials", Method: http.MethodGet, Path: "/v1/webdav-credentials", Summary: "List the caller's WebDAV credentials", Tags: []string{"WebDAV credentials"}},
		func(ctx context.Context, _ *struct{}) (*Body[Unpaged[WebDAVCredential]], error) {
			out, err := a.DMS.WebDAVCredentials(ctx, subject(ctx))
			return unpaged(out, err, infallible(credentialOut))
		})
	// WebDAV credentials belong to the calling principal, not to a workspace, so
	// issuing and revoking them is logged rather than written to a workspace audit.
	register(a, api, huma.Operation{OperationID: "createWebDAVCredential", Method: http.MethodPost, Path: "/v1/webdav-credentials", Summary: "Issue a WebDAV app password for the caller (at most 20)", Description: "Clients such as Windows Explorer authenticate to /webdav/ with HTTP Basic: any user name and this password. The password is returned only in this response; only its SHA-256 is stored.", Tags: []string{"WebDAV credentials"}, DefaultStatus: 201},
		func(ctx context.Context, in *struct{ Body dms.WebDAVCredentialInput }) (*Body[NewWebDAVCredential], error) {
			out, err := a.DMS.CreateWebDAVCredential(ctx, subject(ctx), in.Body)
			if err != nil {
				return nil, err
			}
			a.Logger.InfoContext(ctx, "webdav credential created", "credential_id", out.ID, "subject", out.Subject, "request_id", ctx.Value(requestKey{}))
			return &Body[NewWebDAVCredential]{NewWebDAVCredential{credentialOut(out.WebDAVCredential), out.Password}}, nil
		})
	register(a, api, huma.Operation{OperationID: "revokeWebDAVCredential", Method: http.MethodDelete, Path: "/v1/webdav-credentials/{id}", Summary: "Revoke one of the caller's WebDAV credentials", Tags: []string{"WebDAV credentials"}},
		func(ctx context.Context, in *struct{ EntityPath }) (*Empty, error) {
			if err := a.DMS.RevokeWebDAVCredential(ctx, subject(ctx), in.ID); err != nil {
				return nil, err
			}
			a.Logger.InfoContext(ctx, "webdav credential revoked", "credential_id", in.ID, "subject", subject(ctx), "request_id", ctx.Value(requestKey{}))
			return &Empty{}, nil
		})
}
