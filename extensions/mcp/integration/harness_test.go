package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/builtin"
	"papergo/extensions/mcp/server"
	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/documentsearch"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"papergo/internal/testutil/searchstore"
)

// aliceToken is the development credential: alice with every scope she
// holds.
const aliceToken = "0123456789abcdef0123456789abcdef"

// env is a PaperGo API on a fixture database and papergo-mcp in front of it.
type env struct {
	svc    *dms.Service
	search *documentsearch.Search
	api    *httptest.Server
	mcp    *httptest.Server
	// gate, when set, holds workspace listings until it is closed.
	gate   chan struct{}
	gateMu sync.Mutex
	// before, when set, runs before the API serves a request.
	before  func(*http.Request)
	logs    *bytes.Buffer
	logsMu  *sync.Mutex
	backend http.Handler
}

type options struct {
	search        bool
	configure     func(*server.Config)
	contributions []mcp.Contribution
	enabled       []string
}

func setup(t *testing.T, o options) *env {
	t.Helper()
	db := daltest.Database(t)
	files, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: dms.NewService(db.SQL), logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}}
	a := &httpapi.API{DMS: e.svc, Auth: auth.Development{Token: aliceToken, Subject: "alice"}, Storage: files,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Ready: db.SQL.PingContext, MaxUpload: 1 << 20}
	if o.search {
		e.search = &documentsearch.Search{Storage: files, Store: searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})}
		if err := e.search.Register(e.svc); err != nil {
			t.Fatal(err)
		}
		a.Modules = []httpapi.Module{e.search.Module()}
	}
	e.backend = a.Handler()
	e.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.gateMu.Lock()
		gate, before := e.gate, e.before
		e.gateMu.Unlock()
		if before != nil {
			before(r)
		}
		if gate != nil && r.URL.Path == "/v1/workspaces" {
			<-gate
		}
		e.backend.ServeHTTP(w, r)
	}))
	t.Cleanup(e.api.Close)
	backendURL, _ := url.Parse(e.api.URL)
	cfg := server.Config{BackendURL: backendURL, Version: "test", Logger: slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))}
	if o.configure != nil {
		o.configure(&cfg)
	}
	registry, err := mcp.NewRegistry(builtin.Tools(), o.contributions, o.enabled)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(cfg, registry)
	if err != nil {
		t.Fatal(err)
	}
	e.mcp = httptest.NewServer(srv.Handler())
	t.Cleanup(e.mcp.Close)
	return e
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (e *env) log() string {
	e.logsMu.Lock()
	defer e.logsMu.Unlock()
	return e.logs.String()
}

func (e *env) endpoint() string { return e.mcp.URL + server.Path }

// token is a personal access token of subject with scopes.
func (e *env) token(t *testing.T, subject string, scopes ...string) identity.CreatedAccessToken {
	t.Helper()
	tok, err := e.svc.Directory().CreateAccessToken(t.Context(), subject, identity.AccessTokenInput{Name: "MCP " + strings.Join(scopes, " "), Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// headers adds fixed headers to every request.
type headers struct {
	set  http.Header
	next http.RoundTripper
}

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.set {
		r.Header[k] = v
	}
	return h.next.RoundTrip(r)
}

// connect opens an MCP client session with the bearer credential.
func (e *env) connect(t *testing.T, token string) *sdk.ClientSession {
	t.Helper()
	session, err := e.dial(t, token)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (e *env) dial(t *testing.T, token string) (*sdk.ClientSession, error) {
	t.Helper()
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	transport := &sdk.StreamableClientTransport{Endpoint: e.endpoint(), DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: headers{h, http.DefaultTransport}}}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "papergo-test", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err == nil {
		t.Cleanup(func() { _ = session.Close() })
	}
	return session, err
}

// call calls a tool and returns its structured result, or its failure.
func call(t *testing.T, s *sdk.ClientSession, tool string, args string) (map[string]any, *mcp.Failure) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatalf("%s %s: %v", tool, args, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if res.IsError {
		var f mcp.Failure
		if err := json.Unmarshal([]byte(text), &f); err != nil || f.Code == "" {
			t.Fatalf("%s: failure %q", tool, text)
		}
		if res.StructuredContent != nil {
			t.Fatalf("%s: failure with structured content", tool)
		}
		return nil, &f
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(structured, []byte(text)) {
		t.Fatalf("%s: text %s and structured content %s differ", tool, text, structured)
	}
	out := map[string]any{}
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	out["$text"] = text
	return out, nil
}

// ok calls a tool that must succeed.
func ok(t *testing.T, s *sdk.ClientSession, tool string, args string) map[string]any {
	t.Helper()
	out, f := call(t, s, tool, args)
	if f != nil {
		t.Fatalf("%s %s: %+v", tool, args, f)
	}
	return out
}

// fails calls a tool that must fail with code.
func fails(t *testing.T, s *sdk.ClientSession, tool string, args string, code string) *mcp.Failure {
	t.Helper()
	out, f := call(t, s, tool, args)
	if f == nil || f.Code != code {
		t.Fatalf("%s %s: %v %+v, want %s", tool, args, out, f, code)
	}
	return f
}

// unknown calls a tool that must not exist for the caller.
func unknown(t *testing.T, s *sdk.ClientSession, tool string) {
	t.Helper()
	_, err := s.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("%s: %v", tool, err)
	}
}

// sameJSON compares JSON as the SDK client decodes it, numbers as float64.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func toolNames(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// post sends a raw request to the MCP endpoint.
func post(t *testing.T, e *env, body string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.endpoint(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func create(t *testing.T, s *dms.Service, subject, parent string, in dms.CreateResource) string {
	t.Helper()
	r, err := s.Create(context.Background(), subject, parent, in)
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
