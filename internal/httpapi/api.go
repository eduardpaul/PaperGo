package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"papergo/internal/runner"
	"papergo/internal/storage"
	"papergo/internal/transfer"
	"papergo/internal/webdav"
	"papergo/internal/workflow"
	_ "papergo/internal/workflowai" // ai activities for catalog tests that do not import the runner
	"papergo/web"
	"strconv"
	"strings"
	"time"
)

type API struct {
	DMS       *dms.Service
	Workflows *workflow.Service
	Runner    *runner.Runner
	Auth      auth.Authenticator
	Storage   storage.Store
	Logger    *slog.Logger
	Ready     func(context.Context) error
	MaxUpload int64
	// MaxInFlight and RequestTimeout bound API work; zero disables each.
	MaxInFlight    int
	RequestTimeout time.Duration
	// WebDAV serves WebDAV-enabled libraries under webdav.Prefix with its own authentication.
	WebDAV *webdav.Handler
	// PublicURL, when set, supplies the scheme and host of absolute links, so
	// they are correct behind a TLS-terminating reverse proxy.
	PublicURL *url.URL
	// BrowserSignIn, when set, tells the web frontend which OIDC client signs
	// users in. It is public configuration: an issuer, a client ID and scopes.
	BrowserSignIn *BrowserSignIn
}

// BrowserSignIn describes the public OIDC client of the web frontend
// (authorization code flow with PKCE).
type BrowserSignIn struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

type subjectKey struct{}
type requestKey struct{}
type publicURLKey struct{}

