package server

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

// env is a PaperGo API serving MCP on a fixture database.
type env struct {
	svc    *dms.Service
	files  storage.Store
	search *documentsearch.Search
	api    *httptest.Server
	// mu guards gate, before and wrap, which tests change while the API
	// serves requests.
	mu sync.Mutex
	// gate, when set, holds the tools' workspace listings until it is
	// closed.
	gate chan struct{}
	// before, when set, runs before the API serves a tool's request.
	before func(*http.Request)
	// wrap, when set, answers the tools' requests in place of the API,
	// which it is given.
	wrap   func(api http.Handler) http.Handler
	logs   *bytes.Buffer
	logsMu *sync.Mutex
}

type options struct {
	search        bool
	configure     func(*Config)
	contributions []mcp.Contribution
	enabled       []string
	// app is the web app's origin results link to.
	app *url.URL
	// maxInFlight bounds the API's concurrent requests.
	maxInFlight int
}

func setup(t *testing.T, o options) *env {
	t.Helper()
	db := daltest.Database(t)
	files, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: dms.NewService(db.SQL), files: files, logs: &bytes.Buffer{}, logsMu: &sync.Mutex{}}
	log := slog.New(slog.NewJSONHandler(lockedWriter{e.logs, e.logsMu}, nil))
	cfg := Config{Version: "test", Logger: log, AppURL: o.app, wrapAPI: func(api http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e.mu.Lock()
			gate, before, wrap := e.gate, e.before, e.wrap
			e.mu.Unlock()
			if before != nil {
				before(r)
			}
			if gate != nil && r.URL.Path == "/v1/workspaces" {
				<-gate
			}
			if wrap != nil {
				wrap(api).ServeHTTP(w, r)
				return
			}
			api.ServeHTTP(w, r)
		})
	}}
	if o.configure != nil {
		o.configure(&cfg)
	}
	registry, err := mcp.NewRegistry(builtin.Tools(), o.contributions, o.enabled)
	if err != nil {
		t.Fatal(err)
	}
	a := &httpapi.API{DMS: e.svc, Auth: auth.Development{Token: aliceToken, Subject: "alice"}, Storage: files, Logger: log,
		Ready: db.SQL.PingContext, MaxUpload: 1 << 20, MaxInFlight: o.maxInFlight, MCP: true,
		Modules: []httpapi.Module{New(cfg, registry).Module()}}
	if o.search {
		e.search = &documentsearch.Search{Storage: files, Store: searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})}
		if err := e.search.Register(e.svc); err != nil {
			t.Fatal(err)
		}
		a.Modules = append(a.Modules, e.search.Module())
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	e.api = httptest.NewServer(a.Handler())
	t.Cleanup(e.api.Close)
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

// intercept makes wrap answer the tools' requests from now on; nil
// restores the API.
func (e *env) intercept(wrap func(api http.Handler) http.Handler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.wrap = wrap
}

func (e *env) endpoint() string { return e.api.URL + Path }

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

// rest calls the PaperGo API as alice and returns its JSON answer; ifMatch,
// when set, is the version a write requires.
func rest(t *testing.T, e *env, method, path, body, ifMatch string) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, e.api.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/json")
	if ifMatch != "" {
		req.Header.Set("If-Match", `"`+ifMatch+`"`)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	if res.StatusCode/100 != 2 || len(raw) > 0 && json.Unmarshal(raw, &out) != nil {
		t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return out
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
