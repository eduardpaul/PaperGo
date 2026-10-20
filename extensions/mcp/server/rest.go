package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"papergo/extensions/mcp"
)

// rest sends requests to the PaperGo API at one fixed origin.
type rest struct {
	base    *url.URL
	client  *http.Client
	maxBody int64
}

func newREST(base *url.URL, maxBody int64, maxConns int) *rest {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = maxConns
	transport.MaxIdleConnsPerHost = maxConns
	return &rest{base: base, maxBody: maxBody, client: &http.Client{
		Transport: transport,
		// A redirect could carry the credential to another path or host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// backend is the API as one caller, with their Authorization header.
type backend struct {
	rest          *rest
	authorization string
}

// Do sends r to the API. A response with an error status is an
// *mcp.APIError; a failure to reach the API, or a response that is not
// JSON, is mcp.ErrUnavailable.
func (b backend) Do(ctx context.Context, r mcp.Request) (*mcp.Response, error) {
	target, err := b.rest.target(r.Path, r.Query)
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
	req.Header.Set("Accept", "application/json")
	if b.authorization != "" {
		req.Header.Set("Authorization", b.authorization)
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.IfMatch > 0 {
		req.Header.Set("If-Match", strconv.Quote(strconv.Itoa(r.IfMatch)))
	}
	res, err := b.rest.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s %s", mcp.ErrUnavailable, r.Method, r.Path)
	}
	defer res.Body.Close()
	requestID := res.Header.Get("X-Request-ID")
	raw, err := io.ReadAll(io.LimitReader(res.Body, b.rest.maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: reading %s %s", mcp.ErrUnavailable, r.Method, r.Path)
	}
	if int64(len(raw)) > b.rest.maxBody {
		return nil, &mcp.Failure{Code: mcp.CodeTooLarge, Message: fmt.Sprintf("PaperGo's answer is larger than %d bytes; ask for fewer entries or values.", b.rest.maxBody), RequestID: requestID}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var problem struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)
		return nil, &mcp.APIError{Status: res.StatusCode, Code: problem.Code, Detail: problem.Detail, RequestID: requestID}
	}
	if len(bytes.TrimSpace(raw)) > 0 && !json.Valid(raw) {
		return nil, fmt.Errorf("%w: %s %s answered with invalid JSON", mcp.ErrUnavailable, r.Method, r.Path)
	}
	return &mcp.Response{Status: res.StatusCode, Body: raw, RequestID: requestID}, nil
}

// target is the URL of an API path: an absolute, already escaped /v1 path
// without dot segments, query or fragment, on the configured origin.
func (r *rest) target(path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/v1/") || strings.ContainsAny(path, "?#\\") || strings.Contains(path, "//") {
		return "", fmt.Errorf("refused API path %q", path)
	}
	for _, segment := range strings.Split(path, "/") {
		unescaped, err := url.PathUnescape(segment)
		if err != nil || unescaped == "." || unescaped == ".." || strings.Contains(unescaped, "/") {
			return "", fmt.Errorf("refused API path %q", path)
		}
	}
	u := *r.base
	u.RawPath = path
	u.Path, _ = url.PathUnescape(path)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// ready reports whether the API answers its readiness check.
func (r *rest) ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := *r.base
	u.Path = "/health/ready"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return errors.New("PaperGo is not ready")
	}
	return nil
}
