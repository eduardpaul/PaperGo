package httpapi

import (
	"net/http"
	"papergo/ent"
	"papergo/internal/dms"
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
	case *ent.Resource:
		etag(w, x.Version)
	case *ent.SchemaTemplate:
		etag(w, x.Version)
	case *ent.TermSet:
		etag(w, x.Version)
	case *ent.Term:
		etag(w, x.Version)
	case *ent.ListView:
		etag(w, x.Version)
	case *ent.RelationshipType:
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
		return a.DMS.ApplyTemplate(r.Context(), subject(r), r.PathValue("id"), r.PathValue("templateID"), v, in.TemplateVersion)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.DMS.Query(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/resources/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.QueryRequest, v int) (any, error) {
		return a.DMS.QueryGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
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
	m.HandleFunc("GET /v1/workspaces/{id}/term-sets", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.TermSets(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/term-sets", foundationMutation(a, 201, false, func(r *http.Request, in dms.TermSetInput, v int) (any, error) {
		return a.DMS.CreateTermSet(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/term-sets/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.TermSet(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/term-sets/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TermSetInput, v int) (any, error) {
		return a.DMS.UpdateTermSet(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("GET /v1/term-sets/{id}/terms", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.Terms(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("q"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/term-sets/{id}/terms", foundationMutation(a, 201, false, func(r *http.Request, in dms.TermInput, v int) (any, error) {
		return a.DMS.CreateTerm(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/terms/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.Term(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/terms/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TermInput, v int) (any, error) {
		return a.DMS.UpdateTerm(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/relationship-types", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.RelationshipTypes(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/relationship-types", foundationMutation(a, 201, false, func(r *http.Request, in dms.RelationshipTypeInput, v int) (any, error) {
		return a.DMS.CreateRelationshipType(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/relationship-types/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.RelationshipType(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/relationship-types/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.RelationshipTypeInput, v int) (any, error) {
		return a.DMS.UpdateRelationshipType(r.Context(), subject(r), r.PathValue("id"), v, in)
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
