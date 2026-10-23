package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"papergo/extensions/mcp"
)

// Server serves MCP and its health checks.
type Server struct {
	cfg        Config
	registry   *mcp.Registry
	rest       *rest
	resolver   CredentialResolver
	slots      chan struct{}
	origins    map[string]bool
	protection *http.CrossOriginProtection
	tools      map[string]*sdk.Tool
	mcp        http.Handler
}

// New returns a server for the registry's tools.
func New(cfg Config, registry *mcp.Registry) (*Server, error) {
	cfg.defaults()
	if cfg.BackendURL == nil {
		return nil, errors.New("the PaperGo API's URL is required")
	}
	s := &Server{cfg: cfg, registry: registry, rest: newREST(cfg.BackendURL, cfg.MaxBackendBytes, cfg.MaxBackendConns),
		slots: make(chan struct{}, cfg.MaxInFlight), origins: map[string]bool{}, protection: http.NewCrossOriginProtection(), tools: map[string]*sdk.Tool{}}
	s.resolver = bearer{rest: s.rest}
	origins := cfg.AllowedOrigins
	if cfg.PublicURL != nil {
		origins = append(origins, origin(cfg.PublicURL))
	}
	for _, o := range origins {
		s.origins[o] = true
		if err := s.protection.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("allowed origin %s: %w", o, err)
		}
	}
	for _, t := range registry.Tools() {
		s.tools[t.Name] = definition(t)
	}
	// Each request gets a server of its own, holding the tools its caller
	// may use; only the tool definitions are shared.
	s.mcp = sdk.NewStreamableHTTPHandler(s.serverFor, &sdk.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          cfg.MaxRequestBytes,
		PropagateRequestCancellation: true,
		Logger:                       slog.New(slog.DiscardHandler),
	})
	return s, nil
}

// RequestTimeout is how long a request may take, and so how long a
// graceful shutdown waits for requests in flight.
func (s *Server) RequestTimeout() time.Duration { return s.cfg.RequestTimeout }

// Handler serves /v1/mcp, /health/live and /health/ready.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(Path, s.observe(s.admit(s.endpoint())))
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := s.rest.ready(r.Context()); err != nil {
			http.Error(w, "PaperGo is not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

type contextKey int

const (
	recordKey contextKey = iota
	callerKey
)

// record is what a request's log line says: metadata only.
type record struct {
	id     string
	tool   string
	failed string
	caller *Caller
}

// observe gives each request an ID, makes every response private and
// uncacheable, and logs the request's metadata, never its credential,
// arguments or results.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &record{id: newID()}
		w.Header().Set("X-Request-ID", rec.id)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), recordKey, rec)))
		attrs := []any{"request_id", rec.id, "method", r.Method, "status", sw.status, "duration_ms", time.Since(started).Milliseconds()}
		if rec.tool != "" {
			attrs = append(attrs, "tool", rec.tool)
		}
		if rec.failed != "" {
			attrs = append(attrs, "tool_failure", rec.failed)
		}
		if rec.caller != nil {
			if ids := rec.caller.trace.list(); len(ids) > 0 {
				attrs = append(attrs, "backend_request_ids", ids)
			}
		}
		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelWarn
		}
		s.cfg.Logger.Log(r.Context(), level, "mcp_request", attrs...)
	})
}

// admit bounds the requests in flight: a request waits briefly for a slot,
// then is refused as overloaded. Admitted requests run under the request
// deadline.
func (s *Server) admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait := time.NewTimer(s.cfg.AdmissionWait)
		defer wait.Stop()
		select {
		case s.slots <- struct{}{}:
		case <-wait.C:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "papergo-mcp is busy; try again shortly", http.StatusServiceUnavailable)
			return
		case <-r.Context().Done():
			return
		}
		defer func() { <-s.slots }()
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// endpoint checks the request's origin, resolves its caller and hands it
// to the MCP handler.
func (s *Server) endpoint() http.Handler {
	authenticated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A browser may send requests only from an origin the deployment
		// names; native clients send none.
		if o := r.Header.Get("Origin"); o != "" && !s.origins[strings.ToLower(o)] {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		caller, err := s.resolver.Resolve(r.Context(), r)
		switch {
		case errors.Is(err, errUnauthenticated):
			w.Header().Set("WWW-Authenticate", `Bearer realm="PaperGo"`)
			http.Error(w, "a bearer credential is required", http.StatusUnauthorized)
			return
		case errors.Is(err, errInvalidToken):
			w.Header().Set("WWW-Authenticate", `Bearer realm="PaperGo", error="invalid_token"`)
			http.Error(w, "the bearer credential is not valid: it may have expired or been revoked; create a new access token in PaperGo under Settings, Access tokens", http.StatusUnauthorized)
			return
		case errors.Is(err, errForbidden):
			http.Error(w, "PaperGo does not admit this caller", http.StatusForbidden)
			return
		case err != nil:
			w.Header().Set("Retry-After", "5")
			http.Error(w, "PaperGo is unavailable", http.StatusServiceUnavailable)
			return
		}
		if rec, ok := r.Context().Value(recordKey).(*record); ok {
			rec.caller = caller
		}
		s.mcp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey, caller)))
	})
	return s.protection.Handler(authenticated)
}

