package httpapi

import (
	"net/http"
	"strconv"

	"papergo/internal/comments"
)

func (a *API) registerComments(api *router) {
	if a.CommentsOff {
		return
	}
	api.HandleFunc("GET /v1/items/{id}/comments", a.itemComments)
	api.HandleFunc("POST /v1/items/{id}/comments", a.createComment)
	api.HandleFunc("GET /v1/items/{id}/mentionable", a.mentionable)
	api.HandleFunc("GET /v1/comments/{id}", a.getComment)
	api.HandleFunc("PATCH /v1/comments/{id}", a.editComment)
	api.HandleFunc("DELETE /v1/comments/{id}", a.deleteComment)
	api.HandleFunc("POST /v1/comments/{id}/resolve", a.resolveComment(true))
	api.HandleFunc("POST /v1/comments/{id}/reopen", a.resolveComment(false))
	api.HandleFunc("POST /v1/comments/counts", a.commentCounts)
	api.HandleFunc("GET /v1/resources/{id}/settings/comments", a.commentSettings)
	api.HandleFunc("PUT /v1/resources/{id}/settings/comments", a.setCommentSettings)
	api.HandleFunc("GET /v1/organization/settings/comments", a.organizationCommentSettings)
	api.HandleFunc("PUT /v1/organization/settings/comments", a.setOrganizationCommentSettings)
}

func (a *API) comments(r *http.Request) *comments.Service {
	return &comments.Service{DMS: a.dms(r), Enabled: !a.CommentsOff}
}

func (a *API) itemComments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("$top"))
	out, err := a.comments(r).Threads(r.Context(), subject(r), r.PathValue("id"), comments.Status(q.Get("status")), limit, q.Get("$skiptoken"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

// commentInput is a comment as POST /v1/items/{id}/comments takes it.
type commentInput struct {
	Body     string `json:"body"`
	ThreadID string `json:"thread_id"`
}

func (a *API) createComment(w http.ResponseWriter, r *http.Request) {
	var in commentInput
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.comments(r).Create(r.Context(), subject(r), runOf(r), r.PathValue("id"), comments.Input{Body: in.Body, ThreadID: in.ThreadID})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/comments/"+out.ID)
	etag(w, int(out.Version))
	respond(w, r, 201, out)
}

func (a *API) getComment(w http.ResponseWriter, r *http.Request) {
	out, err := a.comments(r).Get(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) editComment(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.comments(r).Edit(r.Context(), subject(r), r.PathValue("id"), int64(version), in.Body)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) deleteComment(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	if err := a.comments(r).Delete(r.Context(), subject(r), r.PathValue("id"), int64(version)); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) resolveComment(resolved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, ok := a.version(w, r)
		if !ok {
			return
		}
		out, err := a.comments(r).Resolve(r.Context(), subject(r), r.PathValue("id"), int64(version), resolved)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, int(out.Version))
		respond(w, r, 200, out)
	}
}

func (a *API) commentCounts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ItemIDs []string `json:"item_ids"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.comments(r).Counts(r.Context(), subject(r), in.ItemIDs)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"counts": out})
}

func (a *API) mentionable(w http.ResponseWriter, r *http.Request) {
	out, err := a.comments(r).Mentionable(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("q"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out})
}

func (a *API) commentSettings(w http.ResponseWriter, r *http.Request) {
	out, err := a.comments(r).PlaceSettings(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) setCommentSettings(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionAtLeast(w, r, 0)
	if !ok {
		return
	}
	var in struct {
		Mode *comments.Mode `json:"mode"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.comments(r).SetPlace(r.Context(), subject(r), r.PathValue("id"), int64(version), in.Mode)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) organizationCommentSettings(w http.ResponseWriter, r *http.Request) {
	out, err := a.comments(r).Organization(r.Context())
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) setOrganizationCommentSettings(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionAtLeast(w, r, 0)
	if !ok {
		return
	}
	var in struct {
		Enabled *bool          `json:"enabled"`
		Mode    *comments.Mode `json:"mode"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		a.problem(w, r, 422, "validation_failed", "enabled is required")
		return
	}
	out, err := a.comments(r).SetOrganization(r.Context(), subject(r), int64(version), *in.Enabled, in.Mode)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}
