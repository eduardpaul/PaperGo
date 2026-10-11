package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
)

// anyJSON documents a response body of any JSON value. Handlers still own the
// concrete shape; conformance checks that the body is JSON when the contract
// says it is.
type anyJSON struct{}

func (anyJSON) Schema(huma.Registry) *huma.Schema { return &huma.Schema{} }

// OpenAPI returns the contract served at GET /openapi.json.
func (a *API) OpenAPI() *huma.OpenAPI {
	api := newHumaAPI(http.NewServeMux())
	for _, route := range documentedRoutes {
		documentRoute(api, route[0], route[1])
	}
	doc := api.OpenAPI()
	binary := &huma.MediaType{Schema: &huma.Schema{Type: "string", Format: "binary"}}
	if item := doc.Paths["/v1/items/{id}/content"]; item != nil {
		if item.Get != nil {
			item.Get.Responses["200"] = &huma.Response{Description: "File bytes", Content: map[string]*huma.MediaType{"application/octet-stream": binary}}
			item.Get.Responses["206"] = &huma.Response{Description: "Partial content", Content: map[string]*huma.MediaType{"application/octet-stream": binary}}
			// The handler returns the stored media type. Extra JSON success
			// statuses would make the generated client parse bytes as JSON.
			delete(item.Get.Responses, "201")
			delete(item.Get.Responses, "202")
			delete(item.Get.Responses, "204")
		}
		if item.Put != nil {
			item.Put.RequestBody = &huma.RequestBody{Required: true, Description: "File bytes. Content-Type is the stored media type and X-Filename is required.", Content: map[string]*huma.MediaType{"application/octet-stream": binary}}
		}
	}
	if item := doc.Paths["/openapi.json"]; item != nil && item.Get != nil {
		if res := item.Get.Responses["200"]; res != nil && res.Content["application/json"] != nil {
			res.Content["application/openapi+json"] = res.Content["application/json"]
			delete(res.Content, "application/json")
		}
	}
	// An empty request schema makes the generated client omit Content-Type.
	// An open object still accepts every JSON body the handlers decode.
	// Bulk operations are an array of objects; Kiota emits invalid JSON when
	// that array is only additionalData, so this request stays typed.
	if item := doc.Paths["/v1/resources/{id}/bulk"]; item != nil && item.Post != nil && item.Post.RequestBody != nil {
		if media := item.Post.RequestBody.Content["application/json"]; media != nil {
			media.Schema = &huma.Schema{
				Type:     "object",
				Required: []string{"operations"},
				Properties: map[string]*huma.Schema{
					"operations": {Type: "array", Items: &huma.Schema{
						Type: "object",
						Properties: map[string]*huma.Schema{
							"action":    {Type: "string"},
							"id":        {Type: "string"},
							"version":   {Type: "integer"},
							"parent_id": {Type: "string"},
							"create":    {Type: "object", AdditionalProperties: true},
							"update":    {Type: "object", AdditionalProperties: true},
						},
					}},
				},
			}
		}
	}
	for _, item := range doc.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil || op.RequestBody == nil {
				continue
			}
			if media := op.RequestBody.Content["application/json"]; media != nil && media.Schema != nil && media.Schema.Ref == "" && media.Schema.Type == "" {
				media.Schema.Type = "object"
				media.Schema.AdditionalProperties = true
			}
		}
	}
	return doc
}

func documentRoute(api huma.API, method, path string) {
	op := huma.Operation{
		OperationID:   operationID(method, path),
		Method:        method,
		Path:          path,
		Summary:       method + " " + path,
		DefaultStatus: 200,
		Errors:        []int{400, 401, 403, 404, 409, 412, 413, 415, 422, 428, 503},
		Responses: map[string]*huma.Response{
			"201": {Description: "Created", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{}}}},
			"202": {Description: "Accepted", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{}}}},
			"204": {Description: "No content"},
		},
	}
	if strings.HasPrefix(path, "/health/") {
		op.Security = []map[string][]string{}
	}
	if method == http.MethodGet {
		huma.Register(api, op, func(context.Context, *struct{}) (*struct{ Body anyJSON }, error) { return nil, nil })
		return
	}
	huma.Register(api, op, func(context.Context, *struct {
		Body json.RawMessage
	}) (*struct{ Body anyJSON }, error) {
		return nil, nil
	})
}

func operationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		part = strings.Trim(part, "{}")
		part = strings.TrimPrefix(part, "$")
		for i, word := range strings.FieldsFunc(part, func(r rune) bool { return r == '-' || r == '_' }) {
			if word == "" {
				continue
			}
			if i == 0 && b.Len() > len(method) {
				b.WriteString(exportWord(word))
				continue
			}
			b.WriteString(exportWord(word))
		}
	}
	return b.String()
}

