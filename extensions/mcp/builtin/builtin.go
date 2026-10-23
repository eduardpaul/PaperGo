// Package builtin holds the MCP tools papergo-mcp always serves: discovery
// of workspaces and collections, collection schemas and views, structured
// queries, item reads, file text, terms, creates and updates, and search.
// Each acts through the PaperGo API as its caller, so the API decides what
// they see and change.
// Custom values and filter literals stay raw JSON from end to end, so
// integers and decimals keep every digit.
package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"papergo/extensions/mcp"
	"papergo/internal/dal/refs"
)

// The PaperGo scopes the tools require. The API still authorizes every
// request against the caller's access to each resource.
const (
	scopeWorkspaceRead   = "workspace.read"
	scopeItemRead        = "item.read"
	scopeItemWrite       = "item.write"
	scopeContentTypeRead = "contentType.read"
	scopeDocumentRead    = "document.read"
	scopeTaxonomyRead    = "taxonomy.read"
	scopeUserRead        = "user.read"
)

// Page bounds.
const (
	defaultLimit       = 50
	maxLimit           = 200
	defaultSearchLimit = 10
	maxSearchLimit     = 50
	maxCursorBytes     = 4096
)

// Options configure the built-in tools.
type Options struct {
	// AppURL is the origin of the PaperGo web app the tools link to. Without
	// it, results carry no links.
	AppURL *url.URL
}

// Tools are the built-in tools.
func Tools(o Options) []mcp.Tool {
	l := links{o.AppURL}
	return []mcp.Tool{listWorkspaces(l), listCollections(l), getCollectionSchema(l), queryItems(l), getItem(l), getItemText(), listTerms(),
		createItem(l), updateItem(l), search(l)}
}

// decode reads validated arguments into dst, keeping numbers exact.
func decode(raw json.RawMessage, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return mcp.Fail(mcp.CodeInvalidArguments, "invalid arguments: "+err.Error())
	}
	return nil
}

func encode(v any) (json.RawMessage, error) { return json.Marshal(v) }

// get reads an API resource as the caller.
func get(ctx context.Context, c mcp.Caller, path string, query url.Values, dst any) error {
	res, err := c.Backend().Do(ctx, mcp.Request{Method: http.MethodGet, Path: path, Query: query})
	if err != nil {
		return err
	}
	return unmarshal(res.Body, dst)
}

// unmarshal reads an API answer, keeping numbers exact.
func unmarshal(raw json.RawMessage, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: unexpected answer: %v", mcp.ErrUnavailable, err)
	}
	return nil
}

