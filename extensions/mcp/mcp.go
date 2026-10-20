// Package mcp is the contract of PaperGo's MCP server, papergo-mcp: the
// tools it serves, the caller they act for and the PaperGo API they act
// through. The server's built-in tools use the same contract as compiled
// contributions, so neither parses credentials, opens connections or sees
// the protocol: a tool receives its validated arguments and a caller whose
// backend reaches only the configured PaperGo API, as that caller.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Tool is one MCP tool.
type Tool struct {
	// Name is the tool's protocol name: 1 to 64 letters, digits, "_" or "-".
	Name        string
	Title       string
	Description string
	// InputSchema is a JSON Schema object (draft 2020-12) for the
	// arguments. Its top level declares its required properties and rejects
	// any others; the server validates every call against it.
	InputSchema json.RawMessage
	// OutputSchema describes the structured result, when the tool has one.
	OutputSchema json.RawMessage
	// Scopes are the PaperGo scopes the caller must hold, at least one. The
	// API still authorizes every request the tool makes.
	Scopes      []string
	Annotations Annotations
	// Available, when set, reports whether the tool serves this caller now,
	// such as when it needs an optional API feature. It is asked when tools
	// are listed and again before each call.
	Available func(context.Context, Caller) (bool, error)
	// Call runs the tool with arguments that passed InputSchema. It returns
	// the structured result, or an error: a *Failure, an *APIError, or
	// another error, which callers see as an internal failure.
	Call func(context.Context, Caller, json.RawMessage) (json.RawMessage, error)
}

// Annotations are hints for clients. They never authorize anything.
type Annotations struct {
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

// Caller is who a request acts for.
type Caller interface {
	// Subject is the caller's PaperGo subject.
	Subject() string
	// HasScope reports whether the caller's effective scopes, its roles'
	// grants narrowed by its credential, include scope.
	HasScope(scope string) bool
	// Backend is the PaperGo API, as the caller.
	Backend() Backend
}

// Backend sends requests to the configured PaperGo API with the caller's
// credential. It reaches only that API's /v1 paths and never follows
// redirects.
type Backend interface {
	Do(ctx context.Context, r Request) (*Response, error)
}

// Request is an API request. Path is an absolute /v1 path whose segments
// the caller escaped; Body, when set, is sent as JSON.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   json.RawMessage
	// IfMatch, when positive, is the resource version a write requires.
	IfMatch int
}

// Response is a successful API response.
type Response struct {
	Status    int
	Body      json.RawMessage
	RequestID string
}

// APIError is an API response with an error status: its problem code and
// detail, which never carry credentials.
type APIError struct {
	Status    int
	Code      string
	Detail    string
	RequestID string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("PaperGo answered %d %s: %s", e.Status, e.Code, e.Detail)
}

// ErrUnavailable means the API could not be reached or did not answer as
// it should.
var ErrUnavailable = errors.New("PaperGo is unavailable")

// Failure codes.
const (
	CodeInvalidArguments = "invalid_arguments"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeVersionConflict  = "version_conflict"
	CodeConflict         = "conflict"
	CodeSchemaChanged    = "schema_changed"
	CodeTooLarge         = "too_large"
	CodeUnavailable      = "unavailable"
	CodeInternal         = "internal_error"
)

// Failure is a tool result that reports an error the caller can act on,
// such as invalid arguments or a stale version.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Retryable says the same call may succeed later.
	Retryable bool `json:"retryable,omitempty"`
	// RequestID is the API request that failed, when one did.
	RequestID string `json:"request_id,omitempty"`
}

func (f *Failure) Error() string { return f.Code + ": " + f.Message }

// Fail returns a failure with code and message.
func Fail(code, message string) *Failure { return &Failure{Code: code, Message: message} }

// FailureOf turns an error a tool returned into the failure its caller
// sees. API errors keep their detail and request ID; unknown errors become
// an internal failure whose text reveals nothing.
func FailureOf(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	var api *APIError
	if errors.As(err, &api) {
		out := &Failure{Message: api.Detail, RequestID: api.RequestID}
		switch {
		case api.Status == http.StatusBadRequest || api.Status == http.StatusUnprocessableEntity || api.Status == http.StatusRequestedRangeNotSatisfiable:
			out.Code = CodeInvalidArguments
		case api.Status == http.StatusUnauthorized:
			out.Code = CodeUnauthorized
		case api.Status == http.StatusForbidden:
			out.Code = CodeForbidden
		case api.Status == http.StatusNotFound || api.Status == http.StatusGone:
			out.Code = CodeNotFound
		case api.Status == http.StatusPreconditionFailed || api.Status == http.StatusPreconditionRequired:
			out.Code = CodeVersionConflict
		case api.Status == http.StatusConflict:
			out.Code = CodeConflict
		case api.Status == http.StatusRequestEntityTooLarge:
			out.Code = CodeTooLarge
		case api.Status == http.StatusTooManyRequests || api.Status == http.StatusServiceUnavailable:
			out.Code, out.Retryable = CodeUnavailable, true
		default:
			out.Code, out.Message, out.Retryable = CodeUnavailable, "PaperGo failed to answer the request.", true
		}
		if out.Message == "" {
			out.Message = http.StatusText(api.Status)
		}
		return out
	}
	if errors.Is(err, ErrUnavailable) {
		return &Failure{Code: CodeUnavailable, Message: "PaperGo is unavailable; try again shortly.", Retryable: true}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Failure{Code: CodeUnavailable, Message: "The request took too long.", Retryable: true}
	}
	return &Failure{Code: CodeInternal, Message: "The tool failed."}
}
