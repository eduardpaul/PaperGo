package httpapi

//go:generate go run ../../cmd/openapi -o ../../api/openapi.json
//go:generate dotnet tool restore
//go:generate dotnet tool run kiota generate --openapi ../../api/openapi.json --language TypeScript --class-name PaperGoClient --namespace-name papergo --output ../../sdk/typescript/src/generated --clean-output --exclude-backward-compatible --log-level Warning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

const (
	apiTitle       = "PaperGo headless DMS"
	apiVersion     = "0.4.0"
	apiDescription = "One organization per SQLite deployment. Immutable schema/content revisions, SharePoint-style exclusive permission scopes, head/published projections, rich typed fields, reusable templates, saved queries, taxonomy, and typed relationships. Libraries with webdav_enabled are also served over WebDAV (RFC 4918 class 1 and 2) at /webdav/{libraryID}/, outside this contract; WebDAV accepts these bearer tokens or HTTP Basic with a WebDAV credential password.\n\nThis document is generated from the Go handlers (`go generate ./internal/httpapi`); do not edit it by hand."
)

// newHumaAPI describes the REST contract on mux. The generated OpenAPI
// document is the API's source of truth: no spec, docs or schema routes are
// served by Huma itself, and responses carry no $schema links.
func newHumaAPI(mux *http.ServeMux) huma.API {
	config := huma.DefaultConfig(apiTitle, apiVersion)
	config.CreateHooks = nil
	config.OpenAPIPath, config.DocsPath, config.SchemasPath = "", "", ""
	config.Formats = map[string]huma.Format{"application/json": jsonFormat, "json": jsonFormat}
	config.Info.Description = apiDescription
	config.Servers = []*huma.Server{{URL: "http://127.0.0.1:8080"}}
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"}}
	config.Security = []map[string][]string{{"bearerAuth": {}}}
	problem := config.Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "Problem")
	config.Components.Responses = map[string]*huma.Response{}
	for status, text := range problemResponses {
		config.Components.Responses[problemResponse(status)] = &huma.Response{Description: text, Content: map[string]*huma.MediaType{"application/problem+json": {Schema: problem}}}
	}
	// Operations share one documented response per error status.
	config.OnAddOperation = append(config.OnAddOperation, func(_ *huma.OpenAPI, op *huma.Operation) {
		for code := range op.Responses {
			if status, err := strconv.Atoi(code); err == nil && problemResponses[status] != "" {
				op.Responses[code] = &huma.Response{Ref: "#/components/responses/" + problemResponse(status)}
			}
		}
	})
	return humago.New(mux, config)
}

var problemResponses = map[int]string{
	400: "Malformed JSON body or If-Match header (invalid_json, invalid_precondition, invalid_upload).",
	401: "Missing or invalid bearer token (unauthorized).",
	403: "The caller lacks the required permission (forbidden).",
	404: "The entity does not exist or is not visible to the caller (not_found).",
	409: "Stale If-Match version, duplicate name or key, or business key collision (conflict).",
	413: "Request body exceeds the size limit (payload_too_large).",
	415: "Request body is not JSON (unsupported_media_type).",
	422: "Request failed schema or business validation (validation_failed); errors lists schema failures.",
	428: "If-Match is required (precondition_required).",
	500: "Unexpected server error (internal_error); the request ID locates the log entry.",
	503: "Server at capacity or read time limit reached, retry after Retry-After (overloaded, timeout), or not ready (unavailable).",
}

func problemResponse(status int) string {
	return strings.ReplaceAll(http.StatusText(status), " ", "")
}

// jsonFormat decodes request bodies with exact numbers (64-bit integers and
// decimals keep their digits) and accepts exactly one JSON value.
var jsonFormat = huma.Format{
	Marshal: huma.DefaultJSONFormat.Marshal,
	Unmarshal: func(data []byte, v any) error {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(v); err != nil {
			return err
		}
		if _, err := decoder.Token(); err != io.EOF {
			return errors.New("request must contain one JSON value")
		}
		return nil
	},
}

// Problem is the RFC 9457 problem response of every failed API request.
type Problem struct {
	Type           string          `json:"type" doc:"Always about:blank; code identifies the problem."`
	Title          string          `json:"title" doc:"HTTP status text."`
	Status         int             `json:"status"`
	Code           string          `json:"code" doc:"Stable machine-readable problem code, e.g. not_found, conflict, validation_failed, precondition_required."`
	Detail         string          `json:"detail"`
	RequestID      string          `json:"request_id" doc:"Also returned as the X-Request-ID header."`
	OperationIndex *int            `json:"operation_index,omitempty" minimum:"0" doc:"Zero-based failing operation index for bulk item errors. Omitted for batch-wide errors."`
	Errors         []ProblemDetail `json:"errors,omitempty" doc:"Request fields that failed schema validation."`
	retryAfter     bool
}

// ProblemDetail locates one request validation failure.
type ProblemDetail struct {
	Location string `json:"location,omitempty" doc:"Where the error occurred, e.g. body.name or query.limit."`
	Message  string `json:"message"`
}

func (p *Problem) Error() string             { return p.Detail }
func (p *Problem) GetStatus() int            { return p.Status }
func (p *Problem) ContentType(string) string { return "application/problem+json" }
func (p *Problem) GetHeaders() http.Header {
	if !p.retryAfter {
		return nil
	}
	return http.Header{"Retry-After": {"1"}}
}

func newProblem(ctx context.Context, status int, code, detail string) *Problem {
	p := &Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, Detail: detail}
	if ctx != nil {
		p.RequestID, _ = ctx.Value(requestKey{}).(string)
	}
	return p
}

