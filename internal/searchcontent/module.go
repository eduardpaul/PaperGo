package searchcontent

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"papergo/internal/dal/dberr"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

// Module serves the search content routes.
func (x *Indexer) Module() httpapi.Module { return module{x} }

type module struct{ x *Indexer }

func (module) Name() string { return "searchcontent" }

func (m module) Routes() []httpapi.Route {
	return []httpapi.Route{
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

// SyncResult counts the search documents a sync wrote and removed.
type SyncResult struct {
	Written int `json:"written"`
	Removed int `json:"removed"`
}

func (m module) sync(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	var request struct {
		ResourceIDs []string `json:"resource_ids"`
	}
	if !in.Decode(w, r, &request) {
		return nil
	}
	out, err := Sync(r.Context(), in.DMS, in.Subject, r.PathValue("id"), request.ResourceIDs)
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, out)
	return nil
}

// Sync brings the search documents of resources of a workspace to their
// current state: written for those that exist, removed for those that are
// gone. The caller needs read access to the workspace; a resource of
// another workspace is refused. Syncing again is harmless.
func Sync(ctx context.Context, s *dms.Service, subject, workspaceID string, ids []string) (SyncResult, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return SyncResult{}, dms.Invalid("give 1 to 500 resource_ids")
	}
	if _, err := s.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return SyncResult{}, err
	}
	for _, id := range ids {
		r, err := s.DAL.Resources().Resource(ctx, id)
		if dberr.IsNotFound(err) {
			continue
		}
		if err != nil {
			return SyncResult{}, err
		}
		if r.WorkspaceID != workspaceID {
			return SyncResult{}, dms.Invalid("resource " + id + " is not in the workspace")
		}
	}
	written, removed, err := s.SyncSearch(ctx, ids)
	return SyncResult{Written: written, Removed: removed}, err
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
		expanded, err := s.SearchTermAliases(ctx, id)
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
