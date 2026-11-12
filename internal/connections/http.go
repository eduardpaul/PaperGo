package connections

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	store "papergo/internal/dal/connections"
	"papergo/internal/httpapi"
)

// HTTP bounds.
const (
	// MaxBody bounds a call's body; MaxAppBody one an app sends.
	MaxBody    = 1 << 20
	MaxAppBody = 16 << 10
	// maxAnswer bounds an answer; a longer one fails rather than being cut.
	maxAnswer = 4 << 20
	// slots are the calls one connection makes at once; a call waits up to
	// slotWait for one.
	slots    = 4
	slotWait = 2 * time.Second
	// cacheBytes bounds a connection's cached answers.
	cacheBytes = 4 << 20
	// callLog is how many calls the monitor shows.
	callLog = 50
)

// callerHeaders are the headers a caller may set; every other is PaperGo's.
var callerHeaders = []string{"Accept", "Content-Type", "If-Match", "If-None-Match", "Idempotency-Key"}

// answerHeaders are the headers of an answer callers see. Location could
// name the target, and cookies are never passed on.
var answerHeaders = []string{"Content-Type", "ETag", "Last-Modified", "Retry-After"}

// Call is a request through an HTTP channel. Body is JSON, sent as JSON
// unless Content-Type says otherwise, when a string body is sent as its
// text; BodyText sends text as it is, and BodyBase64 the bytes it encodes.
type Call struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	BodyText   *string           `json:"body_text,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// Envelope is the endpoint's answer: its status and headers, and its body
// parsed when it is JSON, as text when it is text, and in base64 otherwise.
type Envelope struct {
	// Operation is the operation that makes a workflow run's call, after
	// the run's step commits; its result is the envelope.
	Operation  string            `json:"operation_id,omitempty"`
	Status     int               `json:"status,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// CallRecord is one call as the monitor shows it.
