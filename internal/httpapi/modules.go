package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"papergo/internal/dms"
	"strings"
)

// Module is a compiled feature that contributes authenticated routes under
// /v1. Its routes run behind the same authentication, admission limits and
// error mapping as the core API, and each must be described in
// api/openapi.json under its OperationID.
type Module interface {
	Name() string
	Routes() []Route
}

// Route is one module route. Pattern is a method and a /v1 path in
// http.ServeMux syntax. Handle answers the request or returns an error the
// API maps to a problem response.
type Route struct {
	Method, Pattern string
	OperationID     string
	Handle          func(http.ResponseWriter, *http.Request, Request) error
}

// Request is what a module route acts with: the authenticated subject and
// the service, bound to the caller's transaction when the request runs
// inside one.
type Request struct {
	Subject string
	DMS     *dms.Service
}

// router records the method-qualified patterns it serves, so the API can
// list its routes and check them against its contract.
type router struct {
	mux      *http.ServeMux
	patterns []string
}

func newRouter() *router { return &router{mux: http.NewServeMux()} }

func (m *router) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(handler))
}

func (m *router) Handle(pattern string, handler http.Handler) {
	m.mux.Handle(pattern, handler)
	m.patterns = append(m.patterns, pattern)
}

func (m *router) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// registerModules adds the modules' routes. A pattern that another route
// already serves, an invalid one or a reused operation ID is an error.
func (a *API) registerModules(m *router) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("module route conflicts: %v", recovered)
		}
	}()
	operations := map[string]string{}
	for _, module := range a.Modules {
		for _, route := range module.Routes() {
			if !strings.HasPrefix(route.Pattern, "/v1/") || route.Method == "" || route.OperationID == "" || route.Handle == nil {
				return fmt.Errorf("module %s: routes need a method, a /v1 pattern, an operation ID and a handler", module.Name())
			}
			if previous, taken := operations[route.OperationID]; taken {
				return fmt.Errorf("module %s: operation %s is also %s", module.Name(), route.OperationID, previous)
			}
			operations[route.OperationID] = module.Name()
			m.HandleFunc(route.Method+" "+route.Pattern, a.moduleHandler(route))
		}
	}
	return nil
}

func (a *API) moduleHandler(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := route.Handle(w, r, Request{Subject: subject(r), DMS: a.DMS}); err != nil {
			a.failure(w, r, err)
		}
	}
}

// Validate checks that the API's routes compose: module routes must not
// collide with each other or with the core API.
func (a *API) Validate() error {
	_, err := a.root()
	return err
}

// routes builds the /v1 routes, the core API's and the modules'.
func (a *API) routes() (*router, error) {
	api := newRouter()
	api.HandleFunc("GET /v1/events", a.events)
	a.registerPortability(api)
	a.registerIdentity(api)
	a.registerFoundation(api)
	a.registerIntegrations(api)
	a.registerSmartFolders(api)
	a.registerWorkflows(api)
	a.registerSearch(api)
	a.registerStorage(api)
	a.registerTasks(api)
	a.registerResources(api)
	if err := a.registerModules(api); err != nil {
		return nil, err
	}
	// Subrequests already share the outer admission slot; authenticating them
	// separately preserves authorization overrides without recursively queueing.
	api.HandleFunc("POST /v1/$batch", a.batch(a.observe(a.authenticate(api))))
	api.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { a.problem(w, r, 404, "not_found", "route not found") })
	return api, nil
}

// Accepted answers a request that accepted an operation: 202 with the
// operation and its location, or 200 when an idempotency key replayed one
// accepted earlier.
func Accepted(w http.ResponseWriter, r *http.Request, op *dms.OperationView) {
	w.Header().Set("Location", "/v1/operations/"+op.ID)
	if op.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		respond(w, r, http.StatusOK, op)
		return
	}
	respond(w, r, http.StatusAccepted, op)
}

// idempotent binds the request's Idempotency-Key to the operation the
// handler accepts.
func (a *API) idempotent(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			handler(w, r)
			return
		}
		ctx, err := dms.WithIdempotencyKey(r.Context(), key)
		var validation *dms.ValidationError
		if errors.As(err, &validation) {
			a.problem(w, r, 400, "invalid_idempotency_key", validation.Error())
			return
		}
		handler(w, r.WithContext(ctx))
	}
}
