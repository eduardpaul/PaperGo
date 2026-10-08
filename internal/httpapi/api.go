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
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"strconv"
	"strings"
	"time"
)

type API struct {
	DMS       *dms.Service
	Auth      auth.Verifier
	Storage   storage.Store
	Logger    *slog.Logger
	Ready     func(context.Context) error
	MaxUpload int64
	// MaxInFlight and RequestTimeout bound API work; zero disables each.
	MaxInFlight    int
	RequestTimeout time.Duration
}
type subjectKey struct{}
type requestKey struct{}

func subject(r *http.Request) string { v, _ := r.Context().Value(subjectKey{}).(string); return v }
func (a *API) Handler() http.Handler {
	api := http.NewServeMux()
	a.registerFoundation(api)
	api.HandleFunc("GET /v1/workspaces", a.browse)
	api.HandleFunc("POST /v1/workspaces", a.workspace)
	api.HandleFunc("GET /v1/resources", a.browse)
	api.HandleFunc("GET /v1/resources/{id}", a.get)
	api.HandleFunc("PATCH /v1/resources/{id}", a.update)
	api.HandleFunc("GET /v1/resources/{id}/children", a.children)
	api.HandleFunc("POST /v1/resources/{id}/children", a.create)
	api.HandleFunc("GET /v1/resources/{id}/fields", a.fields)
	api.HandleFunc("POST /v1/resources/{id}/fields", a.createField)
	api.HandleFunc("PATCH /v1/resources/{id}/fields/{fieldID}", a.updateField)
	api.HandleFunc("GET /v1/resources/{id}/schemas", a.schemas)
	api.HandleFunc("GET /v1/resources/{id}/permissions", a.permissions)
	api.HandleFunc("PUT /v1/resources/{id}/permissions", a.setPermissions)
	api.HandleFunc("GET /v1/items/{id}/relationships", a.relationships)
	api.HandleFunc("POST /v1/items/{id}/relationships", a.link)
	api.HandleFunc("DELETE /v1/items/{id}/relationships/{linkID}", a.unlink)
	api.HandleFunc("POST /v1/items/{id}/publications", a.publish)
	api.HandleFunc("GET /v1/items/{id}/publications", a.publications)
	api.HandleFunc("POST /v1/items/{id}/unpublish", a.unpublish)
	api.HandleFunc("GET /v1/items/{id}/revisions", a.revisions)
	api.HandleFunc("GET /v1/items/{id}/schema", a.itemSchema)
	api.HandleFunc("PUT /v1/items/{id}/content", a.upload)
	api.HandleFunc("GET /v1/items/{id}/content", a.download)
	api.HandleFunc("GET /v1/workspaces/{id}/audit", a.audit)
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { a.problem(w, r, 404, "not_found", "route not found") })
	root := http.NewServeMux()
	root.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	root.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := a.Ready(ctx); err != nil {
			a.problem(w, r, 503, "unavailable", "service is not ready")
			return
		}
		respond(w, 200, map[string]string{"status": "ok"})
	})
	root.Handle("/", a.authenticate(a.limit(api)))
	return a.observe(root)
}

// admissionWait is how long a request may queue for a slot before it is shed.
const admissionWait = time.Second

