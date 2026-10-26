package sqlitestore

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"papergo/internal/dal/refs"
	"papergo/internal/search"
)

// columns are the FTS5 columns a query searches: the words themselves and
// their stems.
type columns struct{ words, stems string }

var (
	docColumns      = columns{"{heading body file_text}", "{heading_stem body_stem file_stem}"}
	metadataColumns = columns{"{heading body}", "{heading_stem body_stem}"}
	chunkColumns    = columns{"{text}", "{text_stem}"}
)

// render writes a parsed query in FTS5 syntax over the document columns.
// Tokens are letters and digits only, so quoting them needs no escaping.
// With a stemmer, a whole word or phrase also matches its stems in any of
// the stemmer's languages; prefixes match words only.
func render(q *search.FullTextQuery, stem *search.Stemmer) string {
	return renderOn(q, stem, docColumns)
}

func renderOn(q *search.FullTextQuery, stem *search.Stemmer, cols columns) string {
	groups := make([]string, len(q.Groups))
	for i, group := range q.Groups {
		terms := make([]string, len(group))
		for j, t := range group {
			terms[j] = term(t, stem, cols)
		}
		groups[i] = strings.Join(terms, " OR ")
		if len(terms) > 1 {
			groups[i] = "(" + groups[i] + ")"
		}
	}
	positive := strings.Join(groups, " AND ")
	if len(q.Excluded) == 0 {
		return positive
	}
	excluded := make([]string, len(q.Excluded))
	for i, t := range q.Excluded {
		excluded[i] = term(t, stem, cols)
	}
	return "(" + positive + ") NOT (" + strings.Join(excluded, " OR ") + ")"
}

func term(t search.Term, stem *search.Stemmer, cols columns) string {
	exact := cols.words + ` : "` + strings.Join(t.Tokens, " ") + `"`
	if t.Prefix {
		return exact + "*"
	}
	if stem == nil || len(stem.Languages()) == 0 {
		return exact
	}
	// One phrase of stems per language, each once.
	phrases := make([][]string, len(stem.Languages()))
	for _, token := range t.Tokens {
		for l, s := range stem.Stem(token) {
			phrases[l] = append(phrases[l], s)
		}
	}
	seen := map[string]bool{}
	var alternatives []string
	for _, p := range phrases {
		phrase := `"` + strings.Join(p, " ") + `"`
		if !seen[phrase] {
			seen[phrase] = true
			alternatives = append(alternatives, phrase)
		}
	}
	return "(" + exact + " OR " + cols.stems + " : (" + strings.Join(alternatives, " OR ") + "))"
}

