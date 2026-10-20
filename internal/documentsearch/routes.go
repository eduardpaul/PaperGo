package documentsearch

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// Module serves the search routes.
func (x *Search) Module() httpapi.Module { return module{x} }

type module struct{ x *Search }

func (module) Name() string { return "documentsearch" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
		{Method: http.MethodGet, Pattern: "/v1/search", OperationID: "search", Handle: m.search},
		{Method: http.MethodPost, Pattern: "/v1/search", OperationID: "searchWithFilter", Handle: m.search},
		{Method: http.MethodPost, Pattern: "/v1/search/reindex", OperationID: "reindexSearch", Handle: m.reindex},
		{Method: http.MethodGet, Pattern: "/v1/search/status", OperationID: "searchStatus", Handle: m.status},
		{Method: http.MethodPost, Pattern: "/v1/items/{id}/search/chunks", OperationID: "chunkItemText", Handle: m.chunk},
		{Method: http.MethodGet, Pattern: "/v1/search/generations/{id}/chunks", OperationID: "generationChunks", Handle: m.chunks},
		{Method: http.MethodPut, Pattern: "/v1/search/generations/{id}/enrichments", OperationID: "enrichGeneration", Handle: m.enrich},
		{Method: http.MethodPost, Pattern: "/v1/search/generations/{id}/embeddings", OperationID: "embedGeneration", Handle: m.generation(KindEmbed)},
		{Method: http.MethodPost, Pattern: "/v1/search/generations/{id}/publish", OperationID: "publishGeneration", Handle: m.generation(KindPublish)},
		{Method: http.MethodPost, Pattern: "/v1/workspaces/{id}/search/sync", OperationID: "syncSearch", Handle: m.sync},
		{Method: http.MethodGet, Pattern: "/v1/workspaces/{id}/search/term-entries", OperationID: "searchTermEntries", Handle: m.termEntries},
	}
}

