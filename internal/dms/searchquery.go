package dms

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/search"
)

// SearchRequest is a search across the workspaces the caller may read.
type SearchRequest struct {
	// Q uses search syntax (see search.Parse); empty lists the filtered
	// entries, newest first.
	Q string
	// Mode is keyword, semantic or hybrid; empty is hybrid when an embedding
	// model is configured and words are given, else keyword.
	Mode          string
	Surface       string
	Kinds         []string
	WorkspaceID   string
	CollectionID  string
	ContentTypeID string
	// TermID matches the term, its descendants and terms merged into them.
	TermID       string
	Keyword      string
	CreatedBy    string
	ModifiedBy   string
	ModifiedFrom time.Time
	ModifiedTo   time.Time
	// Filter filters on fields of the lists and libraries the caller may
	// read (SearchFields), in the collection query filter language.
	Filter *FilterExpr
	// Offset is a position in the store's results (SearchResult.NextOffset).
	Offset int
	Limit  int
	Facets bool
	// vector is the embedding of Q by model, for semantic and hybrid
	// searches; embedded before the read transaction.
	vector []float32
	model  string
}

// SearchHit is an entry the caller may read that matches.
type SearchHit struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Surface       string `json:"surface"`
	WorkspaceID   string `json:"workspace_id"`
	CollectionID  string `json:"collection_id"`
	ContentTypeID string `json:"content_type_id,omitempty"`
	Name          string `json:"name"`
	Snippet       string `json:"snippet,omitempty"`
	// Page is the page of the file the snippet is on, when the file's text
	// matched.
	Page       int       `json:"page,omitempty"`
	Highlights [][2]int  `json:"highlights,omitempty"`
	MatchedBy  []string  `json:"matched_by"`
	Rank       float64   `json:"rank"`
	ModifiedAt time.Time `json:"modified_at"`
	ModifiedBy string    `json:"modified_by"`
}

// SearchResult is a page of hits. Total counts the store's matches;
// TotalEstimated reports a bounded candidate set or entries left out because
// the caller may no longer read them as indexed. NextOffset continues, or is -1.
type SearchResult struct {
	Hits           []SearchHit    `json:"value"`
	Total          int            `json:"total"`
	TotalEstimated bool           `json:"total_estimated,omitempty"`
	Facets         *search.Facets `json:"facets,omitempty"`
	Mode           string         `json:"mode"`
	NextOffset     int            `json:"-"`
}

// MaxSearchLimit bounds a page of search hits.
const MaxSearchLimit = 100

// searchRounds is how many times a page asks the store for more when hits
// were left out.
const searchRounds = 3