func subject(r *http.Request) string { v, _ := r.Context().Value(subjectKey{}).(string); return v }
func (a *API) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/events", a.events)
	a.registerPortability(api)
	a.registerIdentity(api)
	a.registerFoundation(api)
	a.registerIntegrations(api)
	a.registerSmartFolders(api)
	a.registerWorkflows(api)
	a.registerSearch(api)
	a.registerTasks(api)
	// Subrequests already share the outer admission slot; authenticating them
	// separately preserves authorization overrides without recursively queueing.
	api.HandleFunc("POST /v1/$batch", a.batch(a.observe(a.authenticate(api))))
	api.HandleFunc("GET /v1/workspaces", a.browse)
	api.HandleFunc("POST /v1/workspaces", a.workspace)
	api.HandleFunc("GET /v1/workspaces/{id}/members", a.workspaceMembers)
	api.HandleFunc("POST /v1/workspaces/{id}/members", a.setWorkspaceMember)
	api.HandleFunc("DELETE /v1/workspaces/{id}/members/{member}", a.removeWorkspaceMember)
	api.HandleFunc("GET /v1/resources", a.browse)
	api.HandleFunc("GET /v1/resources/{id}", a.get)
	api.HandleFunc("PATCH /v1/resources/{id}", a.update)
	api.HandleFunc("DELETE /v1/resources/{id}", a.delete)
	api.HandleFunc("GET /v1/resources/{id}/children", a.children)
	api.HandleFunc("GET /v1/resources/{id}/delta", a.delta)
	api.HandleFunc("POST /v1/resources/{id}/children", a.create)
	api.HandleFunc("POST /v1/resources/{id}/documents", a.createDocument)
	api.HandleFunc("GET /v1/resources/{id}/fields", a.fields)
	api.HandleFunc("POST /v1/resources/{id}/fields", a.createField)
	api.HandleFunc("PATCH /v1/resources/{id}/fields/{fieldID}", a.updateField)
	api.HandleFunc("GET /v1/resources/{id}/schemas", a.schemas)
	api.HandleFunc("GET /v1/resources/{id}/permissions", a.permissions)
	api.HandleFunc("PUT /v1/resources/{id}/permissions", a.setPermissions)
	api.HandleFunc("GET /v1/items/{id}/relationships", a.relationships)
	api.HandleFunc("POST /v1/resources/{id}/move-selection", foundationMutation(a, 200, false, func(r *http.Request, in dms.MoveSelection, _ int) (any, error) {
		return a.DMS.MoveSelection(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	api.HandleFunc("POST /v1/items/{id}/move", foundationMutation(a, 200, true, func(r *http.Request, in dms.MoveItem, version int) (any, error) {
		return a.DMS.MoveItem(r.Context(), subject(r), r.PathValue("id"), version, in)
	}))
	api.HandleFunc("POST /v1/items/{id}/relationships", a.link)
	api.HandleFunc("DELETE /v1/items/{id}/relationships/{linkID}", a.unlink)
	api.HandleFunc("POST /v1/items/{id}/publications", a.publish)
	api.HandleFunc("GET /v1/items/{id}/publications", a.publications)
	api.HandleFunc("POST /v1/items/{id}/unpublish", a.unpublish)
	api.HandleFunc("GET /v1/items/{id}/revisions", a.revisions)
	api.HandleFunc("GET /v1/items/{id}/revisions/{number}", a.revision)
	api.HandleFunc("POST /v1/items/{id}/revisions/{number}/restore", a.restoreRevision)
	api.HandleFunc("GET /v1/resources/{id}/recycle-bin", a.recycleBin)
	api.HandleFunc("POST /v1/resources/{id}/recycle-bin/{itemID}/restore", a.restoreDeleted)
	api.HandleFunc("DELETE /v1/resources/{id}/recycle-bin/{itemID}", a.purgeDeleted)
	api.HandleFunc("GET /v1/items/{id}/schema", a.itemSchema)
	api.HandleFunc("PUT /v1/items/{id}/content", a.upload)
	api.HandleFunc("GET /v1/items/{id}/content", a.download)
	api.HandleFunc("GET /v1/items/{id}/preview", a.preview)
	api.HandleFunc("GET /v1/items/{id}/content/versions", a.fileVersions)
	api.HandleFunc("GET /v1/items/{id}/text", a.itemText)
	api.HandleFunc("POST /v1/items/{id}/content/versions/{number}/restore", a.restoreBlob)
	api.HandleFunc("GET /v1/audit", a.audit)
	api.HandleFunc("GET /v1/workspaces/{id}/keywords", a.keywords)
	if a.Auth == nil || a.Auth.Kind() != auth.TrustedProxy {
		api.HandleFunc("GET /v1/webdav-credentials", a.webdavCredentials)
		api.HandleFunc("POST /v1/webdav-credentials", a.createWebDAVCredential)
		api.HandleFunc("DELETE /v1/webdav-credentials/{id}", a.revokeWebDAVCredential)
	}
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { a.problem(w, r, 404, "not_found", "route not found") })
	root := http.NewServeMux()
	root.HandleFunc("GET /auth/config", func(w http.ResponseWriter, r *http.Request) {
		mode := auth.Bearer
		if a.Auth != nil {
			mode = a.Auth.Kind()
		}
		var signIn *BrowserSignIn
		if mode == auth.Bearer {
			signIn = a.BrowserSignIn
		}
		respond(w, r, 200, struct {
			Mode auth.Kind      `json:"mode"`
			OIDC *BrowserSignIn `json:"oidc,omitempty"`
		}{mode, signIn})
	})
	if local, ok := a.Auth.(auth.Local); ok {
		root.HandleFunc("POST /auth/login", a.localLogin(local))
		root.HandleFunc("POST /auth/logout", a.localLogout(local))
	}
	root.Handle("/auth/", http.NotFoundHandler())
	root.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, r, 200, map[string]string{"status": "ok"}) })
	root.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.Ready(ctx); err != nil {
			a.problem(w, r, 503, "unavailable", "service is not ready")
			return
		}
		respond(w, r, 200, map[string]string{"status": "ok"})
	})
	limit := a.limiter()
	root.Handle("GET /v1/events", a.authenticate(a.streamLimiter(http.HandlerFunc(a.events))))
	authenticated := a.authenticate(limit(api))
	root.Handle("/v1", authenticated)
	root.Handle("/v1/", authenticated)
	root.Handle("/health", http.NotFoundHandler())
	root.Handle("/health/", http.NotFoundHandler())
	root.Handle("/", web.Handler())
	if a.WebDAV != nil {
		dav := limit(a.WebDAV)
		root.Handle(webdav.Prefix, dav)
		root.Handle(strings.TrimSuffix(webdav.Prefix, "/"), dav)
		// Windows and Office discover WebDAV support at the server root.
		root.Handle("OPTIONS /{$}", a.WebDAV)
	} else {
		root.Handle(webdav.Prefix, http.NotFoundHandler())
		root.Handle(strings.TrimSuffix(webdav.Prefix, "/"), http.NotFoundHandler())
	}
	return a.observe(root)
}