func exportWord(word string) string {
	runes := []rune(word)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

var documentedRoutes = [][2]string{
	{"POST", "/v1/$batch"},
	{"GET", "/v1/workspaces"},
	{"POST", "/v1/workspaces"},
	{"GET", "/v1/workspaces/{id}/members"},
	{"POST", "/v1/workspaces/{id}/members"},
	{"DELETE", "/v1/workspaces/{id}/members/{member}"},
	{"GET", "/v1/resources"},
	{"GET", "/v1/resources/{id}"},
	{"PATCH", "/v1/resources/{id}"},
	{"DELETE", "/v1/resources/{id}"},
	{"GET", "/v1/resources/{id}/children"},
	{"POST", "/v1/resources/{id}/children"},
	{"POST", "/v1/resources/{id}/documents"},
	{"GET", "/v1/resources/{id}/fields"},
	{"POST", "/v1/resources/{id}/fields"},
	{"PATCH", "/v1/resources/{id}/fields/{fieldID}"},
	{"GET", "/v1/resources/{id}/schemas"},
	{"GET", "/v1/resources/{id}/permissions"},
	{"PUT", "/v1/resources/{id}/permissions"},
	{"GET", "/v1/items/{id}/relationships"},
	{"POST", "/v1/items/{id}/move"},
	{"POST", "/v1/items/{id}/relationships"},
	{"DELETE", "/v1/items/{id}/relationships/{linkID}"},
	{"POST", "/v1/items/{id}/publications"},
	{"GET", "/v1/items/{id}/publications"},
	{"POST", "/v1/items/{id}/unpublish"},
	{"GET", "/v1/items/{id}/revisions"},
	{"GET", "/v1/items/{id}/revisions/{number}"},
	{"POST", "/v1/items/{id}/revisions/{number}/restore"},
	{"GET", "/v1/resources/{id}/recycle-bin"},
	{"POST", "/v1/resources/{id}/recycle-bin/{itemID}/restore"},
	{"DELETE", "/v1/resources/{id}/recycle-bin/{itemID}"},
	{"GET", "/v1/items/{id}/schema"},
	{"PUT", "/v1/items/{id}/content"},
	{"GET", "/v1/items/{id}/content"},
	{"POST", "/v1/items/{id}/content/versions/{number}/restore"},
	{"GET", "/v1/workspaces/{id}/audit"},
	{"GET", "/v1/workspaces/{id}/tags"},
	{"GET", "/v1/webdav-credentials"},
	{"POST", "/v1/webdav-credentials"},
	{"DELETE", "/v1/webdav-credentials/{id}"},
	{"GET", "/openapi.json"},
	{"GET", "/health/live"},
	{"GET", "/health/ready"},
	{"GET", "/v1/collection-templates"},
	{"GET", "/v1/content-types/audit"},
	{"GET", "/v1/content-types"},
	{"POST", "/v1/content-types"},
	{"GET", "/v1/fields"},
	{"POST", "/v1/fields"},
	{"PATCH", "/v1/fields/{id}"},
	{"DELETE", "/v1/fields/{id}"},
	{"GET", "/v1/resources/{id}/content-types"},
	{"POST", "/v1/resources/{id}/content-types"},
	{"DELETE", "/v1/resources/{id}/content-types/{typeID}"},
	{"GET", "/v1/content-types/{id}"},
	{"PUT", "/v1/content-types/{id}"},
	{"DELETE", "/v1/resources/{id}/fields/{fieldID}"},
	{"POST", "/v1/resources/{id}/bulk"},
	{"GET", "/v1/workspaces/{id}/templates"},
	{"POST", "/v1/workspaces/{id}/templates"},
	{"GET", "/v1/templates/{id}"},
	{"PUT", "/v1/templates/{id}"},
	{"POST", "/v1/resources/{id}/templates/{templateID}/apply"},
	{"POST", "/v1/resources/{id}/query"},
	{"POST", "/v1/resources/{id}/query/groups"},
	{"GET", "/v1/resources/{id}/operations"},
	{"GET", "/v1/operations/{id}"},
	{"POST", "/v1/resources/{id}/bulk-update"},
	{"GET", "/v1/resources/{id}/views"},
	{"POST", "/v1/resources/{id}/views"},
	{"GET", "/v1/views/{id}"},
	{"PUT", "/v1/views/{id}"},
	{"POST", "/v1/views/{id}/query"},
	{"POST", "/v1/views/{id}/query/groups"},
	{"GET", "/v1/relationship-types"},
	{"POST", "/v1/relationship-types"},
	{"GET", "/v1/relationship-types/{id}"},
	{"PATCH", "/v1/items/{id}/relationships/{linkID}"},
	{"DELETE", "/v1/views/{id}"},
	{"GET", "/v1/people"},
	{"GET", "/v1/identity/users/{subject}/status"},
	{"PUT", "/v1/identity/users/{subject}/status"},
	{"GET", "/v1/identity/scopes"},
	{"GET", "/v1/identity/me/scopes"},
	{"GET", "/v1/identity/users"},
	{"POST", "/v1/identity/users"},
	{"GET", "/v1/identity/groups"},
	{"POST", "/v1/identity/groups"},
	{"PUT", "/v1/identity/groups/{id}"},
	{"DELETE", "/v1/identity/groups/{id}"},
	{"GET", "/v1/identity/groups/{id}/members"},
	{"GET", "/v1/identity/roles"},
	{"POST", "/v1/identity/roles"},
	{"PUT", "/v1/identity/roles/{id}"},
	{"DELETE", "/v1/identity/roles/{id}"},
	{"GET", "/v1/identity/roles/{id}/assignments"},
	{"GET", "/v1/workspaces/{id}/smart-folders/export"},
	{"POST", "/v1/workspaces/{id}/smart-folders/import"},
	{"POST", "/v1/smart-folders/{id}/items"},
	{"DELETE", "/v1/smart-folders/{id}/items/{itemID}"},
	{"POST", "/v1/smart-folders/{id}/query"},
	{"POST", "/v1/smart-folders/{id}/query/groups"},
	{"GET", "/v1/smart-folders"},
	{"POST", "/v1/smart-folders"},
	{"GET", "/v1/smart-folders/{id}"},
	{"PUT", "/v1/smart-folders/{id}"},
	{"DELETE", "/v1/smart-folders/{id}"},
	{"GET", "/v1/taxonomy/audit"},
	{"GET", "/v1/term-groups"},
	{"POST", "/v1/term-groups"},
	{"GET", "/v1/term-groups/{id}"},
	{"PUT", "/v1/term-groups/{id}"},
	{"DELETE", "/v1/term-groups/{id}"},
	{"POST", "/v1/term-groups/{id}/import"},
	{"GET", "/v1/taxonomy/export"},
	{"POST", "/v1/taxonomy/import"},
	{"GET", "/v1/term-sets"},
	{"POST", "/v1/term-sets"},
	{"GET", "/v1/term-sets/{id}"},
	{"PUT", "/v1/term-sets/{id}"},
	{"DELETE", "/v1/term-sets/{id}"},
	{"GET", "/v1/term-sets/{id}/terms"},
	{"POST", "/v1/term-sets/{id}/terms"},
	{"GET", "/v1/terms"},
	{"GET", "/v1/terms/{id}"},
	{"PUT", "/v1/terms/{id}"},
	{"POST", "/v1/terms/{id}/move"},
	{"POST", "/v1/terms/{id}/merge"},
	{"GET", "/v1/keywords"},
	{"POST", "/v1/keywords"},
	{"GET", "/v1/keywords/popular"},
	{"POST", "/v1/keywords/{id}/promote"},
	{"GET", "/v1/workflow-catalog"},
	{"GET", "/v1/workspaces/{id}/workflows"},
	{"POST", "/v1/workspaces/{id}/workflows"},
	{"GET", "/v1/workflows/{id}"},
	{"PUT", "/v1/workflows/{id}"},
	{"DELETE", "/v1/workflows/{id}"},
	{"GET", "/v1/workflows/{id}/versions"},
	{"POST", "/v1/workflows/{id}/runs"},
	{"GET", "/v1/workspaces/{id}/workflow-builtins"},
	{"PUT", "/v1/workspaces/{id}/workflow-builtins/{key}"},
	{"POST", "/v1/workspaces/{id}/workflow-builtins/{key}/copy"},
	{"GET", "/v1/workspaces/{id}/workflow-runs"},
	{"GET", "/v1/workflow-runs/{id}"},
	{"POST", "/v1/workflow-runs/{id}/cancel"},
	{"POST", "/v1/workflow-runs/{id}/retry"},
	{"PUT", "/v1/identity/groups/{id}/members/{principalType}/{principalID}"},
	{"DELETE", "/v1/identity/groups/{id}/members/{principalType}/{principalID}"},
	{"PUT", "/v1/identity/roles/{id}/assignments/{principalType}/{principalID}"},
	{"DELETE", "/v1/identity/roles/{id}/assignments/{principalType}/{principalID}"},
	{"GET", "/v1/identity/me/preferences"},
	{"PATCH", "/v1/identity/me/preferences"},
	{"GET", "/v1/organization/preferences"},
	{"PATCH", "/v1/organization/preferences"},
}
