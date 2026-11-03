package pgstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"papergo/internal/dal/refs"
	"papergo/internal/search"
)

// clause is a piece of SQL over documents d with its parameters, in order.
type clause struct {
	sql  string
	args []any
}

// published is a document's published file content: chunks c of its file
// version.
const published = "c.item_id=d.resource_id AND c.blob_id=d.blob_id AND c.published"

// matching is the condition that documents d match a query: every group in
// the metadata words, or with content in a chunk of the published file
// content, and no excluded term in either. As on SQLite, the groups of a
// query may match in different places.
func matching(q *search.FullTextQuery, stem *search.Stemmer, content bool) clause {
	in := func(query string) clause {
		c := clause{"d.words @@ ?::tsquery", []any{query}}
		if content {
			c.sql = "(" + c.sql + " OR EXISTS (SELECT 1 FROM chunks c WHERE " + published + " AND c.words @@ ?::tsquery))"
			c.args = append(c.args, query)
		}
		return c
	}
	var parts []string
	var args []any
	for _, g := range q.Groups {
		c := in(tsquery(g, stem))
		parts, args = append(parts, c.sql), append(args, c.args...)
	}
	if len(q.Excluded) > 0 {
		c := in(tsquery(q.Excluded, stem))
		parts, args = append(parts, "NOT "+c.sql), append(args, c.args...)
	}
	return clause{strings.Join(parts, " AND "), args}
}

// positive is a query of every term a query may match, for ranking.
func positive(q *search.FullTextQuery, stem *search.Stemmer) string {
	groups := make([]string, len(q.Groups))
	for i, g := range q.Groups {
		groups[i] = tsquery(g, stem)
	}
	return strings.Join(groups, " | ")
}

// rank scores documents d for a query by their weighted words, as the
// SQLite store's bm25 column weights: with content, the best matching
// chunk counts as the body does. ts_rank weighs how often and where words
// occur, without bm25's rarity across documents.
func rank(q *search.FullTextQuery, stem *search.Stemmer, content bool) clause {
	query := positive(q, stem)
	c := clause{"ts_rank(" + rankWeights + ", d.words, ?::tsquery)::float8", []any{query}}
	if content {
		c.sql = "(" + c.sql + " + 0.1 * coalesce((SELECT max(ts_rank(" + rankWeights + ", c.words, ?::tsquery)) FROM chunks c WHERE " + published + " AND c.words @@ ?::tsquery), 0))"
		c.args = append(c.args, query, query)
	}
	return c
}

// where builds the filter of a search over documents d: what the caller may
// read on the requested surface, then the narrowing filters.
func where(f search.Filter) (clause, error) {
	draft, read := list(f.Access.Draft), list(f.Access.Read)
	var conds []string
	var args []any
	switch f.Surface {
	case search.SurfaceHead:
		conds = append(conds, "(d.views & 1) <> 0 AND d.scope_id = ANY(?::text[])")
		args = append(args, draft)
	case search.SurfacePublished:
		conds = append(conds, "(d.views & 2) <> 0 AND (d.scope_id = ANY(?::text[]) OR d.scope_id = ANY(?::text[]))")
		args = append(args, read, draft)
	default:
		// Drafts where the caller reads them, else what is published.
		conds = append(conds, "((d.views & 1) <> 0 AND d.scope_id = ANY(?::text[]) OR (d.views & 2) <> 0 AND d.scope_id = ANY(?::text[]) AND NOT d.scope_id = ANY(?::text[]))")
		args = append(args, draft, read, draft)
	}
	if !f.Archive.Valid() {
		return clause{}, errors.New("invalid archive selector")
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
		conds = append(conds, "d.kind = ANY(?::text[])")
		args = append(args, f.Kinds)
	}
	for _, c := range []struct{ column, value string }{{"workspace_id", f.WorkspaceID}, {"collection_id", f.CollectionID}, {"created_by", f.CreatedBy}, {"modified_by", f.ModifiedBy}} {
		if c.value != "" {
			conds = append(conds, "d."+c.column+"=?")
			args = append(args, c.value)
		}
	}
	if len(f.ContentTypeIDs) > 0 {
		conds = append(conds, "d.content_type_id = ANY(?::text[])")
		args = append(args, f.ContentTypeIDs)
	}
	if f.Within != nil {
		conds = append(conds, "(d.workspace_id = ANY(?::text[]) OR d.collection_id = ANY(?::text[]))")
		args = append(args, list(f.Within.Workspaces), list(f.Within.Collections))
	}
	if len(f.TermIDs) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM document_terms t WHERE t.doc=d.id AND t.term_id = ANY(?::text[]))")
		args = append(args, f.TermIDs)
	}
	if f.Keyword != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM document_keywords g WHERE g.doc=d.id AND g.keyword=?)")
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
			return clause{}, err
		}
		conds = append(conds, "("+text+")")
		args = append(args, a...)
	}
	return clause{strings.Join(conds, " AND "), args}, nil
}

