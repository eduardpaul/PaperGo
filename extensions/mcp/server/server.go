package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"papergo/extensions/mcp"
	"papergo/internal/httpapi"
	"papergo/internal/requestmeta"
)

// Server serves MCP as a module of the PaperGo API.
type Server struct {
	cfg      Config
	registry *mcp.Registry
	tools    map[string]*sdk.Tool
	mcp      http.Handler
}

// New returns a server for the registry's tools.
func New(cfg Config, registry *mcp.Registry) *Server {
	cfg.defaults()
	s := &Server{cfg: cfg, registry: registry, tools: map[string]*sdk.Tool{}}
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
		// Every request carries a bearer credential, which a page that
		// rebinds a name to a loopback address does not have; and a proxy
		// on loopback reaches PaperGo with its public host.
		DisableLocalhostProtection: true,
		Logger:                     slog.New(slog.DiscardHandler),
	})
	return s
}

// Module is the API module that serves the endpoint.
func (s *Server) Module() httpapi.Module { return module{s} }

type module struct{ s *Server }

func (module) Name() string { return "mcp" }

// Routes serve MCP on POST. Without sessions there is no stream to open or
// session to end, so GET and DELETE answer 405, as the transport requires.
func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodPost, Pattern: Path, OperationID: "mcp", Handle: m.s.serve},
		{Method: http.MethodGet, Pattern: Path, OperationID: "mcpStream", Handle: notAllowed},
		{Method: http.MethodDelete, Pattern: Path, OperationID: "mcpEndSession", Handle: notAllowed},
	}
}

func notAllowed(w http.ResponseWriter, _ *http.Request, _ httpapi.Request) error {
	w.Header().Set("Allow", http.MethodPost)
	return &httpapi.Problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Detail: "the MCP endpoint is stateless: POST each message"}
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
}

// serve answers an MCP message for the API caller. Assistants connect with
// a credential of their own, never the browser's session or a proxy's
// sign-in, so the connection's token decides what they reach.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	if !in.Bearer() {
		w.Header().Set("WWW-Authenticate", `Bearer realm="PaperGo"`)
		return &httpapi.Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Detail: "AI assistants connect with an access token: create one in PaperGo under Settings, AI assistants"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	api := in.Loopback()
	if s.cfg.WrapAPI != nil {
		api = s.cfg.WrapAPI(api)
	}
	rec := &record{id: requestmeta.Trace(r.Context())}
	caller, err := resolve(ctx, backend{api: api, host: r.Host, requestID: rec.id, maxBody: s.cfg.MaxResultBytes})
	if err != nil {
		return err
	}
	s.mcp.ServeHTTP(private{w}, r.WithContext(context.WithValue(context.WithValue(ctx, callerKey, caller), recordKey, rec)))
	if rec.tool != "" {
		attrs := []any{"request_id", rec.id, "tool", rec.tool}
		if rec.failed != "" {
			attrs = append(attrs, "tool_failure", rec.failed)
		}
		s.cfg.Logger.Info("mcp_tool", attrs...)
	}
	return nil
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
		if int64(len(out)) > s.cfg.MaxResultBytes {
			return s.failed(rec, mcp.Fail(mcp.CodeTooLarge, fmt.Sprintf("the result is larger than %d bytes; ask for fewer entries or values", s.cfg.MaxResultBytes))), nil
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

// private keeps every response private and uncacheable, whatever the MCP
// handler set: what a caller sees depends on who they are.
type private struct{ http.ResponseWriter }

func (w private) WriteHeader(code int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.ResponseWriter.WriteHeader(code)
}

func (w private) Write(b []byte) (int, error) {
	w.Header().Set("Cache-Control", "private, no-store")
	return w.ResponseWriter.Write(b)
}

func (w private) Unwrap() http.ResponseWriter { return w.ResponseWriter }