type CallRecord struct {
	At      time.Time `json:"at"`
	Channel string    `json:"channel"`
	Method  string    `json:"method"`
	// Path is without its query.
	Path   string `json:"path"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Took   int64  `json:"took_ms"`
	Caller string `json:"caller"`
	Cached bool   `json:"cached,omitempty"`
}

// prepared is a call checked against its channel: what is sent.
type prepared struct {
	method      string
	path        Path
	headers     http.Header
	body        []byte
	contentType string
}

// prepare checks a call against its channel and the rules, for a caller
// who sends at most limit bytes.
func prepareCall(ch Channel, in Call, limit int) (prepared, error) {
	method := strings.ToUpper(in.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !ch.allows(method) {
		return prepared{}, pathInvalid(fmt.Sprintf("channel %s does not allow %s", ch.Key, method))
	}
	path, err := ParsePath(in.Path)
	if err != nil {
		return prepared{}, pathInvalid(err.Error())
	}
	if !pathMatches(ch.Path, path.Segments) {
		return prepared{}, pathInvalid(fmt.Sprintf("%s is outside channel %s (%s)", strings.SplitN(path.Raw, "?", 2)[0], ch.Key, ch.Path))
	}
	out := prepared{method: method, path: path, headers: http.Header{}}
	for name, value := range in.Headers {
		canonical := http.CanonicalHeaderKey(name)
		allowed := false
		for _, h := range callerHeaders {
			allowed = allowed || h == canonical
		}
		if !allowed {
			return prepared{}, pathInvalid("a call sets only Accept, Content-Type, If-Match, If-None-Match and Idempotency-Key; " + name + " is PaperGo's")
		}
		if len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
			return prepared{}, pathInvalid("header " + name + " is not valid")
		}
		out.headers.Set(canonical, value)
	}
	out.contentType = out.headers.Get("Content-Type")
	given := 0
	for _, set := range []bool{len(in.Body) > 0 && string(in.Body) != "null", in.BodyText != nil, in.BodyBase64 != ""} {
		if set {
			given++
		}
	}
	switch {
	case given > 1:
		return prepared{}, pathInvalid("a call has one of body, body_text and body_base64")
	case in.BodyText != nil:
		out.body = []byte(*in.BodyText)
		if out.contentType == "" {
			out.contentType = "text/plain; charset=utf-8"
		}
	case in.BodyBase64 != "":
		if out.body, err = base64.StdEncoding.DecodeString(in.BodyBase64); err != nil {
			return prepared{}, pathInvalid("body_base64 is not base64")
		}
		if out.contentType == "" {
			out.contentType = "application/octet-stream"
		}
	case len(in.Body) > 0 && string(in.Body) != "null":
		var text string
		if out.contentType != "" && !jsonMedia(out.contentType) && json.Unmarshal(in.Body, &text) == nil {
			out.body = []byte(text)
		} else {
			out.body = in.Body
			if out.contentType == "" {
				out.contentType = "application/json"
			}
		}
	}
	if len(out.body)+len(path.Raw) > limit {
		return prepared{}, pathInvalid(fmt.Sprintf("a call sends at most %d KiB, query included", limit>>10))
	}
	if out.body != nil && (method == http.MethodGet || method == http.MethodHead) {
		return prepared{}, pathInvalid(method + " sends no body")
	}
	return out, nil
}

// allows reports whether an HTTP channel allows method.
func (c Channel) allows(method string) bool {
	for _, m := range c.Methods {
		if m == method {
			return true
		}
	}
	return false
}

func jsonMedia(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && (media == "application/json" || strings.HasSuffix(media, "+json"))
}

// httpEndpoint calls an HTTP service: no standing connection, keep-alive
// pooling, at most slots calls at once.
type httpEndpoint struct {
	conn     store.Connection
	base     *url.URL
	v        sealed
	settings Settings
	client   *http.Client
	slots    chan struct{}

	mu    sync.Mutex
	state Status
	cache map[string]cached
	bytes int
	calls []CallRecord
}

type cached struct {
	answer  Envelope
	size    int
	expires time.Time
}

func (e *httpEndpoint) status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *httpEndpoint) close() { e.client.CloseIdleConnections() }

func (r *Runtime) httpClient(v sealed, u *url.URL, timeout time.Duration) (*http.Client, error) {
	secure := u.Scheme == "https"
	transport := &http.Transport{Proxy: nil, DialContext: r.policy(secure).DialContext, MaxConnsPerHost: slots, MaxIdleConnsPerHost: slots,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: timeout, ForceAttemptHTTP2: secure}
	if secure {
		config, err := tlsConfig(v.CA, u.Hostname())
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = config
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func (r *Runtime) startHTTP(c store.Connection, v sealed, u *url.URL) (endpoint, error) {
	settings := settingsOf(c)
	client, err := r.httpClient(v, u, settings.timeout())
	if err != nil {
		return nil, err
	}
	return &httpEndpoint{conn: c, base: u, v: v, settings: settings, client: client, slots: make(chan struct{}, slots), state: Status{State: StateUnknown}, cache: map[string]cached{}}, nil
}

// sign is the signature of a call: HMAC-SHA256 over
// "{time}.{subject}.{workspace}.{body}" with the connection's secret.
func sign(secret string, at int64, subject, workspace string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s.%s.", at, subject, workspace)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// request builds what the endpoint receives.
func (e *httpEndpoint) request(ctx context.Context, p prepared, subject, requestID string) (*http.Request, error) {
	target := strings.TrimSuffix(e.base.String(), "/") + p.path.Raw
	// The root of a base with a path is the base itself, with or without
	// a query.
	if path, query, hasQuery := strings.Cut(p.path.Raw, "?"); path == "/" && e.base.Path != "" && e.base.Path != "/" {
		target = e.base.String()
		if hasQuery {
			target += "?" + query
		}
	}
	var body io.Reader
	if p.body != nil {
		body = bytes.NewReader(p.body)
	}
	req, err := http.NewRequestWithContext(ctx, p.method, target, body)
	if err != nil {
		return nil, pathInvalid("the path does not make a URL")
	}
	for name, value := range e.v.Headers {
		req.Header.Set(name, value)
	}
	for name, values := range p.headers {
		req.Header[name] = values
	}
	if p.contentType != "" {
		req.Header.Set("Content-Type", p.contentType)
	}
	switch c := e.v.Credentials; c.Type {
	case CredentialBasic:
		req.SetBasicAuth(c.Username, c.Password)
	case CredentialBearer:
		req.Header.Set("Authorization", "Bearer "+c.Token)
	case CredentialHeader:
		req.Header.Set(c.Name, c.Value)
	}
	req.Header.Set("PaperGo-Request-Id", requestID)
	if e.settings.Sign && e.v.SigningSecret != "" {
		at := time.Now().Unix()
		req.Header.Set("PaperGo-Workspace", e.conn.WorkspaceID)
		req.Header.Set("PaperGo-Subject", subject)
		req.Header.Set("PaperGo-Signature", "t="+strconv.FormatInt(at, 10)+",v1="+sign(e.v.SigningSecret, at, subject, e.conn.WorkspaceID, p.body))
	}
	req.Header.Set("User-Agent", "PaperGo")
	return req, nil
}

// envelope reads an answer into what callers receive.
func envelope(resp *http.Response) (Envelope, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return Envelope{}, err
	}
	if len(raw) > maxAnswer {
		return Envelope{}, connectionFailed("the service answered more than 4 MiB")
	}
	out := Envelope{Status: resp.StatusCode, Headers: map[string]string{}}
	for _, name := range answerHeaders {
		if v := resp.Header.Get(name); v != "" {
			out.Headers[strings.ToLower(name)] = v
		}
	}
	if len(raw) == 0 {
		return out, nil
	}
	contentType := resp.Header.Get("Content-Type")
	media, _, _ := mime.ParseMediaType(contentType)
	switch {
	case jsonMedia(contentType) && json.Valid(raw):
		out.Body = raw
	case (strings.HasPrefix(media, "text/") || media == "" || media == "application/xml") && utf8.Valid(raw):
		out.Body = marshal(string(raw))
	default:
		out.BodyBase64 = base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}

// record keeps a call for the monitor and the connection's status.
func (e *httpEndpoint) record(rec CallRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !rec.Cached {
		at := rec.At
		e.state.CheckedAt = &at
		switch {
		case rec.Error != "":
			e.state.State, e.state.LastError = StateFailing, rec.Error
		case rec.Status >= 500:
			e.state.State, e.state.LastError = StateFailing, fmt.Sprintf("the service answered %d", rec.Status)
		default:
			e.state.State, e.state.LastError = StateOK, ""
		}
	}
	e.calls = append(e.calls, rec)
	if len(e.calls) > callLog {
		e.calls = e.calls[len(e.calls)-callLog:]
	}
}

// Calls are the connection's recent calls, newest first.
func (e *httpEndpoint) recent() []CallRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]CallRecord, len(e.calls))
	for i, rec := range e.calls {
		out[len(out)-1-i] = rec
	}
	return out
}

func (e *httpEndpoint) cached(key string, now time.Time) (Envelope, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.cache[key]
	if !ok || now.After(c.expires) {
		return Envelope{}, false
	}
	return c.answer, true
}

func (e *httpEndpoint) keep(key string, answer Envelope, ttl time.Duration, now time.Time) {
	size := len(answer.Body) + len(answer.BodyBase64) + len(key)
	if size > cacheBytes/4 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.cache[key]; ok {
		e.bytes -= old.size
		delete(e.cache, key)
	}
	for k, c := range e.cache {
		if e.bytes+size <= cacheBytes {
			break
		}
		e.bytes -= c.size
		delete(e.cache, k)
	}
	e.cache[key] = cached{answer: answer, size: size, expires: now.Add(ttl)}
	e.bytes += size
}

// cacheKey is what a channel's cached GET answer is kept under.
func cacheKey(ch Channel, p prepared) string {
	return ch.Key + "\x00" + p.path.Raw + "\x00" + p.headers.Get("Accept")
}

// do makes a call through channel ch as subject. A GET a channel caches is
// answered from the cache while it is fresh.
func (e *httpEndpoint) do(ctx context.Context, ch Channel, p prepared, subject, requestID string) (Envelope, error) {
	now := time.Now()
	rec := CallRecord{At: now.UTC(), Channel: ch.Key, Method: p.method, Path: strings.SplitN(p.path.Raw, "?", 2)[0], Caller: subject}
	key := cacheKey(ch, p)
	ttl := ch.cacheFor()
	if ttl > 0 && p.method == http.MethodGet {
		if answer, ok := e.cached(key, now); ok {
			rec.Status, rec.Cached = answer.Status, true
			e.record(rec)
			return answer, nil
		}
	}
	wait := time.NewTimer(slotWait)
	defer wait.Stop()
	select {
	case e.slots <- struct{}{}:
	case <-wait.C:
		return Envelope{}, &busy{code: "connection_busy", detail: fmt.Sprintf("connection %s is making %d calls already", e.conn.Key, slots), after: time.Second}
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	}
	defer func() { <-e.slots }()
	cctx, cancel := context.WithTimeout(ctx, e.settings.timeout())
	defer cancel()
	req, err := e.request(cctx, p, subject, requestID)
	if err != nil {
		return Envelope{}, err
	}
	resp, err := e.client.Do(req)
	rec.Took = time.Since(now).Milliseconds()
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		// The caller went away: that says nothing of the service.
		return Envelope{}, ctx.Err()
	}
	// timedOut reports whether a failure is the call's own timeout.
	timedOut := func(err error) bool {
		return errors.Is(err, context.DeadlineExceeded) || cctx.Err() != nil && ctx.Err() == nil
	}
	if err != nil {
		var out error
		if timedOut(err) {
			out = connectionTimeout(fmt.Sprintf("the service did not answer within %s", e.settings.timeout()))
			rec.Error = out.Error()
		} else {
			// Why names the target, its address or its certificate, which
			// only the connection's managers see: in its status and monitor.
			out = connectionFailed("the service could not be reached; the connection's managers see why in its monitor")
			rec.Error = "the service could not be reached: " + explain(unwrapURL(err))
		}
		e.record(rec)
		return Envelope{}, out
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
		out := connectionFailed(fmt.Sprintf("the service redirected (%d); PaperGo never follows redirects, so give the address it redirects to", resp.StatusCode))
		rec.Status, rec.Error = resp.StatusCode, out.Error()
		e.record(rec)
		return Envelope{}, out
	}
	answer, err := envelope(resp)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return Envelope{}, ctx.Err()
		}
		// An answer that stopped after its headers is the service's failure,
		// as one that never came is: a problem, not an error of PaperGo's.
		rec.Status, rec.Error = resp.StatusCode, err.Error()
		var problem *httpapi.Problem
		switch {
		case errors.As(err, &problem):
		case timedOut(err):
			err = connectionTimeout(fmt.Sprintf("the service did not answer within %s", e.settings.timeout()))
			rec.Error = err.Error()
		default:
			err = connectionFailed("the service's answer could not be read")
		}
		e.record(rec)
		return Envelope{}, err
	}
	rec.Status = answer.Status
	e.record(rec)
	if ttl > 0 && p.method == http.MethodGet && answer.Status >= 200 && answer.Status < 300 {
		e.keep(key, answer, ttl, now)
	}
	return answer, nil
}

// unwrapURL drops the url.Error around a transport failure, which would
// name the target.
func unwrapURL(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

// testHTTP calls the health path, or the base URL, once.
func (r *Runtime) testHTTP(ctx context.Context, c store.Connection, v sealed, u *url.URL) error {
	settings := settingsOf(c)
	client, err := r.httpClient(v, u, settings.timeout())
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	e := &httpEndpoint{conn: c, base: u, v: v, settings: settings, client: client}
	path := "/"
	if settings.HealthPath != "" {
		path = settings.HealthPath
	}
	parsed, err := ParsePath(path)
	if err != nil {
		return err
	}
	req, err := e.request(ctx, prepared{method: http.MethodGet, path: parsed, headers: http.Header{}}, "papergo-test", newSecret()[:16])
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return unwrapURL(err)
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the service refused PaperGo's credentials (%d)", resp.StatusCode)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("the service redirected (%d); PaperGo never follows redirects, so give the address it redirects to", resp.StatusCode)
	case resp.StatusCode >= 500:
		return fmt.Errorf("the service answered %d", resp.StatusCode)
	}
	return nil
}