// errFilter reports a field filter the store cannot run; the service
// validates filters first, so it means a bug.
var errFilter = errors.New("invalid field filter")

// fieldFilter compiles a field filter over documents d.
func fieldFilter(f *search.FieldFilter, depth int, nodes *int) (string, []any, error) {
	if *nodes++; *nodes > search.MaxFilterNodes || depth > search.MaxFilterDepth {
		return "", nil, errFilter
	}
	if f.Not != nil {
		text, args, err := fieldFilter(f.Not, depth+1, nodes)
		return "NOT (" + text + ")", args, err
	}
	if f.And != nil || f.Or != nil {
		items, op := f.And, " AND "
		if f.Or != nil {
			items, op = f.Or, " OR "
		}
		if len(items) == 0 {
			return "", nil, errFilter
		}
		parts := make([]string, len(items))
		var args []any
		for i := range items {
			text, a, err := fieldFilter(&items[i], depth+1, nodes)
			if err != nil {
				return "", nil, err
			}
			parts[i], args = "("+text+")", append(args, a...)
		}
		return strings.Join(parts, op), args, nil
	}
	held := "SELECT 1 FROM document_fields f WHERE f.doc=d.id AND f.key=? AND f.type=? AND f.scale=?"
	field := []any{f.Key, f.Type, f.Scale}
	column, array := "f.text", "text[]"
	values := make([]any, len(f.Values))
	var texts []string
	var ints []int64
	for i, v := range f.Values {
		values[i] = v.Text
		texts = append(texts, v.Text)
		ints = append(ints, v.Int)
	}
	var many any = texts
	if search.IntegerType(f.Type) {
		column, array, many = "f.int", "bigint[]", ints
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
		member := "EXISTS (" + held + " AND " + column + " = ANY(?::" + array + "))"
		if f.Op == "in" {
			return member, append(field, many), nil
		}
		// Present, and no value equals: as collection queries read ne.
		return "EXISTS (" + held + ") AND NOT " + member, append(append(field, field...), many), nil
	}
	operator := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[f.Op]
	if operator == "" || len(values) != 1 {
		return "", nil, errFilter
	}
	return "EXISTS (" + held + " AND " + column + operator + "?)", append(field, values[0]), nil
}

