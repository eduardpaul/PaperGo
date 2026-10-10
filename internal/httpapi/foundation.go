package httpapi

import (
	"net/http"
	"papergo/ent"
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
		out, e := fn(r, in, version)
		if e != nil {
			a.failure(w, r, e)
			return
		}
		entityETag(w, out)
		respond(w, status, out)
	}
}
func entityETag(w http.ResponseWriter, v any) {
	switch x := v.(type) {
	case identity.Group:
		etag(w, x.Version)
	case identity.Role:
		etag(w, x.Version)
	case *ent.Resource:
		etag(w, x.Version)
	case *ent.SchemaTemplate:
		etag(w, x.Version)
	case *ent.FieldDefinition:
		etag(w, x.Version)
	case *ent.ContentType:
		etag(w, x.Version)
	case *ent.SmartFolder:
		etag(w, x.Version)
	case dms.SmartFolderImportResult:
		etag(w, x.WorkspaceVersion)
	case *ent.TermSet:
		etag(w, x.Version)
	case *ent.TermGroup:
		etag(w, x.Version)
	case dms.TermView:
		etag(w, x.Version)
	case *ent.ListView:
		etag(w, x.Version)
	case dms.RelationshipTypeView:
		etag(w, x.Version)
	case *ent.Relationship:
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
		respond(w, 200, out)
	}
}
func (a *API) registerFoundation(m *http.ServeMux) {
	m.HandleFunc("GET /v1/collection-templates", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.CollectionTemplates(r.Context(), subject(r))
	}))
	m.HandleFunc("GET /v1/content-types/audit", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.ContentTypeAudit(r.Context(), subject(r), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("GET /v1/content-types", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.CatalogContentTypes(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/content-types", foundationMutation(a, 201, false, func(r *http.Request, in dms.ContentTypeInput, v int) (any, error) {
		return a.DMS.CreateContentType(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/fields", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.CatalogFields(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/fields", foundationMutation(a, 201, false, func(r *http.Request, in dms.CreateField, v int) (any, error) {
		return a.DMS.CreateCatalogField(r.Context(), subject(r), in)
	}))

	m.HandleFunc("PATCH /v1/fields/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.UpdateField, v int) (any, error) {
		return a.DMS.UpdateCatalogField(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/fields/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if e := a.DMS.DeleteCatalogField(r.Context(), subject(r), r.PathValue("id"), v); e != nil {
			a.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /v1/resources/{id}/content-types", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.CollectionContentTypes(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/content-types", foundationMutation(a, 201, true, func(r *http.Request, in dms.AttachContentTypeInput, v int) (any, error) {
		return a.DMS.AttachContentType(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("GET /v1/content-types/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.GetContentType(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/content-types/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.ContentTypeInput, v int) (any, error) {
		return a.DMS.UpdateContentType(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/resources/{id}/fields/{fieldID}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if err := a.DMS.DeleteField(r.Context(), subject(r), r.PathValue("id"), r.PathValue("fieldID"), v); err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, v+1)
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /v1/resources/{id}/bulk", foundationMutation(a, 200, false, func(r *http.Request, in dms.BulkRequest, v int) (any, error) {
		return a.DMS.Bulk(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/templates", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.Templates(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/templates", foundationMutation(a, 201, false, func(r *http.Request, in dms.TemplateInput, v int) (any, error) {
		return a.DMS.CreateTemplate(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/templates/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.Template(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/templates/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TemplateInput, v int) (any, error) {
		return a.DMS.UpdateTemplate(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/resources/{id}/templates/{templateID}/apply", foundationMutation(a, 200, true, func(r *http.Request, in dms.ApplyTemplateInput, v int) (any, error) {
		return a.DMS.ApplyTemplate(r.Context(), subject(r), r.PathValue("id"), r.PathValue("templateID"), v, in.TemplateVersion, in.ContentTypeID)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.DMS.Query(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.DMS.QueryGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/resources/{id}/operations", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.Operations(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("GET /v1/operations/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.Operation(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("POST /v1/resources/{id}/bulk-update", func(w http.ResponseWriter, r *http.Request) {
		var in dms.FilteredBulkUpdate
		if !a.decode(w, r, &in) {
			return
		}
		op, e := a.DMS.StartBulkUpdate(r.Context(), subject(r), r.PathValue("id"), in)
		if e != nil {
			a.failure(w, r, e)
			return
		}
		w.Header().Set("Location", "/v1/operations/"+op.ID)
		respond(w, http.StatusAccepted, op)
	})
	m.HandleFunc("GET /v1/resources/{id}/views", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.Views(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/views", foundationMutation(a, 201, false, func(r *http.Request, in dms.ViewInput, v int) (any, error) {
		return a.DMS.CreateView(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/views/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.View(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/views/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.ViewInput, v int) (any, error) {
		return a.DMS.UpdateView(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/views/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.ViewQueryRequest, v int) (any, error) {
		return a.DMS.QueryView(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/views/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.ViewQueryRequest, v int) (any, error) {
		return a.DMS.QueryViewGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	a.registerTaxonomy(m)
	m.HandleFunc("GET /v1/relationship-types", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.RelationshipTypes(r.Context(), subject(r), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/relationship-types", foundationMutation(a, 201, false, func(r *http.Request, in dms.RelationshipTypeInput, v int) (any, error) {
		return a.DMS.CreateRelationshipType(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/relationship-types/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.RelationshipType(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PATCH /v1/items/{id}/relationships/{linkID}", foundationMutation(a, 200, true, func(r *http.Request, in dms.UpdateRelationship, v int) (any, error) {
		return a.DMS.UpdateRelationship(r.Context(), subject(r), r.PathValue("id"), r.PathValue("linkID"), v, in)
	}))
	m.HandleFunc("DELETE /v1/views/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if e := a.DMS.DeleteView(r.Context(), subject(r), r.PathValue("id"), v); e != nil {
			a.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	})
}
