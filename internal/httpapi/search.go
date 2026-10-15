package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"papergo/internal/dms"
)

func (a *API) registerSearch(api *http.ServeMux) {
	api.HandleFunc("GET /v1/search", a.search)
	api.HandleFunc("POST /v1/search", a.search)
	api.HandleFunc("GET /v1/search/fields", a.searchFields)
	api.HandleFunc("POST /v1/search/reindex", a.reindexSearch)
	api.HandleFunc("GET /v1/search/status", a.searchStatus)
}

// searchPage is a page of search results with its continuation.
type searchPage struct {
	dms.SearchResult
	cursor string
}

func (p searchPage) APIPage() (map[string]any, string) {
	out := map[string]any{"value": p.Hits, "total": p.Total, "mode": p.Mode}
	if p.TotalEstimated {
		out["total_estimated"] = true
	}
	if p.Facets != nil {
		out["facets"] = p.Facets
	}
	return out, p.cursor
}

// searchCursor continues a search: a position in the store's results, bound
// to the caller and the search, so it cannot page another search.
type searchCursor struct {
	Offset int    `json:"o"`
	Query  string `json:"q"`
	// Mode is the mode the first page ran in, so later pages page the same
	// results.
	Mode string `json:"m,omitempty"`
}

// searchBody is the JSON body of POST /v1/search: the parameters of GET
// /v1/search, and a filter on search fields. $top and $skiptoken stay in
// the URL.
type searchBody struct {
	Q             string          `json:"q"`
	Mode          string          `json:"mode"`
	Surface       string          `json:"surface"`
	Kind          []string        `json:"kind"`
	WorkspaceID   string          `json:"workspace_id"`
	CollectionID  string          `json:"collection_id"`
	ContentTypeID string          `json:"content_type_id"`
	TermID        string          `json:"term_id"`
	Tag           string          `json:"tag"`
	CreatedBy     string          `json:"created_by"`
	ModifiedBy    string          `json:"modified_by"`
	ModifiedFrom  *time.Time      `json:"modified_from"`
	ModifiedTo    *time.Time      `json:"modified_to"`
	Facets        *bool           `json:"facets"`
	Filter        *dms.FilterExpr `json:"filter"`
}

// searchRequest reads a search from the URL (GET) or the body (POST).
func (a *API) searchRequest(w http.ResponseWriter, r *http.Request) (dms.SearchRequest, bool) {
	var b searchBody
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		if !a.decode(w, r, &b) {
			return dms.SearchRequest{}, false
		}
	} else {
		b = searchBody{Q: q.Get("q"), Mode: q.Get("mode"), Surface: q.Get("surface"), WorkspaceID: q.Get("workspace_id"), CollectionID: q.Get("collection_id"), ContentTypeID: q.Get("content_type_id"),
			TermID: q.Get("term_id"), Tag: q.Get("tag"), CreatedBy: q.Get("created_by"), ModifiedBy: q.Get("modified_by")}
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
					a.problem(w, r, 422, "validation_failed", key+" must be an RFC 3339 time")
					return dms.SearchRequest{}, false
				}
				*into = &t
			}
		}
	}
	in := dms.SearchRequest{Q: b.Q, Mode: b.Mode, Surface: b.Surface, Kinds: b.Kind, WorkspaceID: b.WorkspaceID, CollectionID: b.CollectionID, ContentTypeID: b.ContentTypeID,
		TermID: b.TermID, Tag: b.Tag, CreatedBy: b.CreatedBy, ModifiedBy: b.ModifiedBy, Filter: b.Filter, Facets: b.Facets == nil || *b.Facets}
	if len(in.Kinds) == 0 {
		in.Kinds = []string{"item"}
	}
	if b.ModifiedFrom != nil {
		in.ModifiedFrom = *b.ModifiedFrom
	}
	if b.ModifiedTo != nil {
		in.ModifiedTo = *b.ModifiedTo
	}
	in.Limit = 25
	if q.Get("$top") != "" {
		in.Limit = min(queryInt(r, "$top"), dms.MaxSearchLimit)
	}
	return in, true
}

// searchFingerprint binds a continuation to the caller and the search.
func searchFingerprint(subject string, in dms.SearchRequest) string {
	in.Offset = 0
	raw, _ := json.Marshal(in)
	h := sha256.Sum256(append([]byte(subject+"\x00"), raw...))
	return hex.EncodeToString(h[:16])
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	in, ok := a.searchRequest(w, r)
	if !ok {
		return
	}
	fingerprint := searchFingerprint(subject(r), in)
	if token := r.URL.Query().Get("$skiptoken"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var c searchCursor
		if err == nil {
			err = json.Unmarshal(raw, &c)
		}
		if err != nil || c.Query != fingerprint || c.Offset < 0 {
			a.problem(w, r, 400, "invalid_skiptoken", "the skip token does not belong to this search")
			return
		}
		in.Offset = c.Offset
		if c.Mode != "" {
			in.Mode = c.Mode
		}
	}
	out, err := a.DMS.Search(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	page := searchPage{SearchResult: out}
	if out.NextOffset >= 0 {
		raw, _ := json.Marshal(searchCursor{Offset: out.NextOffset, Query: fingerprint, Mode: out.Mode})
		page.cursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	respond(w, r, 200, page)
}

// searchFields lists the fields search can filter on for the caller.
func (a *API) searchFields(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.DMS.SearchFields(r.Context(), subject(r), q.Get("workspace_id"), q.Get("collection_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out})
}

// reindexSearch rebuilds the search index from every item and folder, as an
// organization operation; with {"content": true} it also has file content
// indexed again.
func (a *API) reindexSearch(w http.ResponseWriter, r *http.Request) {
	var in dms.SearchReindex
	if r.ContentLength != 0 && !a.decode(w, r, &in) {
		return
	}
	op, err := a.DMS.StartSearchReindex(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/operations/"+op.ID)
	respond(w, r, 202, op)
}

func (a *API) searchStatus(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.SearchStatus(r.Context(), subject(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
