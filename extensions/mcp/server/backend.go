package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"papergo/extensions/mcp"
)

// backend is the API as one caller, served in-process. Its requests share
// the ID of the MCP request that makes them.
type backend struct {
	api       http.Handler
	host      string
	requestID string
	maxBody   int64
}

// Do sends r to the API. A response with an error status is an
// *mcp.APIError; a response that is not JSON is mcp.ErrUnavailable.
func (b backend) Do(ctx context.Context, r mcp.Request) (*mcp.Response, error) {
	target, err := target(r.Path, r.Query)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		return nil, err
	}
	// Links the API answers with name the host the client reached.
	req.Host = b.host
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.IfMatch > 0 {
		req.Header.Set("If-Match", strconv.Quote(strconv.Itoa(r.IfMatch)))
	}
	res := &recorder{header: http.Header{}, max: b.maxBody}
	b.api.ServeHTTP(res, req)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	status := res.status
	if status == 0 {
		status = http.StatusOK
	}
	raw := res.body.Bytes()
	if res.over {
		return nil, &mcp.Failure{Code: mcp.CodeTooLarge, Message: fmt.Sprintf("PaperGo's answer is larger than %d bytes; ask for fewer entries or values.", b.maxBody), RequestID: b.requestID}
	}
	if status < 200 || status > 299 {
		var problem struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)
		return nil, &mcp.APIError{Status: status, Code: problem.Code, Detail: problem.Detail, RequestID: b.requestID}
	}
	if len(bytes.TrimSpace(raw)) > 0 && !json.Valid(raw) {
		return nil, fmt.Errorf("%w: %s %s answered with invalid JSON", mcp.ErrUnavailable, r.Method, r.Path)
	}
	return &mcp.Response{Status: status, Body: raw, RequestID: b.requestID}, nil
}

// target is the URL of an API path: an absolute, already escaped /v1 path
// without dot segments, query or fragment.
func target(path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/v1/") || strings.ContainsAny(path, "?#\\") || strings.Contains(path, "//") {
		return "", fmt.Errorf("refused API path %q", path)
	}
	for _, segment := range strings.Split(path, "/") {
		unescaped, err := url.PathUnescape(segment)
		if err != nil || unescaped == "." || unescaped == ".." || strings.Contains(unescaped, "/") {
			return "", fmt.Errorf("refused API path %q", path)
		}
	}
	u := url.URL{RawPath: path, RawQuery: query.Encode()}
	u.Path, _ = url.PathUnescape(path)
	return u.String(), nil
}

// recorder keeps an in-process answer up to max bytes.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	max    int64
	over   bool
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if room := r.max - int64(r.body.Len()); int64(len(p)) > room {
		r.over = true
		r.body.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return r.body.Write(p)
}