// humaProblem converts errors raised by Huma itself (request decoding and
// schema validation) and errors returned from resolvers into problems.
func humaProblem(ctx context.Context, status int, msg string, errs ...error) *Problem {
	for _, err := range errs {
		var p *Problem
		if errors.As(err, &p) {
			return p
		}
	}
	code := map[int]string{400: "invalid_json", 406: "not_acceptable", 408: "timeout", 413: "payload_too_large", 415: "unsupported_media_type", 422: "validation_failed", 500: "internal_error"}[status]
	if code == "" {
		code = strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
	}
	p := newProblem(ctx, status, code, msg)
	var details []string
	for _, err := range errs {
		var d huma.ErrorDetailer
		if errors.As(err, &d) {
			detail := d.ErrorDetail()
			p.Errors = append(p.Errors, ProblemDetail{Location: detail.Location, Message: detail.Message})
			details = append(details, strings.TrimPrefix(detail.Location+": ", ": ")+detail.Message)
		} else if err != nil && status < 500 {
			details = append(details, err.Error())
		}
	}
	if len(details) > 0 {
		p.Detail = msg + ": " + strings.Join(details, "; ")
	}
	return p
}

func init() {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return humaProblem(nil, status, msg, errs...)
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return humaProblem(ctx.Context(), status, msg, errs...)
	}
}

// fail maps service and storage errors to problems; unexpected errors are logged.
func (a *API) fail(ctx context.Context, err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	var validation *dms.ValidationError
	var max *http.MaxBytesError
	switch {
	case errors.As(err, &validation):
		p = newProblem(ctx, 422, "validation_failed", validation.Error())
	case errors.Is(err, dms.ErrNotFound):
		p = newProblem(ctx, 404, "not_found", "resource not found")
	case errors.Is(err, dms.ErrForbidden):
		p = newProblem(ctx, 403, "forbidden", "access denied")
	case errors.Is(err, dms.ErrConflict):
		p = newProblem(ctx, 409, "conflict", "version conflict or duplicate")
	case errors.Is(err, storage.ErrTooLarge) || errors.As(err, &max):
		p = newProblem(ctx, 413, "payload_too_large", "request body exceeds size limit")
	case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
		p = newProblem(ctx, 503, "timeout", "request exceeded the server time limit")
		p.retryAfter = true
	default:
		a.Logger.ErrorContext(ctx, "request failed", "error", err, "request_id", ctx.Value(requestKey{}))
		p = newProblem(ctx, 500, "internal_error", "request could not be completed")
	}
	var bulk *dms.BulkError
	if errors.As(err, &bulk) {
		p.OperationIndex = &bulk.Index
	}
	return p
}

// precondition reads the If-Match header that versioned mutations require.
// Embedding it documents the header and resolves Version before the handler runs.
type precondition struct{ Version int }

func (*precondition) conditional() {}
func (p *precondition) Resolve(ctx huma.Context) []error {
	version, err := ifMatch(ctx.Context(), ctx.Header("If-Match"))
	if err != nil {
		return []error{err}
	}
	p.Version = version
	return nil
}
func ifMatch(ctx context.Context, raw string) (int, *Problem) {
	if raw == "" {
		return 0, newProblem(ctx, 428, "precondition_required", "If-Match with the current resource ETag is required")
	}
	v, err := strconv.Unquote(raw)
	if err != nil {
		return 0, newProblem(ctx, 400, "invalid_precondition", "If-Match must be a quoted integer ETag")
	}
	version, err := strconv.Atoi(v)
	if err != nil || version < 1 {
		return 0, newProblem(ctx, 400, "invalid_precondition", "If-Match must be a positive integer ETag")
	}
	return version, nil
}

var ifMatchParam = &huma.Param{Name: "If-Match", In: "header", Required: true, Description: "Current quoted numeric ETag of the entity being changed, e.g. \"1\". Missing returns 428, stale returns 409.", Schema: &huma.Schema{Type: huma.TypeString, Pattern: `^"[1-9][0-9]*"$`}}

// register adds one operation. Handler errors become problems, and the
// documented error responses follow from what the operation accepts.
func register[I, O any](a *API, api huma.API, op huma.Operation, handler func(context.Context, *I) (*O, error)) {
	input := reflect.TypeFor[I]()
	errs := map[int]bool{401: true, 500: true, 503: true}
	if strings.Contains(op.Path, "{") {
		errs[403], errs[404] = true, true
	}
	if _, ok := input.FieldByName("Body"); ok {
		errs[400], errs[413], errs[415], errs[422] = true, true, true, true
	}
	if input.NumField() > 0 {
		errs[422] = true
	}
	if op.Method != http.MethodGet {
		errs[409] = true
	}
	if _, ok := any(new(I)).(interface{ conditional() }); ok {
		errs[400], errs[428] = true, true
		op.Parameters = append(op.Parameters, ifMatchParam)
	}
	if op.Security != nil {
		errs = map[int]bool{503: true}
	}
	for code := range errs {
		op.Errors = append(op.Errors, code)
	}
	slices.Sort(op.Errors)
	huma.Register(api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := handler(ctx, in)
		if err != nil {
			return nil, a.fail(ctx, err)
		}
		return out, nil
	})
}

// Spec renders the contract as the indented JSON committed in api/openapi.json.
func Spec() ([]byte, error) {
	compact, err := json.Marshal((&API{}).OpenAPI())
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err = json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