func (s *Store) Search(ctx context.Context, q search.Query) (search.Result, error) {
	var out search.Result
	filter, err := where(q.Filter)
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
		err = s.read(ctx, func(tx tx) error { return s.fused(ctx, tx, q, filter, limit, &out) })
		return out, err
	case "", search.ModeKeyword:
	default:
		return out, errors.New("unknown search mode " + q.Mode)
	}
	order := "d.modified_at DESC, d.resource_id"
	matches := clause{"SELECT d.id AS doc, 0.0::float8 AS rank FROM documents d WHERE " + filter.sql, filter.args}
	if q.Text != nil {
		r, m := rank(q.Text, s.stem, q.Content), matching(q.Text, s.stem, q.Content)
		matches = clause{"SELECT d.id AS doc, " + r.sql + " AS rank FROM documents d WHERE " + m.sql + " AND " + filter.sql, append(append(r.args, m.args...), filter.args...)}
		order = "m.rank DESC, d.resource_id"
	}
	err = s.read(ctx, func(tx tx) error {
		if err := tx.row(ctx, "SELECT count(*) FROM ("+matches.sql+") m", matches.args...).Scan(&out.Total); err != nil {
			return err
		}
		rows, err := tx.query(ctx, `SELECT `+hitColumns+`, m.rank FROM (`+matches.sql+`) m JOIN documents d ON d.id=m.doc
 ORDER BY `+order+` LIMIT ? OFFSET ?`, append(matches.args, limit, q.Offset)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var h search.Hit
			var id int64
			if err = scanHit(rows, &h, &id, &h.Rank); err != nil {
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
			verified, e := verifyFacets(ctx, tx, matches, q.VerifyFacets)
			if e != nil {
				return e
			}
			if verified {
				out.Facets, err = facets(ctx, tx, matches)
			}
		}
		return err
	})
	return out, err
}

// hitColumns are the document columns scanHit reads.
const hitColumns = "d.archived,d.archived_at,d.archived_by,d.id, d.resource_id, d.kind, d.surface, d.workspace_id, d.collection_id, d.content_type_id, d.revision_id, d.name, d.keywords, d.body, d.modified_by, d.modified_at"

func scanHit(rows *sql.Rows, h *search.Hit, id *int64, extra ...any) error {
	var keywords, body string
	var modified int64
	var archived sql.NullInt64
	err := rows.Scan(append([]any{&h.Archived, &archived, &h.ArchivedBy, id, &h.ResourceID, &h.Kind, &h.Surface, &h.WorkspaceID, &h.CollectionID, &h.ContentTypeID, &h.RevisionID, &h.Name, &keywords, &body, &h.ModifiedBy, &modified}, extra...)...)
	h.ArchivedAt = archiveTime(archived)
	h.ModifiedAt, h.Text = fromNanos(modified), body
	if h.Text == "" {
		h.Text = keywords
	}
	return err
}