// Search finds the entries matching a request among those the caller may
// read. The store trims by permission scope; every hit is then checked
// against the database, so an index that lags never shows what the caller
// may no longer read.
func (s *Service) Search(ctx context.Context, subject string, in SearchRequest) (SearchResult, error) {
	if !s.transaction {
		if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
			return SearchResult{}, err
		}
		if err := s.embedSearch(ctx, &in); err != nil {
			return SearchResult{}, err
		}
		return read(ctx, s, func(t *Service) (SearchResult, error) { return t.Search(ctx, subject, in) })
	}
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return SearchResult{}, err
	}
	if s.search == nil || s.searchRebuild.Load() {
		return SearchResult{}, ErrSearchUnavailable
	}
	if in.Mode == "" || in.Mode != search.ModeKeyword && in.vector == nil {
		// Only the outer call embeds: inside a transaction, search by words.
		in.Mode = search.ModeKeyword
	}
	if in.Surface == "" {
		in.Surface = search.SurfaceAuto
	}
	if in.Surface != search.SurfaceAuto && in.Surface != search.SurfaceHead && in.Surface != search.SurfacePublished {
		return SearchResult{}, invalid("surface must be auto, head or published")
	}
	for _, k := range in.Kinds {
		if k != "item" && k != "folder" {
			return SearchResult{}, invalid("kind must be item or folder")
		}
	}
	if in.Limit <= 0 {
		in.Limit = 25
	}
	if in.Limit > MaxSearchLimit {
		in.Limit = MaxSearchLimit
	}
	if in.Offset < 0 {
		return SearchResult{}, invalid("invalid page position")
	}
	var keywordErr error
	in.Keyword, keywordErr = s.keywordFilter(ctx, in.Keyword)
	if keywordErr != nil {
		return SearchResult{}, keywordErr
	}
	q := search.Query{Mode: in.Mode, Vector: in.vector, Model: in.model, Filter: search.Filter{Surface: in.Surface, Kinds: in.Kinds, WorkspaceID: in.WorkspaceID, CollectionID: in.CollectionID, ContentTypeID: in.ContentTypeID,
		Keyword: in.Keyword, CreatedBy: in.CreatedBy, ModifiedBy: in.ModifiedBy, ModifiedFrom: in.ModifiedFrom, ModifiedTo: in.ModifiedTo}, Facets: in.Facets}
	if strings.TrimSpace(in.Q) != "" {
		var err error
		// A semantic search needs no keyword syntax: exclusions alone are
		// meaning enough.
		if q.Text, err = search.Parse(in.Q); err != nil && in.Mode != search.ModeSemantic {
			return SearchResult{}, invalid(err.Error())
		}
	} else if in.WorkspaceID == "" && in.CollectionID == "" && in.ContentTypeID == "" && in.TermID == "" && in.Keyword == "" && in.CreatedBy == "" && in.ModifiedBy == "" && in.Filter == nil {
		return SearchResult{}, invalid("enter words to search for, or at least one filter")
	}
	if in.TermID != "" {
		terms, err := s.SearchTerms(ctx, in.TermID)
		if err != nil {
			return SearchResult{}, err
		}
		if len(terms) == 0 {
			terms = []string{in.TermID}
		}
		q.Filter.TermIDs = terms
	}
	if in.Filter != nil {
		fields, err := read(ctx, s, func(t *Service) (*search.FieldFilter, error) {
			defs, err := t.searchFieldDefinitions(ctx, subject, in.WorkspaceID, in.CollectionID)
			if err != nil {
				return nil, err
			}
			nodes := 0
			return t.searchFilter(ctx, in.Filter, defs, 0, &nodes)
		})
		if err != nil {
			return SearchResult{}, err
		}
		q.Filter.Fields = fields
	}
	access, err := s.SearchScopes(ctx, subject)
	if err != nil {
		return SearchResult{}, err
	}
	q.Filter.Access = access
	if q.Content, err = s.searchContentAllowed(ctx, subject); err != nil {
		return SearchResult{}, err
	}
	q.VerifyFacets = func(ctx context.Context, keys []search.Key) (bool, error) {
		visible, err := s.SearchVisible(ctx, subject, in.Surface, keys)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			if !visible[key] {
				return false, nil
			}
		}
		return true, nil
	}
	out := SearchResult{Hits: []SearchHit{}, Mode: in.Mode, NextOffset: -1}
	var tokens []string
	if q.Text != nil {
		tokens = q.Text.Positive()
	}
	offset := in.Offset
	for round := 0; round < searchRounds && len(out.Hits) < in.Limit; round++ {
		q.Offset, q.Limit = offset, in.Limit-len(out.Hits)
		q.Facets = in.Facets && round == 0
		res, err := s.search.Search(ctx, q)
		if err != nil {
			return SearchResult{}, err
		}
		if round == 0 {
			out.Total, out.Facets = res.Total, res.Facets
			out.TotalEstimated = res.Estimated || in.Facets && res.Facets == nil
		}
		keys := make([]search.Key, len(res.Hits))
		for i, h := range res.Hits {
			keys[i] = search.Key{ResourceID: h.ResourceID, Surface: h.Surface}
		}
		visible, err := s.SearchVisible(ctx, subject, in.Surface, keys)
		if err != nil {
			return SearchResult{}, err
		}
		for i, h := range res.Hits {
			offset++
			if !visible[keys[i]] {
				out.TotalEstimated = true
				continue
			}
			hit := SearchHit{ID: h.ResourceID, Kind: h.Kind, Surface: h.Surface, WorkspaceID: h.WorkspaceID, CollectionID: h.CollectionID, ContentTypeID: h.ContentTypeID,
				Name: h.Name, MatchedBy: h.MatchedBy, Rank: h.Rank, ModifiedAt: h.ModifiedAt, ModifiedBy: h.ModifiedBy, Page: h.Page}
			if hit.MatchedBy == nil {
				hit.MatchedBy = []string{}
			}
			hit.Snippet, hit.Highlights = snippet(h.Text, tokens)
			out.Hits = append(out.Hits, hit)
		}
		if offset >= res.Total || len(res.Hits) == 0 {
			return out, nil
		}
	}
	if offset < out.Total {
		out.NextOffset = offset
	}
	return out, nil
}

// embedSearch settles the request's mode and, for semantic and hybrid
// searches, embeds its words. A semantic or hybrid search without an
// embedding model is invalid; an embedding failure falls back to keyword.
func (s *Service) embedSearch(ctx context.Context, in *SearchRequest) error {
	words := strings.TrimSpace(in.Q) != ""
	switch in.Mode {
	case "":
		in.Mode = search.ModeKeyword
		if s.embeddings != nil && words {
			in.Mode = search.ModeHybrid
		}
	case search.ModeKeyword:
	case search.ModeSemantic, search.ModeHybrid:
		if s.embeddings == nil {
			return invalid("mode: semantic and hybrid search need an embedding model (AI_EMBEDDINGS_PROVIDER)")
		}
		if !words {
			return invalid("mode: semantic and hybrid search need words to search for")
		}
	default:
		return invalid("mode must be keyword, semantic or hybrid")
	}
	if in.Mode == search.ModeKeyword {
		return nil
	}
	if len(in.Q) > search.MaxQueryBytes {
		return invalid("a search is at most 256 bytes")
	}
	model, err := s.embeddings.Key(ctx)
	if err == nil {
		in.vector, err = s.embeddings.EmbedQuery(ctx, search.SemanticText(in.Q))
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("search query embedding failed; searching by keyword", "error", err)
		in.Mode, in.vector = search.ModeKeyword, nil
		return nil
	}
	in.model = model
	return nil
}

// snippetRadius is how many characters a snippet keeps around the first
// match.
const snippetRadius = 80

// snippet cuts the text around its first matching token and marks every
// token in it: byte offsets into the snippet.
func snippet(text string, tokens []string) (string, [][2]int) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", nil
	}
	lower := strings.ToLower(text)
	first := -1
	for _, t := range tokens {
		if i := strings.Index(lower, t); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	start, end := 0, len(text)
	if first > snippetRadius {
		start = first - snippetRadius
	}
	if start+2*snippetRadius < end {
		end = start + 2*snippetRadius
	}
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	cut, prefix := text[start:end], ""
	if start > 0 {
		prefix = "…"
	}
	out := prefix + cut
	if end < len(text) {
		out += "…"
	}
	var marks [][2]int
	lowerCut := strings.ToLower(cut)
	for _, t := range tokens {
		for from := 0; ; {
			i := strings.Index(lowerCut[from:], t)
			if i < 0 || t == "" {
				break
			}
			at := len(prefix) + from + i
			marks = append(marks, [2]int{at, at + len(t)})
			from += i + len(t)
		}
	}
	return out, marks
}
