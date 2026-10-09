package httpapi

import (
	"context"
	"mime"
	"net/http"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/transfer"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// Content transfers stream request and response bodies under their own
// deadlines, so they are plain handlers rather than Huma operations, which
// buffer bodies. They are documented on the same contract.
func (a *API) registerContent(api huma.API, mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/items/{id}/content", a.upload)
	mux.HandleFunc("GET /v1/items/{id}/content", a.download)

	oapi := api.OpenAPI()
	registry := oapi.Components.Schemas
	blob := registry.Schema(reflect.TypeFor[Blob](), true, "Blob")
	binary := &huma.Schema{Type: huma.TypeString, Format: "binary"}
	id := &huma.Param{Name: "id", In: "path", Required: true, Description: "Item ID.", Schema: &huma.Schema{Type: huma.TypeString}}
	errors := func(codes ...int) map[string]*huma.Response {
		out := map[string]*huma.Response{}
		for _, code := range codes {
			out[strconv.Itoa(code)] = &huma.Response{Ref: "#/components/responses/" + problemResponse(code)}
		}
		return out
	}
	upload := errors(400, 401, 403, 404, 409, 413, 422, 428, 500, 503)
	upload["201"] = &huma.Response{Description: "Created", Headers: map[string]*huma.Header{"ETag": {Description: "Quoted resource version after upload.", Schema: &huma.Schema{Type: huma.TypeString}}}, Content: map[string]*huma.MediaType{"application/json": {Schema: blob}}}
	oapi.AddOperation(&huma.Operation{OperationID: "uploadContent", Method: http.MethodPut, Path: "/v1/items/{id}/content", Summary: "Stream a new immutable blob revision (library items)", Description: "The request Content-Type is stored as the blob media type. Uploads beyond the server limit return 413 and leave nothing behind. Consumes a resource lock version.", Tags: []string{"Content"},
		Parameters:  []*huma.Param{id, ifMatchParam, {Name: "X-Filename", In: "header", Required: true, Description: "File name stored with the blob.", Schema: &huma.Schema{Type: huma.TypeString, MinLength: ptr(1), MaxLength: ptr(255)}}},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"*/*": {Schema: binary}}},
		Responses:   upload})
	download := errors(401, 403, 404, 500)
	download["200"] = &huma.Response{Description: "Blob bytes", Headers: map[string]*huma.Header{"ETag": {Description: "Quoted hex SHA-256 of the bytes.", Schema: &huma.Schema{Type: huma.TypeString}}, "Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString}}}, Content: map[string]*huma.MediaType{"*/*": {Schema: binary}}}
	download["206"] = &huma.Response{Description: "Requested byte range", Content: map[string]*huma.MediaType{"*/*": {Schema: binary}}}
	download["304"] = &huma.Response{Description: "Not modified (If-None-Match)"}
	download["416"] = &huma.Response{Description: "Range not satisfiable"}
	oapi.AddOperation(&huma.Operation{OperationID: "downloadContent", Method: http.MethodGet, Path: "/v1/items/{id}/content", Summary: "Download the selected head/published blob; supports byte ranges", Description: "Ordinary readers can download only the current published blob, including with blob_id. Draft readers default to head and may request retained historical blobs.", Tags: []string{"Content"},
		Parameters: []*huma.Param{id, {Name: "blob_id", In: "query", Description: "Retained blob revision to download.", Schema: &huma.Schema{Type: huma.TypeString, Format: "uuid"}}, {Name: "Range", In: "header", Description: "Single or multiple byte ranges.", Schema: &huma.Schema{Type: huma.TypeString}}},
		Responses:  download})
}

func ptr[T any](v T) *T { return &v }

func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), r.PathValue("id")
	version, p := ifMatch(ctx, r.Header.Get("If-Match"))
	if p != nil {
		writeProblem(w, p)
		return
	}
	if err := a.DMS.CanUpload(ctx, subject(ctx), id, version); err != nil {
		a.failure(w, r, err)
		return
	}
	filename := r.Header.Get("X-Filename")
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || filename == "" {
		a.problem(w, r, 400, "invalid_upload", "Content-Type and X-Filename are required")
		return
	}
	if r.ContentLength > a.MaxUpload {
		a.failure(w, r, storage.ErrTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.MaxUpload)
	object, err := a.Storage.Put(ctx, transfer.Reader(r.Body, w), a.MaxUpload)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	out, err := a.DMS.AttachBlob(ctx, subject(ctx), id, version, dms.BlobInput{ObjectKey: object.Key, Filename: filename, ContentType: media, Size: object.Size, SHA256: object.SHA256})
	if err != nil {
		if cleanupErr := a.Storage.Delete(context.Background(), object.Key); cleanupErr != nil {
			a.Logger.Error("orphan cleanup failed", "object_key", object.Key, "error", cleanupErr)
		}
		a.failure(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(version+1))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_ = jsonFormat.Marshal(w, Blob{ID: out.ID, ItemID: out.ItemID, Version: out.Version, ResourceVersion: version + 1, Filename: out.Filename, ContentType: out.ContentType, Size: out.Size, SHA256: out.Sha256})
}
func (a *API) download(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := a.DMS.GetBlob(ctx, subject(ctx), r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	file, err := a.Storage.Open(ctx, b.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", b.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": b.Filename}))
	w.Header().Set("ETag", strconv.Quote(b.Sha256))
	http.ServeContent(transfer.Writer(w), r, b.Filename, b.CreatedAt, file)
}