func (m module) chunk(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request ChunkRequest
	if !in.Decode(w, r, &request) {
		return nil
	}
	op, err := m.x.StartChunk(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (m module) generation(kind string) func(http.ResponseWriter, *http.Request, httpapi.Request) error {
	return func(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
		op, err := m.x.StartGeneration(r.Context(), in.DMS, in.Subject, kind, r.PathValue("id"))
		if err != nil {
			return err
		}
		httpapi.Accepted(w, r, op)
		return nil
	}
}

func (m module) chunks(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	offset, limit := 0, 20
	var err error
	if v := r.URL.Query().Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil {
			return dms.Invalid("offset must be a whole number")
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil {
			return dms.Invalid("limit must be a whole number")
		}
	}
	page, err := m.x.Chunks(r.Context(), in.DMS, in.Subject, r.PathValue("id"), offset, limit)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, page)
	return nil
}

func (m module) enrich(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request struct {
		Enrichments []Enrichment `json:"enrichments"`
	}
	if !in.Decode(w, r, &request) {
		return nil
	}
	if err := m.x.Enrich(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request.Enrichments); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m module) sync(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request struct {
		ResourceIDs []string `json:"resource_ids"`
	}
	if !in.Decode(w, r, &request) {
		return nil
	}
	op, err := StartSync(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request.ResourceIDs)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (m module) reindex(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request Reindex
	if r.ContentLength != 0 && !in.Decode(w, r, &request) {
		return nil
	}
	op, err := StartReindex(r.Context(), in.DMS, in.Subject, request)
	if err != nil {
		return err
	}
	httpapi.Accepted(w, r, op)
	return nil
}

func (m module) status(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	out, err := m.x.Status(r.Context(), in.DMS, in.Subject)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// page is a page of search results with its continuation.
type page struct {
	Result
	cursor string
}

func (p page) APIPage() (map[string]any, string) {
	out := map[string]any{"value": p.Hits, "total": p.Total, "mode": p.Mode}
	if p.TotalEstimated {
		out["total_estimated"] = true
	}
	if p.Facets != nil {
		out["facets"] = p.Facets
	}
	return out, p.cursor
}

// cursor continues a search: a position in the store's results, bound to
// the caller and the search, so it cannot page another search.
type cursor struct {
	Offset int    `json:"o"`
	Query  string `json:"q"`
	// Mode is the mode the first page ran in, so later pages page the same
	// results.
	Mode string `json:"m,omitempty"`
}

// body is the JSON body of POST /v1/search: the parameters of GET
// /v1/search, and a filter on search fields. $top and $skiptoken stay in
// the URL.
type body struct {
	Q             string          `json:"q"`
	Mode          string          `json:"mode"`
	Surface       string          `json:"surface"`
	Kind          []string        `json:"kind"`
	WorkspaceID   string          `json:"workspace_id"`
	CollectionID  string          `json:"collection_id"`
	ContentTypeID string          `json:"content_type_id"`
	TermID        string          `json:"term_id"`
	Keyword       string          `json:"keyword"`
	CreatedBy     string          `json:"created_by"`
	ModifiedBy    string          `json:"modified_by"`
	ModifiedFrom  *time.Time      `json:"modified_from"`
	ModifiedTo    *time.Time      `json:"modified_to"`
	Facets        *bool           `json:"facets"`
	Filter        *dms.FilterExpr `json:"filter"`
}

// readRequest reads a search from the URL (GET) or the body (POST). When it
// reports false it has answered the request with the problem.
func readRequest(w http.ResponseWriter, r *http.Request, in httpapi.Request) (Request, bool, error) {
	var b body
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		if !in.Decode(w, r, &b) {
			return Request{}, false, nil
		}
	} else {
		b = body{Q: q.Get("q"), Mode: q.Get("mode"), Surface: q.Get("surface"), WorkspaceID: q.Get("workspace_id"), CollectionID: q.Get("collection_id"), ContentTypeID: q.Get("content_type_id"),
			TermID: q.Get("term_id"), Keyword: q.Get("keyword"), CreatedBy: q.Get("created_by"), ModifiedBy: q.Get("modified_by")}
		if kind := q.Get("kind"); kind != "" {
			b.Kind = strings.Split(kind, ",")
		}
		if q.Get("facets") == "false" {
			off := false
			b.Facets = &off
		}
		for key, into := range map[string]**time.Time{"modified_from": &b.ModifiedFrom, "modified_to": &b.ModifiedTo} {
			if v := q.Get(key); v != "" {
				t, err := time.Parse(time.RFC3339Nano, v)
				if err != nil {
					return Request{}, false, dms.Invalid(key + " must be an RFC 3339 time")
				}
				*into = &t
			}
		}
	}
	out := Request{Q: b.Q, Mode: b.Mode, Surface: b.Surface, Kinds: b.Kind, WorkspaceID: b.WorkspaceID, CollectionID: b.CollectionID, ContentTypeID: b.ContentTypeID,
		TermID: b.TermID, Keyword: b.Keyword, CreatedBy: b.CreatedBy, ModifiedBy: b.ModifiedBy, Filter: b.Filter, Facets: b.Facets == nil || *b.Facets}
	if len(out.Kinds) == 0 {
		out.Kinds = []string{"item"}
	}
	if b.ModifiedFrom != nil {
		out.ModifiedFrom = *b.ModifiedFrom
	}
	if b.ModifiedTo != nil {
		out.ModifiedTo = *b.ModifiedTo
	}
	out.Limit = 25
	if q.Get("$top") != "" {
		top, err := strconv.Atoi(q.Get("$top"))
		if err != nil {
			top = 50
		}
		out.Limit = min(max(top, 1), MaxLimit)
	}
	return out, true, nil
}

// fingerprint binds a continuation to the caller and the search.
func fingerprint(subject string, in Request) string {
	in.Offset = 0
	raw, _ := json.Marshal(in)
	h := sha256.Sum256(append([]byte(subject+"\x00"), raw...))
	return hex.EncodeToString(h[:16])
}

func (m module) search(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	req, ok, err := readRequest(w, r, in)
	if err != nil || !ok {
		return err
	}
	bound := fingerprint(in.Subject, req)
	if token := r.URL.Query().Get("$skiptoken"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var c cursor
		if err == nil {
			err = json.Unmarshal(raw, &c)
		}
		if err != nil || c.Query != bound || c.Offset < 0 {
			return &httpapi.Problem{Status: http.StatusBadRequest, Code: "invalid_skiptoken", Detail: "the skip token does not belong to this search"}
		}
		req.Offset = c.Offset
		if c.Mode != "" {
			req.Mode = c.Mode
		}
	}
	out, err := m.x.Search(r.Context(), in.DMS, in.Subject, req)
	if err != nil {
		return err
	}
	p := page{Result: out}
	if out.NextOffset >= 0 {
		raw, _ := json.Marshal(cursor{Offset: out.NextOffset, Query: bound, Mode: out.Mode})
		p.cursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	httpapi.Respond(w, r, http.StatusOK, p)
	return nil
}

// termBatch is how many entries a page of term entries lists.
const termBatch = 500

// TermEntries is a page of the resources holding terms.
type TermEntries struct {
	Items []string `json:"items"`
	// Next is the after of the next page, empty on the last.
	Next string `json:"next,omitempty"`
}

func (m module) termEntries(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	q := r.URL.Query()
	out, err := Holders(r.Context(), in.DMS, in.Subject, r.PathValue("id"), q.Get("collection_id"), q["term_id"], q.Get("after"))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// Holders lists, 500 at a time, the resources of a workspace, or of one of
// its collections, holding any of terms or the terms merged into them,
// whose search entries a relabel or merge changes. The caller needs manage
// access to the workspace.
func Holders(ctx context.Context, s *dms.Service, subject, workspaceID, collectionID string, terms []string, after string) (TermEntries, error) {
	if len(terms) == 0 || len(terms) > 20 {
		return TermEntries{}, dms.Invalid("give 1 to 20 term_id values")
	}
	if _, err := s.Authorize(ctx, subject, workspaceID, "manage"); err != nil {
		return TermEntries{}, err
	}
	// Retained values keep merged source IDs; their words come from the
	// target, so their entries change too.
	var aliases []string
	for _, id := range terms {
		expanded, err := s.TermAliases(ctx, id)
		if err != nil {
			return TermEntries{}, err
		}
		for _, alias := range expanded {
			if !slices.Contains(aliases, alias) {
				aliases = append(aliases, alias)
			}
		}
	}
	items, err := s.EntriesWithTerms(ctx, workspaceID, collectionID, nil, aliases, after, termBatch)
	if err != nil {
		return TermEntries{}, err
	}
	out := TermEntries{Items: items}
	if out.Items == nil {
		out.Items = []string{}
	}
	if len(items) == termBatch {
		out.Next = items[len(items)-1]
	}
	return out, nil
}
