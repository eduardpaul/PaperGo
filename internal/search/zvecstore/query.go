package zvecstore

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"

	"papergo/internal/search"
)

type match struct {
	key   string
	rank  float64
	chunk *search.StoredChunk
	by    []string
}

func (s *Store) filter(f search.Filter) (string, error) {
	text, err := nativeFilter(f)
	if err != nil {
		return "", err
	}
	if f.Fields != nil {
		// Validate even an empty store, and restrict before native top-k so
		// exact filters cannot hide matches behind unrelated ANN candidates.
		nodes := 0
		if _, err = fieldMatch(search.Document{}, f.Fields, 0, &nodes); err != nil {
			return "", err
		}
		keys := []string{}
		for key, r := range s.documents {
			nodes = 0
			ok, err := fieldMatch(*r.Document, f.Fields, 0, &nodes)
			if err != nil {
				return "", err
			}
			if ok {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		text = conjunction([]string{text, in("document_key", keys)})
	}
	return text, nil
}

func (s *Store) documentText(d search.Document, content bool) string {
	text := d.Name + "\n" + d.Keywords + "\n" + d.Body
	if content {
		for _, c := range s.published(d) {
			text += "\n" + search.EmbeddingInput(c.Chunk.Text, c.Chunk.Enrichment)
		}
	}
	return text
}

func visible(d search.Document, f search.Filter) bool {
	draft, read := slices.Contains(f.Access.Draft, d.ScopeID), slices.Contains(f.Access.Read, d.ScopeID)
	switch f.Surface {
	case search.SurfaceHead:
		return d.Views&search.ViewHead != 0 && draft
	case search.SurfacePublished:
		return d.Views&search.ViewPublished != 0 && (draft || read)
	default:
		return d.Views&search.ViewHead != 0 && draft || d.Views&search.ViewPublished != 0 && read && !draft
	}
}
func scalarMatch(d search.Document, f search.Filter) bool {
	if !visible(d, f) || len(f.Kinds) > 0 && !slices.Contains(f.Kinds, d.Kind) {
		return false
	}
	if f.WorkspaceID != "" && d.WorkspaceID != f.WorkspaceID || f.CollectionID != "" && d.CollectionID != f.CollectionID || f.ContentTypeID != "" && d.ContentTypeID != f.ContentTypeID || f.CreatedBy != "" && d.CreatedBy != f.CreatedBy || f.ModifiedBy != "" && d.ModifiedBy != f.ModifiedBy || f.Keyword != "" && !slices.Contains(d.KeywordIDs, f.Keyword) {
		return false
	}
	if !f.ModifiedFrom.IsZero() && d.ModifiedAt.Before(f.ModifiedFrom) || !f.ModifiedTo.IsZero() && !d.ModifiedAt.Before(f.ModifiedTo) {
		return false
	}
	if len(f.TermIDs) > 0 {
		found := false
		for _, id := range f.TermIDs {
			found = found || slices.Contains(d.TermIDs, id)
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Store) order(matches []match) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank > matches[j].rank
		}
		a, b := s.documents[matches[i].key].Document, s.documents[matches[j].key].Document
		if !a.ModifiedAt.Equal(b.ModifiedAt) {
			return a.ModifiedAt.After(b.ModifiedAt)
		}
		if a.ResourceID != b.ResourceID {
			return a.ResourceID < b.ResourceID
		}
		return a.Surface < b.Surface
	})
}

