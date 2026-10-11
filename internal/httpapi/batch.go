package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxBatchResponse = 16 << 20

type batchRequest struct {
	ID        string            `json:"id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
	DependsOn []string          `json:"dependsOn,omitempty"`
}

type batchResponse struct {
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body,omitempty"`
}

func batchTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.HasPrefix(raw, "//") || strings.Contains(u.Path, "\\") {
		return nil, errors.New("batch URLs must be relative API paths")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." || strings.EqualFold(segment, "$batch") {
			return nil, errors.New("nested batches and path traversal are forbidden")
		}
	}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	if !strings.HasPrefix(u.Path, "/v1/") {
		u.Path = "/v1" + u.Path
	}
	u.RawPath = ""
	return u, nil
}

func (a *API) batch(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Requests []batchRequest `json:"requests"`
		}
		if !a.decode(w, r, &in) {
			return
		}
		invalid := func(detail string) { a.problem(w, r, 400, "invalid_batch", detail) }
		if len(in.Requests) < 1 || len(in.Requests) > 20 {
			invalid("a batch requires 1 to 20 requests")
			return
		}
		seen := map[string]bool{}
		targets := make([]*url.URL, len(in.Requests))
		for i, call := range in.Requests {
			if strings.TrimSpace(call.ID) == "" || seen[call.ID] {
				invalid("request IDs must be nonempty and unique")
				return
			}
			switch strings.ToUpper(call.Method) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				invalid("unsupported batch method")
				return
			}
			for _, dependency := range call.DependsOn {
				if !seen[dependency] {
					invalid("dependencies must name earlier requests")
					return
				}
			}
			var err error
			targets[i], err = batchTarget(call.URL)
			if err != nil {
				invalid(err.Error())
				return
			}
			headers := map[string]bool{}
			for key, value := range call.Headers {
				canonical := http.CanonicalHeaderKey(key)
				if !validBatchHeader(key, value) || headers[canonical] {
					invalid("invalid or duplicate request header")
					return
				}
				switch canonical {
				case "Host", "Content-Length", "Connection", "Transfer-Encoding", "Trailer", "Upgrade", "Proxy-Authorization", "Proxy-Connection":
					invalid("transport headers cannot be overridden")
					return
				}
				headers[canonical] = true
			}
			seen[call.ID] = true
		}
		out := make([]batchResponse, 0, len(in.Requests))
		failed := map[string]bool{}
		for i, call := range in.Requests {
			if r.Context().Err() != nil {
				a.failure(w, r, r.Context().Err())
				return
			}
			result := batchResponse{ID: call.ID, Headers: map[string]string{}}
			blocked := false
			for _, dependency := range call.DependsOn {
				blocked = blocked || failed[dependency]
			}
			if blocked {
				result.Status = 424
				result.Headers["Content-Type"] = "application/problem+json"
				result.Body = map[string]any{"type": "about:blank", "title": http.StatusText(http.StatusFailedDependency), "status": http.StatusFailedDependency, "code": "failed_dependency", "detail": "a prerequisite request failed"}
			} else {
				result = a.batchCall(r, call, targets[i], next)
			}
			failed[call.ID] = result.Status >= 400
			out = append(out, result)
		}
		respond(w, r, 200, struct {
			Responses []batchResponse `json:"responses"`
		}{out})
	}
}

func validBatchHeader(key, value string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	for _, c := range value {
		if c == 127 || (c < 32 && c != '\t') {
			return false
		}
	}
	return true
}

func (a *API) batchCall(outer *http.Request, call batchRequest, target *url.URL, next http.Handler) batchResponse {
	body := []byte(call.Body)
	headers := make(http.Header)
	for _, key := range []string{"Authorization", "Cookie", "Accept-Language"} {
		headers[key] = outer.Header.Values(key)
	}
	for key, value := range call.Headers {
		headers.Set(key, value)
	}
	if len(body) > 0 {
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json")
		}
		if !strings.Contains(headers.Get("Content-Type"), "json") {
			var plain string
			if json.Unmarshal(body, &plain) == nil {
				body = []byte(plain)
			}
		}
	}
	r, _ := http.NewRequestWithContext(outer.Context(), strings.ToUpper(call.Method), target.String(), bytes.NewReader(body))
	r.Header, r.Host, r.RemoteAddr = headers, outer.Host, outer.RemoteAddr
	r.TLS = outer.TLS
	if a.RequestTimeout > 0 && readRequest(r) && !transferRequest(r) {
		ctx, cancel := context.WithTimeout(r.Context(), a.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	capture := &batchWriter{header: make(http.Header)}
	next.ServeHTTP(capture, r)
	status := capture.status
	if status == 0 {
		status = 200
	}
	result := batchResponse{ID: call.ID, Status: status, Headers: map[string]string{}}
	for _, key := range []string{"Content-Type", "ETag", "Location", "Retry-After", "X-Request-ID"} {
		if value := capture.header.Get(key); value != "" {
			result.Headers[key] = value
		}
	}
	if capture.overflow {
		result.Status = 413
		result.Headers = map[string]string{"Content-Type": "application/problem+json", "X-Request-ID": capture.header.Get("X-Request-ID")}
		result.Body = map[string]any{"type": "about:blank", "title": http.StatusText(http.StatusRequestEntityTooLarge), "status": http.StatusRequestEntityTooLarge, "code": "batch_response_too_large", "detail": "subresponse exceeds 16 MiB"}
	} else if capture.body.Len() > 0 {
		data := capture.body.Bytes()
		switch {
		case strings.Contains(capture.header.Get("Content-Type"), "json") && json.Valid(data):
			result.Body = json.RawMessage(data)
		case strings.HasPrefix(capture.header.Get("Content-Type"), "text/"):
			result.Body = string(data)
		default:
			result.Body = base64.StdEncoding.EncodeToString(data)
		}
	}
	return result
}

type batchWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *batchWriter) Header() http.Header { return w.header }
func (w *batchWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *batchWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if len(data) > maxBatchResponse-w.body.Len() {
		w.overflow = true
		return 0, fmt.Errorf("batch response exceeds %d bytes", maxBatchResponse)
	}
	return w.body.Write(data)
}

// Transfers use ResponseController deadlines; this writer has no network socket.
func (*batchWriter) SetReadDeadline(time.Time) error  { return nil }
func (*batchWriter) SetWriteDeadline(time.Time) error { return nil }