// where builds the filter of a search over documents d: what the caller may
// read on the requested surface, then the narrowing filters.
func where(f search.Filter) (string, []any, error) {
	draft, read := jsonList(f.Access.Draft), jsonList(f.Access.Read)
	var conds []string
	var args []any
	switch f.Surface {
	case search.SurfaceHead:
		conds = append(conds, "(d.views & 1) <> 0 AND d.scope_id IN (SELECT value FROM json_each(?))")
		args = append(args, draft)
	case search.SurfacePublished:
		conds = append(conds, "(d.views & 2) <> 0 AND (d.scope_id IN (SELECT value FROM json_each(?)) OR d.scope_id IN (SELECT value FROM json_each(?)))")
		args = append(args, read, draft)
	default:
		// Drafts where the caller reads them, else what is published.
		conds = append(conds, "((d.views & 1) <> 0 AND d.scope_id IN (SELECT value FROM json_each(?)) OR (d.views & 2) <> 0 AND d.scope_id IN (SELECT value FROM json_each(?)) AND d.scope_id NOT IN (SELECT value FROM json_each(?)))")
		args = append(args, draft, read, draft)
	}
	if !f.Archive.Valid() {
		return "", nil, errors.New("invalid archive selector")
	}
	if f.Archive == refs.ArchiveActive {
		conds = append(conds, "NOT d.archived")
	}
	if f.Archive == refs.ArchiveArchived {
		conds = append(conds, "d.archived")
	}
	if f.ArchivedBy != "" {
		conds = append(conds, "d.archived_by=?")
		args = append(args, f.ArchivedBy)
	}
	if !f.ArchivedFrom.IsZero() {
		conds = append(conds, "d.archived_at>=?")
		args = append(args, f.ArchivedFrom.UnixNano())
	}
	if !f.ArchivedTo.IsZero() {
		conds = append(conds, "d.archived_at<=?")
		args = append(args, f.ArchivedTo.UnixNano())
	}
	if len(f.Kinds) > 0 {
		conds = append(conds, "d.kind IN (SELECT value FROM json_each(?))")
		args = append(args, jsonList(f.Kinds))
	}
	for column, value := range map[string]string{"workspace_id": f.WorkspaceID, "collection_id": f.CollectionID, "created_by": f.CreatedBy, "modified_by": f.ModifiedBy} {
		if value != "" {
			conds = append(conds, "d."+column+"=?")
			args = append(args, value)
		}
	}
	if len(f.ContentTypeIDs) > 0 {
		conds = append(conds, "d.content_type_id IN (SELECT value FROM json_each(?))")
		args = append(args, jsonList(f.ContentTypeIDs))
	}
	if f.Within != nil {
		conds = append(conds, "(d.workspace_id IN (SELECT value FROM json_each(?)) OR d.collection_id IN (SELECT value FROM json_each(?)))")
		args = append(args, jsonList(f.Within.Workspaces), jsonList(f.Within.Collections))
	}
	if len(f.TermIDs) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM document_terms t WHERE t.doc=d.rowid AND t.term_id IN (SELECT value FROM json_each(?)))")
		args = append(args, jsonList(f.TermIDs))
	}
	if f.Keyword != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM document_keywords g WHERE g.doc=d.rowid AND g.keyword=?)")
		args = append(args, f.Keyword)
	}
	if !f.ModifiedFrom.IsZero() {
		conds = append(conds, "d.modified_at>=?")
		args = append(args, f.ModifiedFrom.UnixNano())
	}
	if !f.ModifiedTo.IsZero() {
		conds = append(conds, "d.modified_at<?")
		args = append(args, f.ModifiedTo.UnixNano())
	}
	if f.Fields != nil {
		nodes := 0
		text, a, err := fieldFilter(f.Fields, 0, &nodes)
		if err != nil {
			return "", nil, err
		}
		conds = append(conds, "("+text+")")
		args = append(args, a...)
	}
	return strings.Join(conds, " AND "), args, nil
}

// Field filters are bounded like collection query filters.
const (
	maxFilterNodes = 64
	maxFilterDepth = 8
)

// errFilter reports a field filter the store cannot run; the service
// validates filters first, so it means a bug.
var errFilter = errors.New("invalid field filter")

// fieldFilter compiles a field filter over documents d.
func fieldFilter(f *search.FieldFilter, depth int, nodes *int) (string, []any, error) {
	if *nodes++; *nodes > maxFilterNodes || depth > maxFilterDepth {
		return "", nil, errFilter
	}
	if f.Not != nil {
		text, args, err := fieldFilter(f.Not, depth+1, nodes)
		return "NOT (" + text + ")", args, err
	}
	if f.And != nil || f.Or != nil {
		list, op := f.And, " AND "
		if f.Or != nil {
			list, op = f.Or, " OR "
		}
		if len(list) == 0 {
			return "", nil, errFilter
		}
		parts := make([]string, len(list))
		var args []any
		for i := range list {
			text, a, err := fieldFilter(&list[i], depth+1, nodes)
			if err != nil {
				return "", nil, err
			}
			parts[i], args = "("+text+")", append(args, a...)
		}
		return strings.Join(parts, op), args, nil
	}
	held := "SELECT 1 FROM document_fields f WHERE f.doc=d.rowid AND f.key=? AND f.type=? AND f.scale=?"
	field := []any{f.Key, f.Type, f.Scale}
	column := "f.text"
	values := make([]any, len(f.Values))
	for i, v := range f.Values {
		values[i] = v.Text
	}
	if search.IntegerType(f.Type) {
		column = "f.int"
		for i, v := range f.Values {
			values[i] = v.Int
		}
	}
	switch f.Op {
	case "present", "missing":
		text := "EXISTS (" + held + ")"
		if f.Op == "missing" {
			text = "NOT " + text
		}
		return text, field, nil
	case "in", "ne":
		if len(values) == 0 {
			return "", nil, errFilter
		}
		list, _ := json.Marshal(values)
		member := "EXISTS (" + held + " AND " + column + " IN (SELECT value FROM json_each(?)))"
		if f.Op == "in" {
			return member, append(field, string(list)), nil
		}
		// Present, and no value equals: as collection queries read ne.
		return "EXISTS (" + held + ") AND NOT " + member, append(append(field, field...), string(list)), nil
	}
	operator := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[f.Op]
	if operator == "" || len(values) != 1 {
		return "", nil, errFilter
	}
	return "EXISTS (" + held + " AND " + column + operator + "?)", append(field, values[0]), nil
}