func (s *Store) keyword(ctx context.Context, q search.Query, filter string) ([]match, bool, error) {
	out := []match{}
	estimated := false
	if q.Text == nil {
		for key, r := range s.documents {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			if !scalarMatch(*r.Document, q.Filter) {
				continue
			}
			if q.Filter.Fields != nil {
				nodes := 0
				ok, err := fieldMatch(*r.Document, q.Filter.Fields, 0, &nodes)
				if err != nil {
					return nil, false, err
				}
				if !ok {
					continue
				}
			}
			out = append(out, match{key: key})
		}
		s.order(out)
		return out, false, nil
	}
	if s.handle == nil {
		return out, false, nil
	}
	field := "metadata"
	if q.Content {
		field = "content"
	}
	hits, err := nativeQuery(s.handle, field, s.render(q.Text, true), conjunction([]string{filter, "row_kind = 'doc'"}), nil, s.limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(hits) > s.limit {
		estimated = true
		hits = hits[:s.limit]
	}
	for _, hit := range hits {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		r, ok := s.documents[hit.key]
		if !ok {
			continue
		}
		d := *r.Document
		if !s.matches(s.documentText(d, q.Content), q.Text, true) {
			continue
		}
		m := match{key: hit.key, rank: float64(hit.score), by: []string{search.MatchedKeyword}}
		// Heading matches outweigh body matches; exact forms outweigh stems.
		for _, group := range q.Text.Groups {
			for _, term := range group {
				if s.termMatches(d.Name, term, false) {
					m.rank += 100
				} else if s.termMatches(d.Keywords, term, false) {
					m.rank += 40
				} else if s.termMatches(d.Name+"\n"+d.Keywords, term, true) {
					m.rank += 20
				}
			}
		}
		if q.Content {
			best := -1
			for _, c := range s.published(d) {
				score := 0
				text := search.EmbeddingInput(c.Chunk.Text, c.Chunk.Enrichment)
				for _, group := range q.Text.Groups {
					for _, term := range group {
						if s.termMatches(text, term, true) {
							score++
						}
					}
				}
				if score > best && score > 0 {
					cc := c
					m.chunk = &cc
					best = score
				}
			}
		}
		out = append(out, m)
	}
	s.order(out)
	return out, estimated, nil
}

func (s *Store) semantic(ctx context.Context, q search.Query, filter string) ([]match, error) {
	out := []match{}
	if !q.Content || s.vectorHandle == nil || q.Model != s.meta.Model {
		return out, nil
	}
	if len(q.Vector) != s.meta.Dimensions {
		return nil, errors.New("query dimensions do not match the zvec model")
	}
	hits, err := nativeQuery(s.vectorHandle, "embedding", "", conjunction([]string{filter, "row_kind = 'passage'"}), q.Vector, 2000)
	if err != nil {
		return nil, err
	}
	minimum := q.MinSimilarity
	if minimum == 0 {
		minimum = search.MinSimilarity
	}
	best := map[string]match{}
	for _, hit := range hits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, ok := s.passages[hit.key]
		if !ok {
			continue
		}
		similarity := 1 - float64(hit.score)
		if similarity < float64(minimum) {
			continue
		}
		d := s.documents[p.document].Document
		if q.Text != nil && !s.matches(s.documentText(*d, true), &search.FullTextQuery{Excluded: q.Text.Excluded}, true) {
			continue
		}
		if old, ok := best[p.document]; ok && old.rank >= similarity {
			continue
		}
		c := s.chunks[p.chunk]
		if _, exists := s.vectors[vectorKey(q.Model, c.Chunk.InputHash)]; !exists {
			continue
		}
		best[p.document] = match{key: p.document, rank: similarity, chunk: &c, by: []string{search.MatchedSemantic}}
	}
	for _, m := range best {
		out = append(out, m)
	}
	s.order(out)
	if len(out) > search.CandidateLimit {
		out = out[:search.CandidateLimit]
	}
	return out, nil
}

