package documentsearch

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"papergo/internal/dal/refs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/search"
)

// Request is a search across the workspaces the caller may read.
type Request struct {
	Archive                  refs.ArchiveSelection
	ArchivedBy               string
	ArchivedFrom, ArchivedTo time.Time
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
	// read (dms.SearchFields), in the collection query filter language.
	Filter *dms.FilterExpr
	// Offset is a position in the store's results (SearchResult.NextOffset).
	Offset int
	Limit  int
	Facets bool
	// vector is the embedding of Q by model, for semantic and hybrid
	// searches; embedded before the read transaction.
	vector []float32
	model  string
}

// Hit is an entry the caller may read that matches.
type Hit struct {
	Archived      bool       `json:"archived"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	ArchivedBy    string     `json:"archived_by,omitempty"`
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Surface       string     `json:"surface"`
	WorkspaceID   string     `json:"workspace_id"`
	CollectionID  string     `json:"collection_id"`
	ContentTypeID string     `json:"content_type_id,omitempty"`
	Name          string     `json:"name"`
	Snippet       string     `json:"snippet,omitempty"`
	// Page is the page of the file the snippet is on, when the file's text
	// matched.
	Page       int       `json:"page,omitempty"`
	Highlights [][2]int  `json:"highlights,omitempty"`
	MatchedBy  []string  `json:"matched_by"`
	Rank       float64   `json:"rank"`
	ModifiedAt time.Time `json:"modified_at"`
	ModifiedBy string    `json:"modified_by"`
}

// Result is a page of hits. Total counts the store's matches;
// TotalEstimated reports a bounded candidate set or entries left out because
// the caller may no longer read them as indexed. NextOffset continues, or is -1.
type Result struct {
	Hits           []Hit          `json:"value"`
	Total          int            `json:"total"`
	TotalEstimated bool           `json:"total_estimated,omitempty"`
	Facets         *search.Facets `json:"facets,omitempty"`
	Mode           string         `json:"mode"`
	NextOffset     int            `json:"-"`
}

// MaxLimit bounds a page of search hits.
const MaxLimit = 100

// searchRounds is how many times a page asks the store for more when hits
// were left out.
const searchRounds = 3

// Search finds the entries matching a request among those the caller may
// read. The store trims by permission scope; every hit is then checked
// against the database, so an index that lags never shows what the caller
// may no longer read.
func (x *Search) Search(ctx context.Context, s *dms.Service, subject string, in Request) (Result, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return Result{}, err
	}
	if x.rebuilding.Load() {
		return Result{}, errUnavailable
	}
	if err := x.embedSearch(ctx, &in); err != nil {
		return Result{}, err
	}
	// The store is asked outside any transaction, so a slow search never
	// holds a database connection; every hit is checked against the
	// database as it is when the check runs.
	return x.search(ctx, s, subject, in)
}

// errUnavailable answers searches while the store is rebuilt.
var errUnavailable = &httpapi.Problem{Status: 503, Code: "search_unavailable", Detail: "Search is unavailable while the search index is rebuilt; try again shortly."}

func (x *Search) search(ctx context.Context, s *dms.Service, subject string, in Request) (Result, error) {
	if !in.Archive.Valid() {
		return Result{}, dms.Invalid("archive must be active, archived or all")
	}
	in.Archive = in.Archive.Default()
	if !in.ArchivedFrom.IsZero() && !in.ArchivedTo.IsZero() && in.ArchivedFrom.After(in.ArchivedTo) {
		return Result{}, dms.Invalid("archived_from must not follow archived_to")
	}
	if in.Mode != search.ModeKeyword && in.vector == nil {
		in.Mode = search.ModeKeyword
	}
	if in.Surface == "" {
		in.Surface = search.SurfaceAuto
	}
	if in.Surface != search.SurfaceAuto && in.Surface != search.SurfaceHead && in.Surface != search.SurfacePublished {
		return Result{}, dms.Invalid("surface must be auto, head or published")
	}
	for _, k := range in.Kinds {
		if k != "item" && k != "folder" {
			return Result{}, dms.Invalid("kind must be item or folder")
		}
	}
	if in.Limit <= 0 {
		in.Limit = 25
	}
	if in.Limit > MaxLimit {
		in.Limit = MaxLimit
	}
	if in.Offset < 0 {
		return Result{}, dms.Invalid("invalid page position")
	}
	var keywordErr error
	in.Keyword, keywordErr = s.CanonicalKeyword(ctx, in.Keyword)
	if keywordErr != nil {
		return Result{}, keywordErr
	}
	q := search.Query{Mode: in.Mode, Vector: in.vector, Model: in.model, Filter: search.Filter{Archive: in.Archive, ArchivedBy: in.ArchivedBy, ArchivedFrom: in.ArchivedFrom, ArchivedTo: in.ArchivedTo, Surface: in.Surface, Kinds: in.Kinds, WorkspaceID: in.WorkspaceID, CollectionID: in.CollectionID, ContentTypeID: in.ContentTypeID,
		Keyword: in.Keyword, CreatedBy: in.CreatedBy, ModifiedBy: in.ModifiedBy, ModifiedFrom: in.ModifiedFrom, ModifiedTo: in.ModifiedTo}, Facets: in.Facets}
	if strings.TrimSpace(in.Q) != "" {
		var err error
		// A semantic search needs no keyword syntax: exclusions alone are
		// meaning enough.
		if q.Text, err = search.Parse(in.Q); err != nil && in.Mode != search.ModeSemantic {
			return Result{}, dms.Invalid(err.Error())
		}
	} else if in.Archive != refs.ArchiveArchived && in.WorkspaceID == "" && in.CollectionID == "" && in.ContentTypeID == "" && in.TermID == "" && in.Keyword == "" && in.CreatedBy == "" && in.ModifiedBy == "" && in.Filter == nil {
		return Result{}, dms.Invalid("enter words to search for, or at least one filter")
	}
	if in.TermID != "" {
		terms, err := s.TermFamily(ctx, in.TermID)
		if err != nil {
			return Result{}, err
		}
		if len(terms) == 0 {
			terms = []string{in.TermID}
		}
		q.Filter.TermIDs = terms
	}
	if in.Filter != nil {
		fields, err := s.CompileIndexFilter(ctx, subject, in.WorkspaceID, in.CollectionID, in.Filter)
		if err != nil {
			return Result{}, err
		}
		q.Filter.Fields = fieldFilter(fields)
	}
	access, err := scopes(ctx, s, subject)
	if err != nil {
		return Result{}, err
	}
	q.Filter.Access = access
	if q.Content, err = contentAllowed(ctx, s, subject); err != nil {
		return Result{}, err
	}
	q.VerifyFacets = func(ctx context.Context, keys []search.Key) (bool, error) {
		shown, err := visible(ctx, s, subject, in.Surface, keys, in.Archive)
		if err != nil {
			return false, err
		}
		for _, key := range keys {
			if !shown[key] {
				return false, nil
			}
		}
		return true, nil
	}
	out := Result{Hits: []Hit{}, Mode: in.Mode, NextOffset: -1}
	var tokens []string
	if q.Text != nil {
		tokens = q.Text.Positive()
	}
	offset := in.Offset
	for round := 0; round < searchRounds && len(out.Hits) < in.Limit; round++ {
		q.Offset, q.Limit = offset, in.Limit-len(out.Hits)
		q.Facets = in.Facets && round == 0
		res, err := x.Store.Search(ctx, q)
		if err != nil {
			return Result{}, err
		}
		if round == 0 {
			out.Total, out.Facets = res.Total, res.Facets
			out.TotalEstimated = res.Estimated || in.Facets && res.Facets == nil
		}
		keys := make([]search.Key, len(res.Hits))
		for i, h := range res.Hits {
			keys[i] = search.Key{ResourceID: h.ResourceID, Surface: h.Surface}
		}
		shown, err := visible(ctx, s, subject, in.Surface, keys, in.Archive)
		if err != nil {
			return Result{}, err
		}
		for i, h := range res.Hits {
			offset++
			if !shown[keys[i]] {
				out.TotalEstimated = true
				continue
			}
			hit := Hit{Archived: h.Archived, ArchivedAt: h.ArchivedAt, ArchivedBy: h.ArchivedBy, ID: h.ResourceID, Kind: h.Kind, Surface: h.Surface, WorkspaceID: h.WorkspaceID, CollectionID: h.CollectionID, ContentTypeID: h.ContentTypeID,
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
func (x *Search) embedSearch(ctx context.Context, in *Request) error {
	words := strings.TrimSpace(in.Q) != ""
	switch in.Mode {
	case "":
		in.Mode = search.ModeKeyword
		if x.Embeddings != nil && words {
			in.Mode = search.ModeHybrid
		}
	case search.ModeKeyword:
	case search.ModeSemantic, search.ModeHybrid:
		if x.Embeddings == nil {
			return dms.Invalid("mode: semantic and hybrid search need an embedding model (AI_EMBEDDINGS_PROVIDER)")
		}
		if !words {
			return dms.Invalid("mode: semantic and hybrid search need words to search for")
		}
	default:
		return dms.Invalid("mode must be keyword, semantic or hybrid")
	}
	if in.Mode == search.ModeKeyword {
		return nil
	}
	if len(in.Q) > search.MaxQueryBytes {
		return dms.Invalid("a search is at most 256 bytes")
	}
	model, err := x.Embeddings.Key(ctx)
	if err == nil {
		in.vector, err = x.Embeddings.EmbedQuery(ctx, search.SemanticText(in.Q))
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

// fieldFilter is a compiled field filter in the store's terms.
func fieldFilter(f *dms.IndexFilter) *search.FieldFilter {
	if f == nil {
		return nil
	}
	out := &search.FieldFilter{Key: f.Key, Type: f.Type, Scale: f.Scale, Op: f.Op, Not: fieldFilter(f.Not)}
	for _, v := range f.Values {
		out.Values = append(out.Values, search.FieldValue{Text: v.Text, Int: v.Int})
	}
	for i := range f.And {
		out.And = append(out.And, *fieldFilter(&f.And[i]))
	}
	for i := range f.Or {
		out.Or = append(out.Or, *fieldFilter(&f.Or[i]))
	}
	return out
}

// scopes decides, once per permission scope of the organization's items
// and folders, whether the subject reads drafts there or published content:
// what search trims to. Permissions depend only on a row's scope, its
// workspace and whether it is content, so one live row stands for each
// scope, as in collection queries.
func scopes(ctx context.Context, s *dms.Service, subject string) (search.Access, error) {
	draft, readable, err := s.DAL.Content().SearchScopes(ctx, dms.Principal(ctx, subject))
	return search.Access{Draft: draft, Read: readable}, err
}

// visible reports which documents the subject may be shown now, on the
// surface asked, whatever the search store holds: entries that are gone, no
// longer readable, read on the other surface, or outside the archive
// selection are left out.
func visible(ctx context.Context, s *dms.Service, subject, surface string, keys []search.Key, archive refs.ArchiveSelection) (map[search.Key]bool, error) {
	out := map[search.Key]bool{}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ResourceID)
	}
	states, err := s.DAL.Content().SearchStates(ctx, dms.Principal(ctx, subject), ids)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		st, ok := states[k.ResourceID]
		if !ok {
			continue
		}
		if !archive.Matches(st.Archived) {
			continue
		}
		head := k.Surface == search.SurfaceHead
		if head && !st.Head || !head && !st.Published {
			continue
		}
		switch surface {
		case search.SurfaceHead:
			// Without publishing the published surface holds the head.
			out[k] = st.Draft && head == st.Publishes
		case search.SurfacePublished:
			out[k] = !head && (st.Read || st.Draft)
		default:
			out[k] = st.Draft && head == st.Publishes || !st.Draft && st.Read && !head
		}
	}
	return out, nil
}