// admissionWait is how long a request may queue for a slot before it is shed.
const admissionWait = time.Second

// limiter returns middleware that sheds load instead of queueing without bound:
// past MaxInFlight concurrent requests, across every handler it wraps, a caller
// waits at most admissionWait, then gets 503 with Retry-After. Admitted reads
// are cancelled after RequestTimeout. Content transfers stream for long periods
// under their own deadlines, so they bypass both.
func (a *API) limiter() func(http.Handler) http.Handler {
	var slots chan struct{}
	if a.MaxInFlight > 0 {
		slots = make(chan struct{}, a.MaxInFlight)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if transferRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			if slots != nil {
				select {
				case slots <- struct{}{}:
				default:
					wait := time.NewTimer(admissionWait)
					select {
					case slots <- struct{}{}:
						wait.Stop()
					case <-wait.C:
						w.Header().Set("Retry-After", "1")
						a.problem(w, r, 503, "overloaded", "server is at capacity; retry shortly")
						return
					case <-r.Context().Done():
						wait.Stop()
						return
					}
				}
				defer func() { <-slots }()
			}
			// Only reads are time-boxed: mutations are serialized and some (index
			// rebuilds, template adoption, bulk publish) legitimately span a collection.
			if a.RequestTimeout > 0 && readRequest(r) {
				ctx, cancel := context.WithTimeout(r.Context(), a.RequestTimeout)
				defer cancel()
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// readRequest reports requests that only read: safe methods, and the typed
// queries of collections, saved views and smart folders, which POST their
// query document but never mutate.
func readRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, "PROPFIND":
		return true
	case http.MethodPost:
	default:
		return false
	}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) == 6 && parts[5] == "groups" {
		parts = parts[:5]
	}
	if len(parts) != 5 || parts[1] != "v1" || parts[4] != "query" {
		return false
	}
	switch parts[2] {
	case "resources", "views", "smart-folders":
		return true
	}
	return false
}