// The weights of doc_fts's columns: the name and keywords rank ten times
// higher than the body, and words that match as they are rank above words
// that match by their stems.
const rankSQL = "-bm25(doc_fts, 10.0, 1.0, 1.0, 5.0, 0.5, 0.5)"

func documentColumns(content bool) columns {
	if content {
		return docColumns
	}
	return metadataColumns
}

func (s *Store) Search(ctx context.Context, q search.Query) (search.Result, error) {
	var out search.Result
	filter, args, err := where(q.Filter)
	if err != nil {
		return out, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 25
	}
	switch q.Mode {
	case search.ModeSemantic, search.ModeHybrid:
		if len(q.Vector) == 0 || q.Model == "" {
			return out, errors.New("semantic and hybrid search need the query vector and its model")
		}
		err = s.read(ctx, func(tx *sql.Tx) error { return s.fused(ctx, tx, q, filter, args, limit, &out) })
		return out, err
	case "", search.ModeKeyword:
	default:
		return out, errors.New("unknown search mode " + q.Mode)
	}
	from, order := "documents d", "d.modified_at DESC, d.resource_id"
	rank := "0.0"
	if q.Text != nil {
		from = "doc_fts JOIN documents d ON d.rowid=doc_fts.rowid"
		filter = "doc_fts MATCH ? AND " + filter
		args = append([]any{renderOn(q.Text, s.stem, documentColumns(q.Content))}, args...)
		rank, order = rankSQL, "rank DESC, d.resource_id"
	}
	matches := "SELECT d.rowid AS doc, " + rank + " AS rank FROM " + from + " WHERE " + filter
	err = s.read(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM ("+matches+")", args...).Scan(&out.Total); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+hitColumns+`, m.rank FROM (`+matches+`) m JOIN documents d ON d.rowid=m.doc
 ORDER BY `+strings.ReplaceAll(order, "rank", "m.rank")+` LIMIT ? OFFSET ?`, append(args, limit, q.Offset)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var h search.Hit
			var rowid int64
			if err = scanHit(rows, &h, &rowid, &h.Rank); err != nil {
				break
			}
			if q.Text != nil {
				h.MatchedBy = []string{search.MatchedKeyword}
			}
			out.Hits = append(out.Hits, h)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
		if q.Text != nil && q.Content {
			if err = s.pageHits(ctx, tx, q.Text, out.Hits); err != nil {
				return err
			}
		}
		if q.Facets {
			verified, e := verifyFacets(ctx, tx, matches, args, q.VerifyFacets)
			if e != nil {
				return e
			}
			if verified {
				out.Facets, err = facets(ctx, tx, matches, args)
			}
		}
		return err
	})
	return out, err
}

// hitColumns are the document columns scanHit reads.
const hitColumns = "d.archived,d.archived_at,d.archived_by,d.rowid, d.resource_id, d.kind, d.surface, d.workspace_id, d.collection_id, d.content_type_id, d.revision_id, d.name, d.keywords, d.body, d.modified_by, d.modified_at"

func scanHit(rows *sql.Rows, h *search.Hit, rowid *int64, extra ...any) error {
	var keywords, body string
	var modified int64
	var archived sql.NullInt64
	err := rows.Scan(append([]any{&h.Archived, &archived, &h.ArchivedBy, rowid, &h.ResourceID, &h.Kind, &h.Surface, &h.WorkspaceID, &h.CollectionID, &h.ContentTypeID, &h.RevisionID, &h.Name, &keywords, &body, &h.ModifiedBy, &modified}, extra...)...)
	h.ArchivedAt = archiveTime(archived)
	h.ModifiedAt, h.Text = fromNanos(modified), body
	if h.Text == "" {
		h.Text = keywords
	}
	return err
}

// fused answers semantic and hybrid searches: the best CandidateLimit
// documents of each side, fused by reciprocal rank, as PaperDotNet's
// database store. Totals and facets cover the fused candidates.
func (s *Store) fused(ctx context.Context, tx *sql.Tx, q search.Query, filter string, args []any, limit int, out *search.Result) error {
	var keyword []int64
	if q.Mode == search.ModeHybrid && q.Text != nil {
		rows, err := tx.QueryContext(ctx, `SELECT d.rowid FROM doc_fts JOIN documents d ON d.rowid=doc_fts.rowid WHERE doc_fts MATCH ? AND `+filter+`
 ORDER BY `+rankSQL+` DESC, d.resource_id LIMIT ?`, append(append([]any{renderOn(q.Text, s.stem, documentColumns(q.Content))}, args...), search.CandidateLimit)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			keyword = append(keyword, id)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	semantic, err := s.semantic(ctx, tx, q, filter, args, search.CandidateLimit)
	if err != nil {
		return err
	}
	scores := map[int64]float64{}
	keywordIDs, semanticIDs := map[int64]bool{}, map[int64]semanticMatch{}
	for i, id := range keyword {
		scores[id] += 1 / float64(search.FusionK+i+1)
		keywordIDs[id] = true
	}
	for i, m := range semantic {
		scores[m.doc] += 1 / float64(search.FusionK+i+1)
		semanticIDs[m.doc] = m
	}
	ordered := make([]int64, 0, len(scores))
	for id := range scores {
		ordered = append(ordered, id)
	}
	slices.SortFunc(ordered, func(a, b int64) int {
		if c := cmp.Compare(scores[b], scores[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	out.Total = len(ordered)
	all, _ := json.Marshal(ordered)
	if q.Facets {
		candidates, candidateArgs := "SELECT value AS doc, 0 AS rank FROM json_each(?)", []any{string(all)}
		verified, err := verifyFacets(ctx, tx, candidates, candidateArgs, q.VerifyFacets)
		if err != nil {
			return err
		}
		if verified {
			if out.Facets, err = facets(ctx, tx, candidates, candidateArgs); err != nil {
				return err
			}
		}
	}
	page := ordered[min(q.Offset, len(ordered)):min(q.Offset+limit, len(ordered))]
	if len(page) == 0 {
		return nil
	}
	ids, _ := json.Marshal(page)
	rows, err := tx.QueryContext(ctx, `SELECT `+hitColumns+` FROM documents d WHERE d.rowid IN (SELECT value FROM json_each(?))`, string(ids))
	if err != nil {
		return err
	}
	byID := map[int64]search.Hit{}
	for rows.Next() {
		var h search.Hit
		var rowid int64
		if err = scanHit(rows, &h, &rowid); err != nil {
			break
		}
		byID[rowid] = h
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	var keywordHits []int
	for _, id := range page {
		h, ok := byID[id]
		if !ok {
			continue
		}
		h.Rank = scores[id]
		if keywordIDs[id] {
			h.MatchedBy = append(h.MatchedBy, search.MatchedKeyword)
			keywordHits = append(keywordHits, len(out.Hits))
		}
		if m, ok := semanticIDs[id]; ok {
			h.MatchedBy = append(h.MatchedBy, search.MatchedSemantic)
			// The most similar chunk, unless a chunk matches the words.
			if err = tx.QueryRowContext(ctx, `SELECT page, text FROM chunks WHERE rowid=?`, m.chunk).Scan(&h.Page, &h.Text); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		out.Hits = append(out.Hits, h)
	}
	if len(keywordHits) > 0 && q.Content {
		matched := make([]search.Hit, len(keywordHits))
		for i, at := range keywordHits {
			matched[i] = out.Hits[at]
		}
		if err = s.pageHits(ctx, tx, q.Text, matched); err != nil {
			return err
		}
		for i, at := range keywordHits {
			out.Hits[at] = matched[i]
		}
	}
	return nil
}

// verifyFacets checks all matching sources, including those outside the hit
// page. A single stale source makes the aggregates unsafe to expose.
func verifyFacets(ctx context.Context, tx *sql.Tx, matches string, args []any, verify func(context.Context, []search.Key) (bool, error)) (bool, error) {
	if verify == nil {
		return true, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.resource_id, d.surface FROM (`+matches+`) m JOIN documents d ON d.rowid=m.doc`, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	keys := make([]search.Key, 0, 200)
	for rows.Next() {
		var key search.Key
		if err = rows.Scan(&key.ResourceID, &key.Surface); err != nil {
			return false, err
		}
		keys = append(keys, key)
		if len(keys) == cap(keys) {
			if ok, err := verify(ctx, keys); err != nil || !ok {
				return false, err
			}
			keys = keys[:0]
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if len(keys) > 0 {
		return verify(ctx, keys)
	}
	return true, nil
}

// pageHits finds, for hits whose file matched, the best matching chunk of
// the file: its page, and the text to cut the snippet from.
func (s *Store) pageHits(ctx context.Context, tx *sql.Tx, q *search.FullTextQuery, hits []search.Hit) error {
	match := renderOn(q, s.stem, chunkColumns)
	for i, h := range hits {
		var page int
		var text string
		err := tx.QueryRowContext(ctx, `SELECT c.page, c.text FROM chunk_fts JOIN chunks c ON c.rowid=chunk_fts.rowid
 WHERE chunk_fts MATCH ? AND c.item_id=? AND c.published=1 AND c.blob_id=(SELECT blob_id FROM documents WHERE resource_id=? AND surface=?)
 ORDER BY bm25(chunk_fts, 1.0, 0.5) LIMIT 1`, match, h.ResourceID, h.ResourceID, h.Surface).Scan(&page, &text)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		hits[i].Page, hits[i].Text = page, text
	}
	return nil
}

// facets counts the matches by workspace, collection, content type, term
// and keyword.
func facets(ctx context.Context, tx *sql.Tx, matches string, args []any) (*search.Facets, error) {
	f := &search.Facets{}
	for _, facet := range []struct {
		into  *[]search.FacetValue
		query string
	}{
		{&f.Workspace, "SELECT d.workspace_id, count(*) FROM (" + matches + ") m JOIN documents d ON d.rowid=m.doc GROUP BY 1"},
		{&f.Collection, "SELECT d.collection_id, count(*) FROM (" + matches + ") m JOIN documents d ON d.rowid=m.doc WHERE d.collection_id<>'' GROUP BY 1"},
		{&f.ContentType, "SELECT d.content_type_id, count(*) FROM (" + matches + ") m JOIN documents d ON d.rowid=m.doc WHERE d.content_type_id<>'' GROUP BY 1"},
		{&f.Term, "SELECT t.term_id, count(*) FROM (" + matches + ") m JOIN document_terms t ON t.doc=m.doc GROUP BY 1"},
		{&f.Keyword, "SELECT g.keyword, count(*) FROM (" + matches + ") m JOIN document_keywords g ON g.doc=m.doc GROUP BY 1"},
	} {
		rows, err := tx.QueryContext(ctx, facet.query+" ORDER BY 2 DESC, 1 LIMIT ?", append(args, search.FacetSize)...)
		if err != nil {
			return nil, err
		}
		*facet.into = []search.FacetValue{}
		for rows.Next() {
			var v search.FacetValue
			if err = rows.Scan(&v.Value, &v.Count); err != nil {
				break
			}
			*facet.into = append(*facet.into, v)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (s *Store) Match(ctx context.Context, q search.MatchQuery) (search.Matches, error) {
	var out search.Matches
	if q.Text == nil {
		return out, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10_000
	}
	scope, value := "d.collection_id=?", q.CollectionID
	if q.CollectionID == "" {
		scope, value = "d.workspace_id=?", q.WorkspaceID
	}
	if q.Archive == refs.ArchiveActive {
		scope += " AND NOT d.archived"
	}
	if q.Archive == refs.ArchiveArchived {
		scope += " AND d.archived"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.resource_id, d.views FROM doc_fts JOIN documents d ON d.rowid=doc_fts.rowid
 WHERE doc_fts MATCH ? AND `+scope+` LIMIT ?`, renderOn(q.Text, nil, documentColumns(q.Content)), value, limit+1)
	if err != nil {
		return out, err
	}
	n := 0
	for rows.Next() {
		var id string
		var views int
		if err = rows.Scan(&id, &views); err != nil {
			break
		}
		if n++; n > limit {
			out.Capped = true
			break
		}
		if views&int(search.ViewHead) != 0 {
			out.Head = append(out.Head, id)
		}
		if views&int(search.ViewPublished) != 0 {
			out.Published = append(out.Published, id)
		}
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

// read runs fn in one read transaction, so counts, pages and facets agree.
func (s *Store) read(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}