// activateCachedModel reuses a previously embedded model after an offline
// copy or a configuration change. Empty/read-only stores stay lazy.
func (s *Store) activateCachedModel(ctx context.Context, model string, dims int) error {
	needed := false
	if err := s.read(ctx, func() error {
		if s.meta.Model != model {
			for _, v := range s.vectors {
				if v.Model == model && len(v.Vector) == dims {
					needed = true
					break
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !needed {
		return nil
	}
	return s.write(ctx, func() error {
		if s.meta.Model == model {
			return nil
		}
		return s.mutation(func() error { return s.switchModel(model, dims) })
	})
}

// Exclusions are exact and apply before native candidate caps, including ANN.
func (s *Store) exclude(filter string, q *search.FullTextQuery, content, stems bool) string {
	if q == nil || len(q.Excluded) == 0 {
		return filter
	}
	keys := []string{}
	for key, r := range s.documents {
		if s.matches(s.documentText(*r.Document, content), &search.FullTextQuery{Excluded: q.Excluded}, stems) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return conjunction([]string{filter, in("document_key", keys)})
}

func (s *Store) Search(ctx context.Context, q search.Query) (out search.Result, err error) {
	if (q.Mode == search.ModeSemantic || q.Mode == search.ModeHybrid) && q.Model != "" && len(q.Vector) > 0 {
		if err = s.activateCachedModel(ctx, q.Model, len(q.Vector)); err != nil {
			return
		}
	}
	read := s.read
	if q.Text != nil || q.Content && (q.Mode == search.ModeSemantic || q.Mode == search.ModeHybrid) {
		read = s.readNative
	}
	err = read(ctx, func() error {
		if q.Mode != "" && q.Mode != search.ModeKeyword && q.Mode != search.ModeSemantic && q.Mode != search.ModeHybrid {
			return errors.New("invalid search mode")
		}
		if q.Mode == search.ModeSemantic || q.Mode == search.ModeHybrid {
			if len(q.Vector) == 0 || q.Model == "" {
				return errors.New("semantic and hybrid searches need a vector and model")
			}
			for _, v := range q.Vector {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					return errors.New("invalid query vector")
				}
			}
		}
		filter, err := s.filter(q.Filter)
		if err != nil {
			return err
		}
		filter = s.exclude(filter, q.Text, q.Content, true)
		matches := []match{}
		if q.Mode != search.ModeSemantic {
			matches, out.Estimated, err = s.keyword(ctx, q, filter)
			if err != nil {
				return err
			}
		}
		if q.Mode == search.ModeSemantic {
			matches, err = s.semantic(ctx, q, filter)
		} else if q.Mode == search.ModeHybrid {
			semantic, err := s.semantic(ctx, q, filter)
			if err != nil {
				return err
			}
			if len(matches) > search.CandidateLimit {
				matches = matches[:search.CandidateLimit]
			}
			fused := map[string]match{}
			for i, m := range matches {
				m.rank = 1 / float64(search.FusionK+i+1)
				fused[m.key] = m
			}
			for i, m := range semantic {
				if keyword, ok := fused[m.key]; ok {
					keyword.rank += 1 / float64(search.FusionK+i+1)
					keyword.by = append(keyword.by, search.MatchedSemantic)
					if keyword.chunk == nil {
						keyword.chunk = m.chunk
					}
					fused[m.key] = keyword
				} else {
					m.rank = 1 / float64(search.FusionK+i+1)
					fused[m.key] = m
				}
			}
			matches = matches[:0]
			for _, m := range fused {
				matches = append(matches, m)
			}
			s.order(matches)
		}
		if err != nil {
			return err
		}
		out.Total = len(matches)
		if q.Facets {
			allowed := true
			if q.VerifyFacets != nil {
				for start := 0; start < len(matches); start += 200 {
					keys := []search.Key{}
					for _, m := range matches[start:min(start+200, len(matches))] {
						d := s.documents[m.key].Document
						keys = append(keys, search.Key{ResourceID: d.ResourceID, Surface: d.Surface})
					}
					ok, e := q.VerifyFacets(ctx, keys)
					if e != nil {
						return e
					}
					if !ok {
						allowed = false
						break
					}
				}
			}
			if allowed {
				out.Facets = s.facets(matches)
			}
		}
		limit := q.Limit
		if limit <= 0 {
			limit = 25
		}
		offset := min(max(q.Offset, 0), len(matches))
		for _, m := range matches[offset:min(offset+limit, len(matches))] {
			d := s.documents[m.key].Document
			h := search.Hit{ResourceID: d.ResourceID, Kind: d.Kind, Surface: d.Surface, WorkspaceID: d.WorkspaceID, CollectionID: d.CollectionID, ContentTypeID: d.ContentTypeID, RevisionID: d.RevisionID, Name: d.Name, Text: d.Body, Rank: m.rank, MatchedBy: m.by, ModifiedBy: d.ModifiedBy, ModifiedAt: d.ModifiedAt}
			if m.chunk != nil {
				h.Text = m.chunk.Chunk.Text
				h.Page = m.chunk.Chunk.Page
			}
			out.Hits = append(out.Hits, h)
		}
		return nil
	})
	return
}

func (s *Store) facets(matches []match) *search.Facets {
	counts := [5]map[string]int{}
	for i := range counts {
		counts[i] = map[string]int{}
	}
	for _, m := range matches {
		d := s.documents[m.key].Document
		for i, value := range []string{d.WorkspaceID, d.CollectionID, d.ContentTypeID} {
			if value != "" {
				counts[i][value]++
			}
		}
		for _, term := range slices.Compact(slices.Sorted(slices.Values(d.TermIDs))) {
			counts[3][term]++
		}
		for _, keyword := range slices.Compact(slices.Sorted(slices.Values(d.KeywordIDs))) {
			counts[4][keyword]++
		}
	}
	values := func(counts map[string]int) []search.FacetValue {
		out := []search.FacetValue{}
		for value, count := range counts {
			out = append(out, search.FacetValue{Value: value, Count: count})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Value < out[j].Value
		})
		if len(out) > search.FacetSize {
			out = out[:search.FacetSize]
		}
		return out
	}
	return &search.Facets{Workspace: values(counts[0]), Collection: values(counts[1]), ContentType: values(counts[2]), Term: values(counts[3]), Keyword: values(counts[4])}
}

func (s *Store) Match(ctx context.Context, q search.MatchQuery) (out search.Matches, err error) {
	read := s.read
	if q.Text != nil {
		read = s.readNative
	}
	err = read(ctx, func() error {
		if q.Text == nil || s.handle == nil {
			return nil
		}
		if q.CollectionID == "" && q.WorkspaceID == "" {
			return errors.New("match needs a collection or workspace")
		}
		limit := q.Limit
		if limit <= 0 {
			limit = 10000
		}
		limit = min(limit, s.limit)
		parts := []string{"row_kind = 'doc'"}
		if q.CollectionID != "" {
			parts = append(parts, equal("collection_id", encode(q.CollectionID)))
		}
		if q.WorkspaceID != "" {
			parts = append(parts, equal("workspace_id", encode(q.WorkspaceID)))
		}
		field := "metadata"
		if q.Content {
			field = "content"
		}
		hits, err := nativeQuery(s.handle, field, s.render(q.Text, false), s.exclude(conjunction(parts), q.Text, q.Content, false), nil, min(limit*2+1, 100000))
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		sort.Slice(hits, func(i, j int) bool { return hits[i].key < hits[j].key })
		for _, hit := range hits {
			d := s.documents[hit.key].Document
			if d == nil || !s.matches(s.documentText(*d, q.Content), q.Text, false) {
				continue
			}
			if !seen[d.ResourceID] && len(seen) >= limit {
				out.Capped = true
				continue
			}
			seen[d.ResourceID] = true
			if d.Views&search.ViewHead != 0 {
				out.Head = append(out.Head, d.ResourceID)
			}
			if d.Views&search.ViewPublished != 0 {
				out.Published = append(out.Published, d.ResourceID)
			}
		}
		out.Capped = out.Capped || len(hits) >= min(limit*2+1, 100000)
		slices.Sort(out.Head)
		out.Head = slices.Compact(out.Head)
		slices.Sort(out.Published)
		out.Published = slices.Compact(out.Published)
		return nil
	})
	return
}