// transferRequest reports requests that stream a body: blob downloads and
// uploads, and WebDAV file GET/PUT. COPY and every other method do database work.
func transferRequest(r *http.Request) bool {
	if (r.Method == http.MethodPost && r.URL.Path == "/v1/portability/imports") || (r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/portability/exports/") && strings.HasSuffix(r.URL.Path, "/package")) {
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/events" {
		return true
	}
	if r.Method == http.MethodPost {
		parts := strings.Split(r.URL.Path, "/")
		return (len(parts) == 5 && parts[1] == "v1" && parts[2] == "resources" && parts[4] == "documents") || r.URL.Path == "/v1/identity/me/inbox/documents" || (len(parts) == 7 && parts[1] == "v1" && parts[2] == "identity" && parts[3] == "groups" && parts[5] == "inbox" && parts[6] == "documents")
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut:
	default:
		return false
	}
	if strings.HasPrefix(r.URL.Path, webdav.Prefix) {
		return true
	}
	parts := strings.Split(r.URL.Path, "/")
	return len(parts) == 5 && parts[1] == "v1" && parts[2] == "items" && parts[4] == "content"
}
func respond(w http.ResponseWriter, r *http.Request, status int, body any) {
	if page, ok := body.(interface {
		APIPage() (map[string]any, string)
	}); ok {
		values, cursor := page.APIPage()
		values["@odata.nextLink"] = nil
		if cursor != "" {
			next := *r.URL
			query := next.Query()
			query.Set("$skiptoken", cursor)
			next.RawQuery = query.Encode()
			if public, ok := r.Context().Value(publicURLKey{}).(*url.URL); ok {
				next.Scheme, next.Host = public.Scheme, public.Host
			}
			if next.Scheme == "" {
				next.Scheme = "http"
				if r.TLS != nil {
					next.Scheme = "https"
				}
			}
			if next.Host == "" {
				next.Host = r.Host
			}
			values["@odata.nextLink"] = next.String()
		}
		body = values
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func (a *API) problem(w http.ResponseWriter, r *http.Request, status int, code, message string, operationIndex ...int) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	id, _ := r.Context().Value(requestKey{}).(string)
	body := map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": message, "request_id": id}
	if len(operationIndex) > 0 {
		body["operation_index"] = operationIndex[0]
	}
	_ = json.NewEncoder(w).Encode(body)
}
func (a *API) failure(w http.ResponseWriter, r *http.Request, err error) {
	var bulk *dms.BulkError
	var operationIndex []int
	if errors.As(err, &bulk) {
		operationIndex = []int{bulk.Index}
	}
	problem := func(status int, code, message string) {
		a.problem(w, r, status, code, message, operationIndex...)
	}
	var moveConflict *dms.MoveNameConflict
	var nameConflict *dms.DocumentNameConflict
	var duplicate *dms.DuplicateError
	var validation *dms.ValidationError
	var identityValidation *identity.ValidationError
	var max *http.MaxBytesError
	switch {
	case errors.As(err, &moveConflict):
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		requestID, _ := r.Context().Value(requestKey{}).(string)
		body := map[string]any{"type": "about:blank", "title": "Conflict", "status": 409, "code": "name_conflict", "detail": moveConflict.Error(), "request_id": requestID, "source_item_id": moveConflict.SourceID}
		if moveConflict.Existing != nil {
			body["existing_item"] = moveConflict.Existing
		}
		_ = json.NewEncoder(w).Encode(body)
	case errors.As(err, &nameConflict):
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		requestID, _ := r.Context().Value(requestKey{}).(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "Conflict", "status": 409, "code": "name_conflict", "detail": nameConflict.Error(), "request_id": requestID, "existing_item": nameConflict})
	case errors.As(err, &duplicate):
		problem(409, "duplicate_file", duplicate.Error()+"; matching item "+duplicate.Documents[0].ItemID)
	case errors.As(err, &identityValidation):
		problem(422, "validation_failed", identityValidation.Error())
	case errors.Is(err, identity.ErrGroupManagedByIdentityProvider):
		problem(409, "group_managed_by_identity_provider", "group membership is managed by the identity provider")
	case errors.Is(err, identity.ErrLastAdministrator):
		problem(409, "last_administrator", "An organization needs at least one administrator.")
	case errors.Is(err, identity.ErrCannotDeleteSelf):
		problem(409, "cannot_delete_self", err.Error())
	case errors.Is(err, identity.ErrLastWorkspaceOwner):
		problem(409, "last_workspace_owner", err.Error())
	case errors.As(err, &validation):
		problem(422, "validation_failed", validation.Error())
	case errors.Is(err, dms.ErrNoText):
		problem(404, "text_not_read", "The file's text has not been read; a workflow with document.read_text reads it.")
	case errors.Is(err, dms.ErrSearchOff):
		problem(400, "search_off", "Search is turned off for this collection: its search.index workflow is disabled.")
	case errors.Is(err, dms.ErrSearchUnavailable):
		problem(503, "search_unavailable", "Search is unavailable while the search index is rebuilt; try again shortly.")
	case errors.Is(err, dms.ErrLiveExpired):
		problem(410, "live_cursor_expired", err.Error())
	case errors.Is(err, dms.ErrDeltaExpired):
		problem(410, "delta_expired", err.Error())
	case errors.Is(err, dms.ErrContentUnavailable):
		problem(410, "content_unavailable", "the file content is no longer available")
	case errors.Is(err, dms.ErrNotFound) || errors.Is(err, identity.ErrNotFound):
		problem(404, "not_found", "resource not found")
	case errors.Is(err, dms.ErrForbidden) || errors.Is(err, identity.ErrForbidden):
		problem(403, "forbidden", "access denied")
	case errors.Is(err, dms.ErrProtectedHome):
		problem(409, "protected_home", err.Error())
	case errors.Is(err, dms.ErrLastOwner):
		problem(409, "last_owner", "A workspace needs at least one owner. Make someone else an owner first.")
	case errors.Is(err, dms.ErrFolderNotEmpty):
		problem(409, "folder_not_empty", "The folder is not empty.")
	case errors.Is(err, dms.ErrPreconditionFailed) || errors.Is(err, identity.ErrPreconditionFailed):
		problem(412, "precondition_failed", "resource changed; read its current ETag before retrying")
	case errors.Is(err, dms.ErrIntegrationBusy):
		problem(409, "integration_busy", err.Error())
	case errors.Is(err, dms.ErrConflict) || identity.IsConstraint(err):
		problem(409, "conflict", "resource conflicts with existing state")
	case errors.Is(err, storage.ErrUnsupportedFile):
		a.problem(w, r, 415, "unsupported_file_type", err.Error())
	case errors.Is(err, storage.ErrTooLarge) || errors.As(err, &max):
		problem(413, "payload_too_large", "request body exceeds size limit")
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(r.Context().Err(), context.DeadlineExceeded):
		w.Header().Set("Retry-After", "1")
		problem(503, "timeout", "request exceeded the server time limit")
	default:
		a.Logger.ErrorContext(r.Context(), "request failed", "error", err, "request_id", r.Context().Value(requestKey{}))
		problem(500, "internal_error", "request could not be completed")
	}
}
func (a *API) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && !(r.Method == http.MethodPatch && media == "application/merge-patch+json")) {
		a.problem(w, r, 415, "unsupported_media_type", "Content-Type must be application/json, or application/merge-patch+json for PATCH")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*dms.MaxItemValuesBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(dst); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			a.failure(w, r, err)
		} else {
			a.problem(w, r, 400, "invalid_json", "invalid JSON request body")
		}
		return false
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		a.problem(w, r, 400, "invalid_json", "request must contain one JSON value")
		return false
	}
	return true
}
func etag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(version)))
}
func (a *API) version(w http.ResponseWriter, r *http.Request) (int, bool) {
	return a.versionAtLeast(w, r, 1)
}
func (a *API) versionAtLeast(w http.ResponseWriter, r *http.Request, minimum int) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		a.problem(w, r, 428, "precondition_required", "If-Match with the current resource ETag is required")
		return 0, false
	}
	raw = strings.TrimPrefix(raw, "W/")
	valid := len(raw) > 2 && raw[0] == '"' && raw[len(raw)-1] == '"'
	value := ""
	if valid {
		value = raw[1 : len(raw)-1]
		for _, c := range value {
			valid = valid && c >= '0' && c <= '9'
		}
	}
	version, err := strconv.Atoi(value)
	if !valid || err != nil || version < minimum {
		a.problem(w, r, 400, "invalid_precondition", "If-Match must contain one quoted numeric ETag")
		return 0, false
	}
	return version, true
}
func queryInt(r *http.Request, key string) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if key == "$top" {
		if err != nil {
			return 50
		}
		if v < 1 {
			return 1
		}
		if v > 500 {
			return 500
		}
	}
	return v
}
func (a *API) workspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Keywords    []string `json:"keywords"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.Create(r.Context(), subject(r), "", dms.CreateResource{Kind: "workspace", Name: in.Name, Description: in.Description, Keywords: in.Keywords})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/resources/"+out.ID)
	etag(w, out.Version)
	respond(w, r, 201, out)
}
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in dms.CreateResource
	if !a.decode(w, r, &in) {
		return
	}
	out, created, err := a.DMS.CreateWithStatus(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/resources/"+out.ID)
	etag(w, out.Version)
	status := 200
	if created {
		status = 201
	}
	respond(w, r, status, out)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.GetSurface(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("surface"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	a.respondWithAccess(w, r, out)
}

// resourceWithAccess is a single resource with the caller's allowed actions.
type resourceWithAccess struct {
	*ent.Resource
	Access []string `json:"access"`
}

func (a *API) respondWithAccess(w http.ResponseWriter, r *http.Request, out *ent.Resource) {
	access, err := a.DMS.Access(r.Context(), subject(r), out)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, resourceWithAccess{out, access})
}
func (a *API) update(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in dms.UpdateResource
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.Update(r.Context(), subject(r), r.PathValue("id"), version, in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	a.respondWithAccess(w, r, out)
}
func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var options dms.DeleteOptions
	if values, present := r.URL.Query()["recursive"]; present {
		if len(values) != 1 || values[0] != "true" && values[0] != "false" {
			a.problem(w, r, 400, "invalid_query", "recursive must be true or false")
			return
		}
		options.Recursive = values[0] == "true"
	}
	if err := a.DMS.Delete(r.Context(), subject(r), r.PathValue("id"), version, options); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) children(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.list(w, r, dms.Browse{ParentID: r.PathValue("id"), Search: q.Get("q"), Keyword: q.Get("keyword"), After: q.Get("$skiptoken"), Limit: queryInt(r, "$top"), Surface: q.Get("surface"), FilterField: q.Get("filter_field"), FilterOp: q.Get("filter_op"), FilterValue: q.Get("filter_value"), Kind: q.Get("kind")})
}
func (a *API) browse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := dms.Browse{WorkspaceID: q.Get("workspace_id"), ParentID: q.Get("parent_id"), Search: q.Get("q"), Keyword: q.Get("keyword"), After: q.Get("$skiptoken"), Limit: queryInt(r, "$top"), Surface: q.Get("surface"), FilterField: q.Get("filter_field"), FilterOp: q.Get("filter_op"), FilterValue: q.Get("filter_value"), Kind: q.Get("kind")}
	if r.URL.Path == "/v1/workspaces" {
		in = dms.Browse{After: q.Get("$skiptoken"), Limit: queryInt(r, "$top")}
	}
	a.list(w, r, in)
}
func (a *API) list(w http.ResponseWriter, r *http.Request, in dms.Browse) {
	out, err := a.DMS.Browse(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) fields(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Fields(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
}
func (a *API) createField(w http.ResponseWriter, r *http.Request) {
	var in dms.CreateField
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.CreateField(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 201, out)
}
func (a *API) permissions(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Permissions(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) setPermissions(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in dms.Permissions
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.SetPermissions(r.Context(), subject(r), r.PathValue("id"), version, in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
}
func (a *API) link(w http.ResponseWriter, r *http.Request) {
	var in dms.CreateRelationship
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.Link(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 201, out)
}
func (a *API) relationships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.DMS.Relationships(r.Context(), subject(r), r.PathValue("id"), q.Get("direction"), q.Get("type"), q.Get("$skiptoken"), queryInt(r, "$top"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) unlink(w http.ResponseWriter, r *http.Request) {
	if err := a.DMS.Unlink(r.Context(), subject(r), r.PathValue("id"), r.PathValue("linkID")); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) publish(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.Publish(r.Context(), subject(r), r.PathValue("id"), version)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, version+1)
	respond(w, r, 201, out)
}
func (a *API) fileVersions(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.FileVersions(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "$skiptoken"), queryInt(r, "$top"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) publications(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Publications(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "$skiptoken"), queryInt(r, "$top"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	if err := a.DMS.CanUpload(r.Context(), subject(r), r.PathValue("id"), version); err != nil {
		a.failure(w, r, err)
		return
	}
	filename := r.Header.Get("X-Filename")
	_, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || filename == "" {
		a.problem(w, r, 400, "invalid_upload", "Content-Type and X-Filename are required")
		return
	}
	if r.ContentLength > a.MaxUpload {
		a.failure(w, r, storage.ErrTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxUpload)
	object, detected, err := storage.PutDocument(r.Context(), a.Storage, transfer.Reader(r.Body, w), a.MaxUpload)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	out, err := a.DMS.AttachBlob(r.Context(), subject(r), r.PathValue("id"), version, dms.BlobInput{ObjectKey: object.Key, Filename: filename, ContentType: detected, Size: object.Size, SHA256: object.SHA256, Languages: strings.TrimSpace(r.Header.Get("X-OCR-Languages"))})
	if err != nil {
		if cleanupErr := a.Storage.Delete(context.Background(), object.Key); cleanupErr != nil {
			a.Logger.Error("orphan cleanup failed", "object_key", object.Key, "error", cleanupErr)
		}
		a.failure(w, r, err)
		return
	}
	etag(w, version+1)
	result := blobResponse(out.Blob, version+1)
	result["duplicates"] = out.Duplicates
	respond(w, r, 201, result)
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	b, err := a.DMS.GetBlob(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	file, err := a.Storage.Open(r.Context(), b.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", b.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": b.Filename}))
	w.Header().Set("ETag", strconv.Quote(b.Sha256))
	http.ServeContent(transfer.Writer(w), r, b.Filename, b.CreatedAt, file)
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ident auth.Identity
		var err error
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && strings.HasPrefix(parts[1], identity.RESTTokenPrefix) {
			token, e := a.DMS.Directory().AuthenticateRESTToken(r.Context(), parts[1])
			if e != nil || (a.Auth.Kind() == auth.TrustedProxy && !token.Integration) {
				err = auth.ErrUnauthenticated
			} else {
				ident = auth.Identity{Subject: token.Subject}
				r = r.WithContext(identity.WithRESTToken(r.Context(), token))
			}
		} else {
			ident, err = a.Auth.Authenticate(r)
		}
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			if a.Auth.Kind() == auth.TrustedProxy || a.Auth.Kind() == auth.LocalMode {
				w.Header().Set("WWW-Authenticate", `Basic realm="PaperGo", charset="UTF-8"`)
			}
			a.problem(w, r, 401, "unauthorized", "valid authentication is required")
			return
		}
		if _, token := identity.RESTTokenContext(r.Context()); !token && (a.Auth.Kind() == auth.TrustedProxy || a.Auth.Kind() == auth.LocalMode) {
			if err = a.DMS.Directory().EnsureUser(r.Context(), ident.Subject); err != nil {
				a.failure(w, r, err)
				return
			}
		} else if _, personal := identity.RESTTokenContext(r.Context()); !personal {
			if err = a.DMS.Directory().SyncUser(r.Context(), identity.UserIdentity{
				Subject:           ident.Subject,
				Name:              ident.Name,
				PreferredUsername: ident.PreferredUsername,
				Email:             ident.Email,
				Groups:            ident.Groups,
				SyncGroups:        ident.SyncGroups,
			}); err != nil {
				a.failure(w, r, err)
				return
			}
		}
		if err = identity.RequireActive(r.Context(), a.DMS.Directory().DB, ident.Subject); err != nil {
			a.failure(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey{}, ident.Subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (a *API) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		ctx := requestmeta.WithTrace(context.WithValue(r.Context(), requestKey{}, id), id)
		if a.PublicURL != nil {
			ctx = context.WithValue(ctx, publicURLKey{}, a.PublicURL)
		}
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		rw := &responseWriter{ResponseWriter: w}
		started := time.Now()
		defer func() {
			if recovered := recover(); recovered != nil {
				a.Logger.Error("handler panic", "request_id", id, "panic", recovered)
				if rw.status == 0 {
					a.problem(rw, r, 500, "internal_error", "request could not be completed")
				}
			}
			a.Logger.Info("http_request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(rw, r)
	})
}

// keywords lists the workspace's keyword vocabulary (keywords) for pickers and autocomplete.
func (a *API) keywords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := queryInt(r, "$top")
	out, err := a.DMS.WorkspaceKeywords(r.Context(), subject(r), r.PathValue("id"), dms.WorkspaceKeywordsQuery{CollectionID: q.Get("collection_id"), Prefix: q.Get("prefix"), After: q.Get("$skiptoken"), Limit: limit})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
