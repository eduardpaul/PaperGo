package httpapi

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"log/slog"
	"net/http"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/webdav"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
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
	// WebDAV serves WebDAV-enabled libraries under webdav.Prefix with its own authentication.
	WebDAV *webdav.Handler
}
type subjectKey struct{}
type requestKey struct{}

func (a *API) Handler() http.Handler {
	api := http.NewServeMux()
	spec, err := json.Marshal(a.routes(api))
	if err != nil {
		panic(err)
	}
	root := http.NewServeMux()
	// Health checks and the contract are public and never shed.
	root.Handle("GET /health/", api)
	root.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/openapi+json")
		_, _ = w.Write(spec)
	})
	limit := a.limiter()
	root.Handle("/", a.authenticate(limit(api)))
	if a.WebDAV != nil {
		dav := limit(a.WebDAV)
		root.Handle(webdav.Prefix, dav)
		root.Handle(strings.TrimSuffix(webdav.Prefix, "/"), dav)
		// Windows and Office discover WebDAV support at the server root.
		root.Handle("OPTIONS /{$}", a.WebDAV)
	}
	return a.observe(root)
}

// OpenAPI returns the REST contract that Handler serves.
func (a *API) OpenAPI() *huma.OpenAPI {
	return a.routes(http.NewServeMux())
}

// routes registers every REST operation on mux and returns its description.
func (a *API) routes(mux *http.ServeMux) *huma.OpenAPI {
	api := newHumaAPI(mux)
	a.registerResources(api)
	a.registerItems(api)
	a.registerContent(api, mux)
	a.registerFoundation(api)
	a.registerSmartFolders(api)
	describeSchemas(api.OpenAPI())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.problem(w, r, 404, "not_found", "route not found")
	})
	return api.OpenAPI()
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
			if a.RequestTimeout > 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == "PROPFIND") {
				ctx, cancel := context.WithTimeout(r.Context(), a.RequestTimeout)
				defer cancel()
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// transferRequest reports requests that stream a body: blob downloads and
// uploads, and WebDAV file GET/PUT. COPY and every other method do database work.
func transferRequest(r *http.Request) bool {
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

// problem writes a problem from plain handlers and middleware.
func (a *API) problem(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeProblem(w, newProblem(r.Context(), status, code, message))
}
func (a *API) failure(w http.ResponseWriter, r *http.Request, err error) {
	writeProblem(w, a.fail(r.Context(), err))
}
func writeProblem(w http.ResponseWriter, p *Problem) {
	for name, values := range p.GetHeaders() {
		w.Header()[name] = values
	}
	w.Header().Set("Content-Type", p.ContentType(""))
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
func subject(ctx context.Context) string { v, _ := ctx.Value(subjectKey{}).(string); return v }
func etag(version int) string            { return strconv.Quote(strconv.Itoa(version)) }

func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := auth.Authenticate(r, a.Auth)
		if err != nil {
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