// serverFor is the MCP server of one request: the tools whose scopes its
// caller holds. A tool another caller sees, or that no compiled code has,
// is equally unknown here.
func (s *Server) serverFor(r *http.Request) *sdk.Server {
	caller, ok := r.Context().Value(callerKey).(*Caller)
	if !ok {
		return nil
	}
	rec, _ := r.Context().Value(recordKey).(*record)
	srv := sdk.NewServer(&sdk.Implementation{Name: "papergo", Title: "PaperGo", Version: s.cfg.Version}, &sdk.ServerOptions{
		// The tools capability holds even when the caller may use none.
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		// What a caller sees depends on who they are and changes with
		// their access: nobody may keep it.
		SetCacheable: func(_ context.Context, _ sdk.Request, c *sdk.Cacheable) { c.CacheScope, c.TTLMs = "private", 0 },
		Logger:       slog.New(slog.DiscardHandler),
	})
	var gated []*mcp.Registered
	for _, t := range s.registry.Tools() {
		if !holds(caller, t.Scopes) {
			continue
		}
		srv.AddTool(s.tools[t.Name], s.handler(t, caller, rec))
		if t.Available != nil {
			gated = append(gated, t)
		}
	}
	if len(gated) > 0 {
		srv.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				res, err := next(ctx, method, req)
				if list, ok := res.(*sdk.ListToolsResult); ok && err == nil && method == "tools/list" {
					visible := list.Tools[:0:0]
					for _, tool := range list.Tools {
						if t := s.registry.Tool(tool.Name); t == nil || t.Available == nil || available(ctx, t, caller) {
							visible = append(visible, tool)
						}
					}
					out := *list
					out.Tools = visible
					return &out, nil
				}
				return res, err
			}
		})
	}
	return srv
}

// handler runs a tool for the caller: it checks again that the tool serves
// them, validates the arguments and turns the tool's failures into tool
// results the client can act on.
func (s *Server) handler(t *mcp.Registered, caller *Caller, rec *record) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
		defer cancel()
		if rec != nil {
			rec.tool = t.Name
		}
		if !holds(caller, t.Scopes) || t.Available != nil && !available(ctx, t, caller) {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown tool %q", t.Name)}
		}
		args, err := t.Validate(req.Params.Arguments)
		if err != nil {
			return s.failed(rec, err), nil
		}
		out, err := t.Call(ctx, caller, args)
		if err != nil {
			return s.failed(rec, err), nil
		}
		if int64(len(out)) > s.cfg.MaxBackendBytes {
			return s.failed(rec, mcp.Fail(mcp.CodeTooLarge, fmt.Sprintf("the result is larger than %d bytes; ask for fewer entries or values", s.cfg.MaxBackendBytes))), nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(out)}}, StructuredContent: out}, nil
	}
}

// failed is the tool result of a failure: its code, message and request
// ID as JSON text, which never carries credentials or internal detail.
func (s *Server) failed(rec *record, err error) *sdk.CallToolResult {
	f := *mcp.FailureOf(err)
	if f.RequestID == "" && rec != nil {
		f.RequestID = rec.id
	}
	if rec != nil {
		rec.failed = f.Code
	}
	if f.Code == mcp.CodeInternal {
		s.cfg.Logger.Error("mcp_tool_failed", "request_id", f.RequestID, "error", err)
	}
	raw, _ := json.Marshal(f)
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}}
}

func holds(caller *Caller, scopes []string) bool {
	for _, scope := range scopes {
		if !caller.HasScope(scope) {
			return false
		}
	}
	return true
}

// available asks a tool whether it serves the caller; a tool that cannot
// tell is unavailable.
func available(ctx context.Context, t *mcp.Registered, caller *Caller) bool {
	ok, err := t.Available(ctx, caller)
	return err == nil && ok
}

// definition is a tool's protocol definition, shared by every request.
func definition(t *mcp.Registered) *sdk.Tool {
	destructive, openWorld := t.Annotations.Destructive, t.Annotations.OpenWorld
	out := &sdk.Tool{Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema,
		Annotations: &sdk.ToolAnnotations{Title: t.Title, ReadOnlyHint: t.Annotations.ReadOnly, DestructiveHint: &destructive, IdempotentHint: t.Annotations.Idempotent, OpenWorldHint: &openWorld}}
	if t.OutputSchema != nil {
		out.OutputSchema = t.OutputSchema
	}
	return out
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// statusWriter records the response status for the log and keeps every
// response private and uncacheable, whatever the MCP handler set.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
		w.Header().Set("Cache-Control", "private, no-store")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
