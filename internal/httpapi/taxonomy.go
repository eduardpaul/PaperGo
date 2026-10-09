package httpapi

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"papergo/internal/dms"
)

func (a *API) registerTaxonomy(m *http.ServeMux) {
	m.HandleFunc("GET /v1/workspaces/{id}/term-groups", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.TermGroups(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/term-groups", foundationMutation(a, 201, false, func(r *http.Request, in dms.TermGroupInput, v int) (any, error) {
		return a.DMS.CreateTermGroup(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/term-groups/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.TermGroup(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/term-groups/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TermGroupInput, v int) (any, error) {
		return a.DMS.UpdateTermGroup(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/term-groups/{id}", a.deletion(func(r *http.Request, v int) error {
		return a.DMS.DeleteTermGroup(r.Context(), subject(r), r.PathValue("id"), v)
	}))
	m.HandleFunc("POST /v1/term-groups/{id}/import", a.importTerms)
	m.HandleFunc("GET /v1/workspaces/{id}/taxonomy/export", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.ExportTaxonomy(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/taxonomy/import", foundationMutation(a, 200, false, func(r *http.Request, in dms.TaxonomyPackage, v int) (any, error) {
		return a.DMS.ImportTaxonomy(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/term-sets", foundationRead(a, func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return a.DMS.TermSets(r.Context(), subject(r), r.PathValue("id"), q.Get("group_id"), q.Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/term-sets", foundationMutation(a, 201, false, func(r *http.Request, in dms.TermSetInput, v int) (any, error) {
		return a.DMS.CreateTermSet(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/term-sets/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.TermSet(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/term-sets/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TermSetInput, v int) (any, error) {
		return a.DMS.UpdateTermSet(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/term-sets/{id}", a.deletion(func(r *http.Request, v int) error {
		return a.DMS.DeleteTermSet(r.Context(), subject(r), r.PathValue("id"), v)
	}))
	m.HandleFunc("GET /v1/term-sets/{id}/terms", foundationRead(a, func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return a.DMS.Terms(r.Context(), subject(r), r.PathValue("id"), dms.TermsQuery{ParentID: q.Get("parent_id"), Search: q.Get("q"),
			IncludeDeprecated: q.Get("include_deprecated") == "true", After: q.Get("after"), Limit: queryInt(r, "limit")})
	}))
	m.HandleFunc("POST /v1/term-sets/{id}/terms", foundationMutation(a, 201, false, func(r *http.Request, in dms.TermInput, v int) (any, error) {
		return a.DMS.CreateTerm(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/terms", foundationRead(a, func(r *http.Request) (any, error) {
		ids := []string{}
		for _, v := range r.URL.Query()["ids"] {
			for _, id := range strings.Split(v, ",") {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
		}
		out, e := a.DMS.TermsByID(r.Context(), subject(r), ids)
		return map[string]any{"data": out}, e
	}))
	m.HandleFunc("GET /v1/terms/{id}", foundationRead(a, func(r *http.Request) (any, error) { return a.DMS.Term(r.Context(), subject(r), r.PathValue("id")) }))
	m.HandleFunc("PUT /v1/terms/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.TermInput, v int) (any, error) {
		return a.DMS.UpdateTerm(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/terms/{id}/move", foundationMutation(a, 200, true, func(r *http.Request, in dms.MoveTermInput, v int) (any, error) {
		return a.DMS.MoveTerm(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/terms/{id}/merge", foundationMutation(a, 200, true, func(r *http.Request, in dms.MergeTermInput, v int) (any, error) {
		return a.DMS.MergeTerm(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/keywords", foundationRead(a, func(r *http.Request) (any, error) {
		out, e := a.DMS.Keywords(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("q"), queryInt(r, "limit"))
		return map[string]any{"data": out}, e
	}))
	m.HandleFunc("POST /v1/workspaces/{id}/keywords", func(w http.ResponseWriter, r *http.Request) {
		var in dms.KeywordInput
		if !a.decode(w, r, &in) {
			return
		}
		out, created, err := a.DMS.AddKeyword(r.Context(), subject(r), r.PathValue("id"), in)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		etag(w, out.Version)
		respond(w, status, out)
	})
	m.HandleFunc("GET /v1/workspaces/{id}/keywords/popular", foundationRead(a, func(r *http.Request) (any, error) {
		out, e := a.DMS.PopularKeywords(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "top"))
		return map[string]any{"data": out}, e
	}))
	m.HandleFunc("POST /v1/keywords/{id}/promote", foundationMutation(a, 200, true, func(r *http.Request, in dms.PromoteKeywordInput, v int) (any, error) {
		return a.DMS.PromoteKeyword(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
}

// deletion handles a DELETE that needs the resource's ETag.
func (a *API) deletion(fn func(*http.Request, int) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if e := fn(r, v); e != nil {
			a.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
	}
}

// importTerms reads a SharePoint term set CSV of up to 1 MiB.
func (a *API) importTerms(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "text/csv" {
		a.problem(w, r, 415, "unsupported_media_type", "Content-Type must be text/csv")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, dms.MaxTermImportBytes))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	out, err := a.DMS.ImportTerms(r.Context(), subject(r), r.PathValue("id"), bytes.NewReader(body))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}