func resourcePath(id string, rest ...string) string {
	p := "/v1/resources/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// page is a page of API results with its continuation.
type page struct {
	Value    []json.RawMessage `json:"value"`
	NextLink *string           `json:"@odata.nextLink"`
}

// cursor is a tool's continuation: which tool, caller and arguments it
// belongs to and where the API continues. It is not a credential: the API
// checks every page against the caller's access.
type cursor struct {
	Tool    string `json:"t"`
	Caller  string `json:"c"`
	Args    string `json:"a"`
	Backend string `json:"k"`
}

// paging continues a tool's listing: it reads the arguments' cursor, if
// any, and makes the next one.
type paging struct {
	tool, caller, args string
}

func newPaging(tool string, c mcp.Caller, args json.RawMessage) (paging, error) {
	fingerprint, err := fingerprint(args)
	return paging{tool: tool, caller: digest(c.Subject()), args: fingerprint}, err
}

// resume returns the API's skip token of a cursor these arguments made.
func (p paging) resume(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	bad := mcp.Fail(mcp.CodeInvalidArguments, "cursor is not a next_cursor of this tool, caller and query; start again without it")
	if len(raw) > maxCursorBytes {
		return "", bad
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", bad
	}
	var c cursor
	if json.Unmarshal(decoded, &c) != nil || c.Tool != p.tool || c.Caller != p.caller || c.Args != p.args || c.Backend == "" {
		return "", bad
	}
	return c.Backend, nil
}

// next is the cursor of the page after one the API answered at path with
// query: the skip token of its next link, which must name the same path
// and query. nil means the last page.
func (p paging) next(link *string, path string, query url.Values) (*string, error) {
	if link == nil || *link == "" {
		return nil, nil
	}
	u, err := url.Parse(*link)
	unexpected := fmt.Errorf("%w: unexpected continuation", mcp.ErrUnavailable)
	if err != nil || u.EscapedPath() != path {
		return nil, unexpected
	}
	got := u.Query()
	token := got.Get("$skiptoken")
	if token == "" || len(got["$skiptoken"]) != 1 {
		return nil, unexpected
	}
	got.Del("$skiptoken")
	want := url.Values{}
	for k, v := range query {
		if k != "$skiptoken" {
			want[k] = v
		}
	}
	if got.Encode() != want.Encode() {
		return nil, unexpected
	}
	raw, err := json.Marshal(cursor{Tool: p.tool, Caller: p.caller, Args: p.args, Backend: token})
	if err != nil {
		return nil, err
	}
	out := base64.RawURLEncoding.EncodeToString(raw)
	if len(out) > maxCursorBytes {
		return nil, unexpected
	}
	return &out, nil
}

// fingerprint identifies a call's arguments apart from its cursor, in
// canonical form: keys sorted, numbers exact.
func fingerprint(args json.RawMessage) (string, error) {
	var m map[string]any
	if err := unmarshal(args, &m); err != nil {
		return "", mcp.Fail(mcp.CodeInvalidArguments, "arguments must be a JSON object")
	}
	delete(m, "cursor")
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return digest(string(raw)), nil
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// limit is a page size within bounds, or the default.
func limit(n *int, fallback int) string {
	if n == nil {
		return fmt.Sprint(fallback)
	}
	return fmt.Sprint(*n)
}

// resource is what the tools read of an API resource.
type resource struct {
	ID                string            `json:"id"`
	Kind              refs.ResourceKind `json:"kind"`
	WorkspaceID       string            `json:"workspace_id"`
	ParentID          *string           `json:"parent_id"`
	ContainerID       *string           `json:"container_id"`
	ContentTypeID     *string           `json:"content_type_id"`
	PersonalOwnerID   *string           `json:"personal_owner_id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Keywords          []string          `json:"keywords"`
	TemplateKeys      []string          `json:"template_keys"`
	Values            json.RawMessage   `json:"values"`
	PublishingEnabled bool              `json:"publishing_enabled"`
	AllowFolders      bool              `json:"allow_folders"`
	Version           int               `json:"version"`
	SchemaHeadID      *string           `json:"schema_head_id"`
	HeadRevisionID    *string           `json:"head_revision_id"`
	PublishedRevision *string           `json:"published_revision_id"`
	Archived          bool              `json:"archived"`
	CreatedAt         string            `json:"created_at"`
	CreatedBy         string            `json:"created_by"`
	UpdatedAt         string            `json:"updated_at"`
	UpdatedBy         string            `json:"updated_by"`
	Access            []string          `json:"access"`
}

// item is an item or folder as the tools return it.
type item struct {
	ID            string            `json:"id"`
	Kind          refs.ResourceKind `json:"kind"`
	WorkspaceID   string            `json:"workspace_id"`
	CollectionID  *string           `json:"collection_id"`
	ParentID      *string           `json:"parent_id"`
	Name          string            `json:"name"`
	ContentTypeID *string           `json:"content_type_id"`
	Version       int               `json:"version"`
	CreatedAt     string            `json:"created_at"`
	CreatedBy     string            `json:"created_by"`
	UpdatedAt     string            `json:"updated_at"`
	UpdatedBy     string            `json:"updated_by"`
	Keywords      []string          `json:"keywords"`
	Values        json.RawMessage   `json:"values"`
	// Labels names the IDs the entry holds; see label.
	Labels map[string]string `json:"labels"`
	// LabelsMissing says labels could not be read after a write.
	LabelsMissing bool     `json:"labels_missing,omitempty"`
	Draft         bool     `json:"draft"`
	Archived      bool     `json:"archived"`
	URL           *string  `json:"url,omitempty"`
	Access        []string `json:"access,omitempty"`
}

// item is the entry r as the tools return it, without labels.
func (r resource) item(l links, withAccess bool) item {
	out := item{ID: r.ID, Kind: r.Kind, WorkspaceID: r.WorkspaceID, CollectionID: r.ContainerID, ParentID: r.ParentID, Name: r.Name, ContentTypeID: r.ContentTypeID,
		Version: r.Version, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy, UpdatedAt: r.UpdatedAt, UpdatedBy: r.UpdatedBy, Keywords: r.Keywords, Values: r.Values,
		Labels: map[string]string{}, Archived: r.Archived, URL: l.entry(r)}
	// A head that is not the published revision holds unpublished changes.
	// Published surfaces leave the head out.
	out.Draft = r.HeadRevisionID != nil && (r.PublishedRevision == nil || *r.HeadRevisionID != *r.PublishedRevision)
	if out.Keywords == nil {
		out.Keywords = []string{}
	}
	if len(out.Values) == 0 || bytes.Equal(out.Values, []byte("null")) {
		out.Values = json.RawMessage(`{}`)
	}
	if withAccess {
		out.Access = r.Access
		if out.Access == nil {
			out.Access = []string{}
		}
	}
	return out
}

// collection is a list or library as the tools return it.
type collection struct {
	ID                string            `json:"id"`
	Kind              refs.ResourceKind `json:"kind"`
	WorkspaceID       string            `json:"workspace_id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	TemplateKeys      []string          `json:"template_keys"`
	PublishingEnabled bool              `json:"publishing_enabled"`
	AllowFolders      bool              `json:"allow_folders"`
	Version           int               `json:"version"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
	URL               *string           `json:"url,omitempty"`
}

func (r resource) collection(l links) collection {
	out := collection{ID: r.ID, Kind: r.Kind, WorkspaceID: r.WorkspaceID, Name: r.Name, Description: r.Description, TemplateKeys: r.TemplateKeys,
		PublishingEnabled: r.PublishingEnabled, AllowFolders: r.AllowFolders, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		URL: l.collection(r.WorkspaceID, r.ID)}
	if out.TemplateKeys == nil {
		out.TemplateKeys = []string{}
	}
	return out
}

func isCollection(k refs.ResourceKind) bool { return k == refs.KindList || k == refs.KindLibrary }
func isEntry(k refs.ResourceKind) bool      { return k == refs.KindItem || k == refs.KindFolder }

// readResource reads one resource as the caller.
func readResource(ctx context.Context, c mcp.Caller, id, surface string) (resource, error) {
	var query url.Values
	if surface != "" {
		query = url.Values{"surface": {surface}}
	}
	var r resource
	err := get(ctx, c, resourcePath(id), query, &r)
	return r, err
}

// listed reads the resources of an API page that keep accepts, or all.
func listed(p page, keep func(resource) bool) ([]resource, error) {
	var out []resource
	for _, raw := range p.Value {
		var r resource
		if err := unmarshal(raw, &r); err != nil {
			return nil, err
		}
		if keep == nil || keep(r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// wrongKind is the failure of an ID that names another kind of resource.
func wrongKind(id string, kind refs.ResourceKind, want ...string) error {
	return mcp.Fail(mcp.CodeInvalidArguments, fmt.Sprintf("%s is a %s; give the ID of a %s", id, kind, joinOr(want)))
}

func joinOr(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return fmt.Sprintf("%s or %s", joinComma(words[:len(words)-1]), words[len(words)-1])
}

func joinComma(words []string) string {
	out := ""
	for i, w := range words {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out
}