// fused answers semantic and hybrid searches: the best CandidateLimit
// documents of each side, fused by reciprocal rank. Totals and facets cover
// the fused candidates.
func (s *Store) fused(ctx context.Context, tx tx, q search.Query, filter clause, limit int, out *search.Result) error {
	var keyword []int64
	if q.Mode == search.ModeHybrid && q.Text != nil {
		r, m := rank(q.Text, s.stem, q.Content), matching(q.Text, s.stem, q.Content)
		rows, err := tx.query(ctx, `SELECT d.id FROM documents d WHERE `+m.sql+` AND `+filter.sql+` ORDER BY `+r.sql+` DESC, d.resource_id LIMIT ?`,
			append(append(append(m.args, filter.args...), r.args...), search.CandidateLimit)...)
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
	semantic, err := s.semantic(ctx, tx, q, filter, search.CandidateLimit)
	if err != nil {
		return err
	}
	semanticIDs := map[int64]semanticMatch{}
	semanticDocs := make([]int64, len(semantic))
	for i, m := range semantic {
		semanticDocs[i] = m.doc
		semanticIDs[m.doc] = m
	}
	ordered, scores := search.Fuse(keyword, semanticDocs)
	keywordIDs := map[int64]bool{}
	for _, id := range keyword {
		keywordIDs[id] = true
	}
	out.Total = len(ordered)
	if q.Facets {
		candidates := clause{"SELECT unnest(?::bigint[]) AS doc, 0.0::float8 AS rank", []any{ordered}}
		verified, err := verifyFacets(ctx, tx, candidates, q.VerifyFacets)
		if err != nil {
			return err
		}
		if verified {
			if out.Facets, err = facets(ctx, tx, candidates); err != nil {
				return err
			}
		}
	}
	page := ordered[min(q.Offset, len(ordered)):min(q.Offset+limit, len(ordered))]
	if len(page) == 0 {
		return nil
	}
	rows, err := tx.query(ctx, `SELECT `+hitColumns+` FROM documents d WHERE d.id = ANY(?::bigint[])`, page)
	if err != nil {
		return err
	}
	byID := map[int64]search.Hit{}
	for rows.Next() {
		var h search.Hit
		var id int64
		if err = scanHit(rows, &h, &id); err != nil {
			break
		}
		byID[id] = h
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
			if err = tx.row(ctx, `SELECT page, text FROM chunks WHERE id=?`, m.chunk).Scan(&h.Page, &h.Text); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
func verifyFacets(ctx context.Context, tx tx, matches clause, verify func(context.Context, []search.Key) (bool, error)) (bool, error) {
	if verify == nil {
		return true, nil
	}
	rows, err := tx.query(ctx, `SELECT d.resource_id, d.surface FROM (`+matches.sql+`) m JOIN documents d ON d.id=m.doc`, matches.args...)
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
func (s *Store) pageHits(ctx context.Context, tx tx, q *search.FullTextQuery, hits []search.Hit) error {
	match, ranked := whole(q, s.stem), positive(q, s.stem)
	for i, h := range hits {
		var page int
		var text string
		err := tx.row(ctx, `SELECT c.page, c.text FROM chunks c
 WHERE c.item_id=? AND c.published AND c.blob_id=(SELECT blob_id FROM documents WHERE resource_id=? AND surface=?) AND c.words @@ ?::tsquery
 ORDER BY ts_rank(`+rankWeights+`, c.words, ?::tsquery) DESC, c.part, c.ordinal LIMIT 1`, h.ResourceID, h.ResourceID, h.Surface, match, ranked).Scan(&page, &text)
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
func facets(ctx context.Context, tx tx, matches clause) (*search.Facets, error) {
	f := &search.Facets{}
	for _, facet := range []struct {
		into  *[]search.FacetValue
		query string
	}{
		{&f.Workspace, "SELECT d.workspace_id, count(*) FROM (" + matches.sql + ") m JOIN documents d ON d.id=m.doc GROUP BY 1"},
		{&f.Collection, "SELECT d.collection_id, count(*) FROM (" + matches.sql + ") m JOIN documents d ON d.id=m.doc WHERE d.collection_id<>'' GROUP BY 1"},
		{&f.ContentType, "SELECT d.content_type_id, count(*) FROM (" + matches.sql + ") m JOIN documents d ON d.id=m.doc WHERE d.content_type_id<>'' GROUP BY 1"},
		{&f.Term, "SELECT t.term_id, count(*) FROM (" + matches.sql + ") m JOIN document_terms t ON t.doc=m.doc GROUP BY 1"},
		{&f.Keyword, "SELECT g.keyword, count(*) FROM (" + matches.sql + ") m JOIN document_keywords g ON g.doc=m.doc GROUP BY 1"},
	} {
		rows, err := tx.query(ctx, "SELECT value, n FROM ("+facet.query+") f(value, n) ORDER BY n DESC, value COLLATE \"C\" LIMIT ?", append(matches.args, search.FacetSize)...)
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
	m := matching(q.Text, nil, q.Content)
	err := s.read(ctx, func(tx tx) error {
		rows, err := tx.query(ctx, `SELECT d.resource_id, d.views FROM documents d WHERE `+m.sql+` AND `+scope+` LIMIT ?`, append(m.args, value, limit+1)...)
		if err != nil {
			return err
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
		return errors.Join(err, rows.Err(), rows.Close())
	})
	return out, err
}