// describeSchemas adds what struct tags cannot express: type descriptions and
// the per-variant requirements of bulk operations and validation rules.
func describeSchemas(oapi *huma.OpenAPI) {
	schemas := oapi.Components.Schemas.Map()
	describe := map[string]string{
		"Permissions":                   "SharePoint-style exclusive scopes. An inherited resource has no local grants. Grants at the nearest exclusive ancestor apply; ancestors beyond it do not. Grants within a scope are additive. Reset with inherit=true and no grants. Denies are rejected.",
		"UpdateField":                   "Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.",
		"FilterExpr":                    "One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $tags supports equality. Multi-valued eq matches any member; ne requires present with no equal member.",
		"QuerySpec":                     "Collection query over visible surfaces. System fields: $id, $name, $tags, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision.",
		"QueryGroup":                    "Grouping key and authorized count before pagination. Integer and decimal keys are exact strings, booleans are booleans, absent values are null.",
		"ViewInput":                     "Collection-managed saved view. PUT replaces the definition.",
		"ContentTypeInput":              "At most 32 content types per collection, with exactly one default. Replacements validate existing heads against the proposed fields and rules atomically.",
		"ValidationRule":                "Comparisons require other_field of the same scalar type and decimal scale; null operands skip comparison. required_if requires when_field and non-null when_value and requires field when those values are equal. Ordered comparisons exclude boolean, choice, lookup and term. Maximum 32 rules per type.",
		"BulkOperation":                 "One item operation. create needs create; update needs id, version and update; publish, unpublish and delete need id and version.",
		"ApplyTemplateInput":            "Adopts compatible shared catalog field definitions and merges rules by key into the selected content type.",
		"SmartFolderDefinition":         "Live query across at most 100 readable collections. Name/key selectors ignore case, OR within each selector and AND between selectors; omitted selectors include all readable collections. Terms include descendants and can occupy different term fields. Incompatible collection filters are skipped; none compatible returns validation. Metadata navigation is scalar and indexed, with compatible field types/scopes across collections. Physical folders have system metadata only and are excluded when term/content-type selectors are present.",
		"SmartFolderInput":              "Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.",
		"SmartFolderResult":             "Recently modified first, ID ascending ties, permission filtering before pagination and totals. Cursors bind folder version, caller, surface, path, schemas and resolved references.",
		"SmartFolderDrop":               "Classify an existing item (item_id and version) or create one (create and optional parent_id) through ordinary item validation, revisions and publication. Inferred AND equalities, selected terms and writable path fields are applied; non-inferable predicates must already fit. The write rolls back unless the head matches the folder and path. Existing items retain their location.",
		"SmartFolderTermRef":            "Taxonomy set key and root-to-leaf term names, resolved in the target workspace.",
		"PortableSmartFolderDefinition": "Portable shared definition: named selectors and term paths, with filter literals retained literally.",
		"SmartFolderPackage":            "Shared definitions only; personal folders and ownership IDs are excluded. Imports merge by exact unique name, preserve IDs and leave other definitions intact. Target taxonomy must exist. Any invalid entry rolls back the entire package. Identical reapplication changes no versions or audit records.",
	}
	for name, text := range describe {
		if s := schemas[name]; s != nil {
			s.Description = text
		}
	}
	variant := func(action string, required ...string) *huma.Schema {
		return &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{"action": {Type: huma.TypeString, Enum: []any{action}}}, Required: append([]string{"action"}, required...)}
	}
	if s := schemas["BulkOperation"]; s != nil {
		s.OneOf = []*huma.Schema{variant("create", "create"), variant("update", "id", "version", "update"), variant("publish", "id", "version"), variant("unpublish", "id", "version"), variant("delete", "id", "version")}
	}
	if s := schemas["SmartFolderDrop"]; s != nil {
		s.OneOf = []*huma.Schema{{Type: huma.TypeObject, Required: []string{"item_id", "version"}}, {Type: huma.TypeObject, Required: []string{"create"}}}
	}
	if s := schemas["ValidationRule"]; s != nil {
		comparisons := []any{"eq", "ne", "gt", "gte", "lt", "lte"}
		s.OneOf = []*huma.Schema{
			{Type: huma.TypeObject, Properties: map[string]*huma.Schema{"op": {Type: huma.TypeString, Enum: []any{"required_if"}}}, Required: []string{"when_field", "when_value"}},
			{Type: huma.TypeObject, Properties: map[string]*huma.Schema{"op": {Type: huma.TypeString, Enum: comparisons}}, Required: []string{"other_field"}},
		}
	}
	for _, s := range schemas {
		for _, v := range s.OneOf {
			// Huma validates only required names that are declared properties.
			for _, name := range v.Required {
				if v.Properties == nil {
					v.Properties = map[string]*huma.Schema{}
				}
				if v.Properties[name] == nil {
					v.Properties[name] = &huma.Schema{}
				}
			}
			v.PrecomputeMessages()
		}
		s.PrecomputeMessages()
	}
}
