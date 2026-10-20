package httpapi

import (
	"net/http"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/resources"
	viewstore "papergo/internal/dal/views"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

func foundationMutation[T any](a *API, status int, precondition bool, fn func(*http.Request, T, int) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := 0
		if precondition {
			v, ok := a.version(w, r)
			if !ok {
				return
			}
			version = v
		}
		var in T
		if !a.decode(w, r, &in) {
			return
		}
		switch query := any(&in).(type) {
		case *dms.QueryRequest:
			query.After = r.URL.Query().Get("$skiptoken")
			query.Limit = queryInt(r, "$top")
		case *dms.ViewQueryRequest:
			query.After = r.URL.Query().Get("$skiptoken")
			query.Limit = queryInt(r, "$top")
		case *dms.SmartFolderQueryRequest:
			query.After = r.URL.Query().Get("$skiptoken")
			query.Limit = queryInt(r, "$top")
		}
		out, e := fn(r, in, version)
		if e != nil {
			a.failure(w, r, e)
			return
		}
		entityETag(w, out)
		respond(w, r, status, out)
	}
}
func entityETag(w http.ResponseWriter, v any) {
	switch x := v.(type) {
	case identity.Organization:
		etag(w, x.Version)
	case identity.Group:
		etag(w, x.Version)
	case identity.Role:
		etag(w, x.Version)
	case *resources.Resource:
		etag(w, x.Version)
	case *catalog.Template:
		etag(w, x.Version)
	case *catalog.FieldDefinition:
		etag(w, x.Version)
	case *catalog.ContentType:
		etag(w, x.Version)
	case *viewstore.SmartFolder:
		etag(w, x.Version)
	case dms.SmartFolderImportResult:
		etag(w, x.WorkspaceVersion)
	case dms.TermSetView:
		etag(w, x.Version)
	case dms.TermGroupView:
		etag(w, x.Version)
	case dms.TermView:
		etag(w, x.Version)
	case *viewstore.ListView:
		etag(w, x.Version)
	case *dms.IntegrationView:
		etag(w, x.Version)
	case dms.RelationshipTypeView:
		etag(w, x.Version)
	case dms.RelationshipRecord:
		etag(w, x.Version)
	case dms.RelationshipView:
		etag(w, x.Version)
	}
}
func foundationRead(a *API, fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, e := fn(r)
		if e != nil {
			a.failure(w, r, e)
			return
		}
		entityETag(w, out)
		respond(w, r, 200, out)
	}
}
func (a *API) registerFoundation(m *router) {
	m.HandleFunc("GET /v1/collection-templates", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).CollectionTemplates(r.Context(), subject(r))
	}))
	m.HandleFunc("GET /v1/content-types", foundationRead(a, func(r *http.Request) (any, error) { return a.dms(r).CatalogContentTypes(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/content-types", foundationMutation(a, 201, false, func(r *http.Request, in dms.ContentTypeInput, v int) (any, error) {
		return a.dms(r).CreateContentType(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/fields", foundationRead(a, func(r *http.Request) (any, error) { return a.dms(r).CatalogFields(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/fields", foundationMutation(a, 201, false, func(r *http.Request, in dms.CreateField, v int) (any, error) {
		return a.dms(r).CreateCatalogField(r.Context(), subject(r), in)
	}))

	m.HandleFunc("PATCH /v1/fields/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.UpdateField, v int) (any, error) {
		return a.dms(r).UpdateCatalogField(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/fields/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if e := a.dms(r).DeleteCatalogField(r.Context(), subject(r), r.PathValue("id"), v); e != nil {
			a.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /v1/resources/{id}/content-types", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).CollectionContentTypes(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/content-types", foundationMutation(a, 201, true, func(r *http.Request, in dms.AttachContentTypeInput, v int) (any, error) {
		return a.dms(r).AttachContentType(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/resources/{id}/content-types/{typeID}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		out, err := a.dms(r).DetachContentType(r.Context(), subject(r), r.PathValue("id"), r.PathValue("typeID"), v)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, out.Version)
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /v1/content-types/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).GetContentType(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/content-types/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.ContentTypeInput, v int) (any, error) {
		return a.dms(r).UpdateContentType(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/resources/{id}/fields/{fieldID}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if err := a.dms(r).DeleteField(r.Context(), subject(r), r.PathValue("id"), r.PathValue("fieldID"), v); err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, v+1)
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /v1/resources/{id}/bulk", foundationMutation(a, 200, false, func(r *http.Request, in dms.BulkRequest, v int) (any, error) {
		return a.dms(r).Bulk(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/templates", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Templates(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/templates", foundationMutation(a, 201, false, func(r *http.Request, in dms.TemplateInput, v int) (any, error) {
		return a.dms(r).CreateTemplate(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/templates/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Template(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/templates/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TemplateInput, v int) (any, error) {
		return a.dms(r).UpdateTemplate(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/resources/{id}/templates/{templateID}/apply", foundationMutation(a, 200, true, func(r *http.Request, in dms.ApplyTemplateInput, v int) (any, error) {
		return a.dms(r).ApplyTemplate(r.Context(), subject(r), r.PathValue("id"), r.PathValue("templateID"), v, in.TemplateVersion, in.ContentTypeID)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.dms(r).Query(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.dms(r).QueryGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/resources/{id}/operations", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Operations(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
	}))
	m.HandleFunc("GET /v1/operations/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Operation(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/operations/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		op, err := a.dms(r).CancelOperation(r.Context(), subject(r), r.PathValue("id"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		respond(w, r, 200, op)
	})
	m.HandleFunc("POST /v1/resources/{id}/bulk-update", a.idempotent(func(w http.ResponseWriter, r *http.Request) {
		var in dms.FilteredBulkUpdate
		if !a.decode(w, r, &in) {
			return
		}
		op, e := a.dms(r).StartBulkUpdate(r.Context(), subject(r), r.PathValue("id"), in)
		if e != nil {
			a.failure(w, r, e)
			return
		}
		Accepted(w, r, op)
	}))
	m.HandleFunc("GET /v1/resources/{id}/views", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Views(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/views", foundationMutation(a, 201, false, func(r *http.Request, in dms.ViewInput, v int) (any, error) {
		return a.dms(r).CreateView(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/views/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.dms(r).View(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/views/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.ViewInput, v int) (any, error) {
		return a.dms(r).UpdateView(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/views/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.ViewQueryRequest, v int) (any, error) {
		return a.dms(r).QueryView(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/views/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.ViewQueryRequest, v int) (any, error) {
		return a.dms(r).QueryViewGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	a.registerTaxonomy(m)
	m.HandleFunc("GET /v1/relationship-types", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).RelationshipTypes(r.Context(), subject(r), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
	}))
	m.HandleFunc("POST /v1/relationship-types", foundationMutation(a, 201, false, func(r *http.Request, in dms.RelationshipTypeInput, v int) (any, error) {
		return a.dms(r).CreateRelationshipType(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/relationship-types/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).RelationshipType(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("GET /v1/items/{id}/relationships/{linkID}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).Relationship(r.Context(), subject(r), r.PathValue("id"), r.PathValue("linkID"))
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/relationships", a.workspaceRelationships)
	m.HandleFunc("GET /v1/workspaces/{id}/relationships/types", a.workspaceRelationshipTypes)
	m.HandleFunc("GET /v1/workspaces/{id}/relationships/items", a.workspaceRelationshipItems)
	m.HandleFunc("GET /v1/workspaces/{id}/relationships/neighborhood", a.workspaceRelationshipNeighborhood)
	m.HandleFunc("PATCH /v1/items/{id}/relationships/{linkID}", foundationMutation(a, 200, true, func(r *http.Request, in dms.UpdateRelationship, v int) (any, error) {
		return a.dms(r).UpdateRelationship(r.Context(), subject(r), r.PathValue("id"), r.PathValue("linkID"), v, in)
	}))
	m.HandleFunc("DELETE /v1/views/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if e := a.dms(r).DeleteView(r.Context(), subject(r), r.PathValue("id"), v); e != nil {
			a.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	})
}