// limit sheds load instead of queueing without bound: past MaxInFlight
// concurrent requests a caller waits at most admissionWait, then gets 503 with
// Retry-After. Admitted requests are cancelled after RequestTimeout. Blob
// transfers stream for long periods under their own deadlines, so they bypass both.
func (a *API) limit(next http.Handler) http.Handler {
	var slots chan struct{}
	if a.MaxInFlight > 0 {
		slots = make(chan struct{}, a.MaxInFlight)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
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
		if a.RequestTimeout > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), a.RequestTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func (a *API) problem(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	id, _ := r.Context().Value(requestKey{}).(string)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": message, "request_id": id})
}
func (a *API) failure(w http.ResponseWriter, r *http.Request, err error) {
	var validation *dms.ValidationError
	var max *http.MaxBytesError
	switch {
	case errors.As(err, &validation):
		a.problem(w, r, 422, "validation_failed", validation.Error())
	case errors.Is(err, dms.ErrNotFound):
		a.problem(w, r, 404, "not_found", "resource not found")
	case errors.Is(err, dms.ErrForbidden):
		a.problem(w, r, 403, "forbidden", "access denied")
	case errors.Is(err, dms.ErrConflict):
		a.problem(w, r, 409, "conflict", "version conflict or duplicate")
	case errors.Is(err, storage.ErrTooLarge) || errors.As(err, &max):
		a.problem(w, r, 413, "payload_too_large", "request body exceeds size limit")
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(r.Context().Err(), context.DeadlineExceeded):
		w.Header().Set("Retry-After", "1")
		a.problem(w, r, 503, "timeout", "request exceeded the server time limit")
	default:
		a.Logger.ErrorContext(r.Context(), "request failed", "error", err, "request_id", r.Context().Value(requestKey{}))
		a.problem(w, r, 500, "internal_error", "request could not be completed")
	}
}
func (a *API) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		a.problem(w, r, 415, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
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
	raw := r.Header.Get("If-Match")
	if raw == "" {
		a.problem(w, r, 428, "precondition_required", "If-Match with the current resource ETag is required")
		return 0, false
	}
	v, err := strconv.Unquote(raw)
	if err != nil {
		a.problem(w, r, 400, "invalid_precondition", "If-Match must be a quoted integer ETag")
		return 0, false
	}
	version, err := strconv.Atoi(v)
	if err != nil || version < 1 {
		a.problem(w, r, 400, "invalid_precondition", "If-Match must be a positive integer ETag")
		return 0, false
	}
	return version, true
}
func queryInt(r *http.Request, key string) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	return v
}
func (a *API) workspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.Create(r.Context(), subject(r), "", dms.CreateResource{Kind: "workspace", Name: in.Name, Tags: in.Tags})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/resources/"+out.ID)
	etag(w, out.Version)
	respond(w, 201, out)
}
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in dms.CreateResource
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.Create(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/resources/"+out.ID)
	etag(w, out.Version)
	respond(w, 201, out)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.GetSurface(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("surface"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, 200, out)
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
	etag(w, out.Version)
	respond(w, 200, out)
}
func (a *API) children(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.list(w, r, dms.Browse{ParentID: r.PathValue("id"), Search: q.Get("q"), Tag: q.Get("tag"), After: q.Get("after"), Limit: queryInt(r, "limit"), Surface: q.Get("surface"), FilterField: q.Get("filter_field"), FilterOp: q.Get("filter_op"), FilterValue: q.Get("filter_value")})
}
func (a *API) browse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := dms.Browse{WorkspaceID: q.Get("workspace_id"), ParentID: q.Get("parent_id"), Search: q.Get("q"), Tag: q.Get("tag"), After: q.Get("after"), Limit: queryInt(r, "limit"), Surface: q.Get("surface"), FilterField: q.Get("filter_field"), FilterOp: q.Get("filter_op"), FilterValue: q.Get("filter_value")}
	if r.URL.Path == "/v1/workspaces" {
		in = dms.Browse{After: q.Get("after"), Limit: queryInt(r, "limit")}
	}
	a.list(w, r, in)
}
func (a *API) list(w http.ResponseWriter, r *http.Request, in dms.Browse) {
	out, err := a.DMS.Browse(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (a *API) fields(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Fields(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
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
	respond(w, 201, out)
}
func (a *API) permissions(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Permissions(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
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
	respond(w, 200, out)
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
	respond(w, 201, out)
}
func (a *API) relationships(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.DMS.Relationships(r.Context(), subject(r), r.PathValue("id"), q.Get("direction"), q.Get("name"), q.Get("after"), queryInt(r, "limit"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (a *API) unlink(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	if err := a.DMS.UnlinkVersion(r.Context(), subject(r), r.PathValue("id"), r.PathValue("linkID"), version); err != nil {
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
	respond(w, 201, out)
}
func (a *API) publications(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Publications(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "after_version"), queryInt(r, "limit"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
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
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || filename == "" {
		a.problem(w, r, 400, "invalid_upload", "Content-Type and X-Filename are required")
		return
	}
	if r.ContentLength > a.MaxUpload {
		a.failure(w, r, storage.ErrTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxUpload)
	object, err := a.Storage.Put(r.Context(), progressReader{r.Body, http.NewResponseController(w)}, a.MaxUpload)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	out, err := a.DMS.AttachBlob(r.Context(), subject(r), r.PathValue("id"), version, dms.BlobInput{ObjectKey: object.Key, Filename: filename, ContentType: media, Size: object.Size, SHA256: object.SHA256})
	if err != nil {
		if cleanupErr := a.Storage.Delete(context.Background(), object.Key); cleanupErr != nil {
			a.Logger.Error("orphan cleanup failed", "object_key", object.Key, "error", cleanupErr)
		}
		a.failure(w, r, err)
		return
	}
	etag(w, version+1)
	respond(w, 201, map[string]any{"id": out.ID, "item_id": out.ItemID, "version": out.Version, "resource_version": version + 1, "filename": out.Filename, "content_type": out.ContentType, "size": out.Size, "sha256": out.Sha256})
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
	http.ServeContent(progressWriter{w, http.NewResponseController(w)}, r, b.Filename, b.CreatedAt, file)
}

// Content transfers can be far larger than the server-wide read/write timeouts
// allow, so they bound stalls instead: each chunk moved extends the deadlines.
const transferStall = 60 * time.Second

type progressReader struct {
	io.Reader
	rc *http.ResponseController
}

func (p progressReader) Read(b []byte) (int, error) {
	deadline := time.Now().Add(transferStall)
	// Unsupported writers (e.g. test recorders) simply keep the server defaults.
	_ = p.rc.SetReadDeadline(deadline)
	_ = p.rc.SetWriteDeadline(deadline)
	return p.Reader.Read(b)
}

type progressWriter struct {
	http.ResponseWriter
	rc *http.ResponseController
}

func (p progressWriter) Write(b []byte) (int, error) {
	_ = p.rc.SetWriteDeadline(time.Now().Add(transferStall))
	return p.ResponseWriter.Write(b)
}
func (p progressWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }
func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Audit(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 8192 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			a.problem(w, r, 401, "unauthorized", "valid bearer authentication is required")
			return
		}
		id, err := a.Auth.Verify(r.Context(), parts[1])
		if err != nil || id == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			a.problem(w, r, 401, "unauthorized", "valid bearer authentication is required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), subjectKey{}, id)))
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
		r = r.WithContext(context.WithValue(r.Context(), requestKey{}, id))
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
