package sqlitestore

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"papergo/internal/search"
)

// vectorIndex holds the vectors of one model in memory, a port of
// PaperDotNet's VectorIndex: searches scan an immutable snapshot without
// locks. Before each semantic search it checks the table (count and newest
// stamp of the model) and loads only what changed, so writes by any process
// are found.
type vectorIndex struct {
	mu        sync.Mutex
	model     string
	dims      int
	hashes    []string
	data      []float32
	positions map[string]int
	count     int64
	newest    int64
}

// vectorSnapshot is a consistent view of the index. Later loads append past
// its length, so it never changes.
type vectorSnapshot struct {
	dims   int
	hashes []string
	data   []float32
}

func (s vectorSnapshot) vector(i int) []float32 { return s.data[i*s.dims : (i+1)*s.dims] }

// snapshot returns the index of model, current with the table.
func (x *vectorIndex) snapshot(ctx context.Context, db *sql.DB, model string) (vectorSnapshot, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var count, newest int64
	if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(max(stamp),0) FROM vectors WHERE model=?`, model).Scan(&count, &newest); err != nil {
		return vectorSnapshot{}, err
	}
	if x.model != model || count < x.count {
		// Another model, or vectors were deleted: load everything again.
		x.model, x.dims, x.hashes, x.data, x.positions, x.count, x.newest = model, 0, nil, nil, map[string]int{}, 0, -1
	}
	if count != x.count || newest != x.newest {
		if err := x.load(ctx, db, newest); err != nil {
			return vectorSnapshot{}, err
		}
		if int64(len(x.hashes)) != count {
			x.dims, x.hashes, x.data, x.positions, x.newest = 0, nil, nil, map[string]int{}, -1
			if err := x.load(ctx, db, newest); err != nil {
				return vectorSnapshot{}, err
			}
		}
		x.count, x.newest = count, newest
	}
	return vectorSnapshot{dims: x.dims, hashes: x.hashes[:len(x.hashes):len(x.hashes)], data: x.data[:len(x.data):len(x.data)]}, nil
}

// load appends the model's vectors written after the newest loaded one, up
// to newest.
func (x *vectorIndex) load(ctx context.Context, db *sql.DB, newest int64) error {
	rows, err := db.QueryContext(ctx, `SELECT input_hash, dims, vector FROM vectors WHERE model=? AND stamp>? AND stamp<=? ORDER BY stamp`, x.model, x.newest, newest)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var dims int
		var raw []byte
		if err = rows.Scan(&hash, &dims, &raw); err != nil {
			return err
		}
		if _, ok := x.positions[hash]; ok || len(raw) != 4*dims {
			continue
		}
		if x.dims == 0 {
			x.dims = dims
		}
		if dims != x.dims {
			continue
		}
		x.positions[hash] = len(x.hashes)
		x.hashes = append(x.hashes, hash)
		x.data = appendVector(x.data, raw)
	}
	return rows.Err()
}

func appendVector(data []float32, raw []byte) []float32 {
	for i := 0; i < len(raw); i += 4 {
		data = append(data, math.Float32frombits(binary.LittleEndian.Uint32(raw[i:])))
	}
	return data
}

func vectorBytes(v []float32) []byte {
	raw := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(x))
	}
	return raw
}

// dot is the dot product of two vectors of equal length, unrolled by 8.
func dot(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3, s4, s5, s6, s7 float32
	i := 0
	for ; i+8 <= len(a); i += 8 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
		s4 += a[i+4] * b[i+4]
		s5 += a[i+5] * b[i+5]
		s6 += a[i+6] * b[i+6]
		s7 += a[i+7] * b[i+7]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return s0 + s1 + s2 + s3 + s4 + s5 + s6 + s7
}

// similar is a chunk input and its similarity to the query.
type similar struct {
	hash       string
	similarity float32
}

type similarHeap []similar

func (h similarHeap) Len() int           { return len(h) }
func (h similarHeap) Less(i, j int) bool { return h[i].similarity < h[j].similarity }
func (h similarHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *similarHeap) Push(x any)        { *h = append(*h, x.(similar)) }
func (h *similarHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// nearest returns the limit inputs most similar to the normalized query, at
// least minSimilarity, by brute force; allowed, when not nil, restricts the
// inputs.
func (s vectorSnapshot) nearest(query []float32, limit int, minSimilarity float32, allowed map[string]bool) []similar {
	if s.dims != len(query) || limit <= 0 {
		return nil
	}
	top := make(similarHeap, 0, limit)
	for i, hash := range s.hashes {
		if allowed != nil && !allowed[hash] {
			continue
		}
		similarity := dot(query, s.vector(i))
		if similarity < minSimilarity {
			continue
		}
		if len(top) < limit {
			heap.Push(&top, similar{hash, similarity})
		} else if similarity > top[0].similarity {
			top[0] = similar{hash, similarity}
			heap.Fix(&top, 0)
		}
	}
	out := make([]similar, len(top))
	for i := len(top) - 1; i >= 0; i-- {
		out[i] = heap.Pop(&top).(similar)
	}
	return out
}

// passageCandidates is how many chunk inputs a semantic search takes from
// the index before it filters them, as PaperDotNet's.
const passageCandidates = 1000

func (s *Store) GenerationToEmbed(ctx context.Context, model, generation string) ([]search.ChunkToEmbed, int, error) {
	var inputs int
	if err := s.db.QueryRowContext(ctx, `SELECT count(DISTINCT input_hash) FROM chunks WHERE generation=?`, generation).Scan(&inputs); err != nil {
		return nil, 0, err
	}
	todo, err := s.toEmbed(ctx, `c.generation=?`, model, generation, -1)
	return todo, inputs, err
}

func (s *Store) ChunksToEmbed(ctx context.Context, model string, limit int) ([]search.ChunkToEmbed, error) {
	return s.toEmbed(ctx, `c.published=1`, model, nil, limit)
}

func (s *Store) toEmbed(ctx context.Context, where, model string, arg any, limit int) ([]search.ChunkToEmbed, error) {
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.input_hash, c.text, c.enrichment FROM chunks c WHERE `+where+`
 AND NOT EXISTS (SELECT 1 FROM vectors v WHERE v.model=? AND v.input_hash=c.input_hash) GROUP BY c.input_hash ORDER BY min(c.rowid) LIMIT ?`, append(args, model, limit)...)
	if err != nil {
		return nil, err
	}
	var out []search.ChunkToEmbed
	for rows.Next() {
		var c search.ChunkToEmbed
		var text, enrichment string
		if err = rows.Scan(&c.InputHash, &text, &enrichment); err != nil {
			break
		}
		c.Input = search.EmbeddingInput(text, enrichment)
		out = append(out, c)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

func (s *Store) SetVectors(ctx context.Context, model string, vectors []search.HashVector) error {
	if len(vectors) == 0 {
		return nil
	}
	if model == "" {
		return errors.New("vectors need a model")
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var stamp int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(stamp),0)+1 FROM vectors`).Scan(&stamp); err != nil {
			return err
		}
		for _, v := range vectors {
			if v.InputHash == "" || len(v.Vector) == 0 {
				return fmt.Errorf("a vector needs an input hash and values")
			}
			// One input has one vector per model: a second write changes nothing.
			if _, err := tx.ExecContext(ctx, `INSERT INTO vectors(model,input_hash,dims,vector,stamp) VALUES (?,?,?,?,?) ON CONFLICT(model,input_hash) DO NOTHING`,
				model, v.InputHash, len(v.Vector), vectorBytes(v.Vector), stamp); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CollectVectors(ctx context.Context, model string, now time.Time, keep time.Duration) (int, error) {
	var deleted int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		used := `model=? AND input_hash IN (SELECT input_hash FROM chunks)`
		if _, err := tx.ExecContext(ctx, `UPDATE vectors SET unused_since=NULL WHERE unused_since IS NOT NULL AND `+used, model); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE vectors SET unused_since=? WHERE unused_since IS NULL AND NOT (`+used+`)`, now.UnixNano(), model); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM vectors WHERE unused_since<=?`, now.Add(-keep).UnixNano())
		if err != nil {
			return err
		}
		deleted, err = res.RowsAffected()
		return err
	})
	return int(deleted), err
}

func (s *Store) Embeddings(ctx context.Context, model string) (search.EmbeddingStats, error) {
	var st search.EmbeddingStats
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM vectors WHERE model=?), (SELECT coalesce(sum(dims),0)*4 FROM vectors WHERE model=?),
 (SELECT count(DISTINCT c.input_hash) FROM chunks c WHERE c.published=1 AND NOT EXISTS (SELECT 1 FROM vectors v WHERE v.model=? AND v.input_hash=c.input_hash))`,
		model, model, model).Scan(&st.Vectors, &st.MemoryBytes, &st.Pending)
	return st, err
}

// semanticMatch is a document found by meaning, with its most similar
// published chunk.
type semanticMatch struct {
	doc        int64
	chunk      int64
	similarity float64
}

// semantic finds the documents the caller may read whose published file
// content is most similar to the query vector, best first, at most limit.
// Documents matching an excluded word are left out. File content is only
// searched with q.Content, so without it nothing matches.
func (s *Store) semantic(ctx context.Context, tx *sql.Tx, q search.Query, filter string, args []any, limit int) ([]semanticMatch, error) {
	if !q.Content {
		return nil, nil
	}
	snapshot, err := s.vectors.snapshot(ctx, s.db, q.Model)
	if err != nil {
		return nil, err
	}
	minSimilarity := q.MinSimilarity
	if minSimilarity == 0 {
		minSimilarity = search.MinSimilarity
	}
	if q.Text != nil && len(q.Text.Excluded) > 0 {
		filter += " AND d.rowid NOT IN (SELECT rowid FROM doc_fts WHERE doc_fts MATCH ?)"
		args = append(args, renderOn(&search.FullTextQuery{Groups: [][]search.Term{q.Text.Excluded}}, s.stem, docColumns))
	}
	candidates := snapshot.nearest(q.Vector, passageCandidates, minSimilarity, nil)
	matches, err := documentsOf(ctx, tx, candidates, filter, args, limit)
	if err != nil || len(matches) >= limit || len(candidates) < passageCandidates {
		return matches, err
	}
	// The best inputs belong mostly to documents the filters leave out:
	// search again among the inputs of the documents they keep.
	allowed, err := allowedInputs(ctx, tx, filter, args)
	if err != nil {
		return nil, err
	}
	return documentsOf(ctx, tx, snapshot.nearest(q.Vector, passageCandidates, minSimilarity, allowed), filter, args, limit)
}

// documentsOf maps similar inputs to the documents whose published content
// holds them and that pass the filter, each with its best chunk.
func documentsOf(ctx context.Context, tx *sql.Tx, candidates []similar, filter string, args []any, limit int) ([]semanticMatch, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	var list strings.Builder
	list.WriteByte('[')
	for i, c := range candidates {
		if i > 0 {
			list.WriteByte(',')
		}
		fmt.Fprintf(&list, `["%s",%g]`, c.hash, c.similarity)
	}
	list.WriteByte(']')
	// max() picks the bare columns of its row: the most similar chunk.
	rows, err := tx.QueryContext(ctx, `WITH cand(hash, similarity) AS (SELECT json_extract(value,'$[0]'), json_extract(value,'$[1]') FROM json_each(?))
 SELECT d.rowid, c.rowid, max(cand.similarity) FROM cand JOIN chunks c ON c.input_hash=cand.hash AND c.published=1
 JOIN documents d ON d.resource_id=c.item_id AND d.blob_id=c.blob_id
 WHERE `+filter+` GROUP BY d.rowid ORDER BY 3 DESC, d.resource_id LIMIT ?`, append(append([]any{list.String()}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	var out []semanticMatch
	for rows.Next() {
		var m semanticMatch
		if err = rows.Scan(&m.doc, &m.chunk, &m.similarity); err != nil {
			break
		}
		out = append(out, m)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

// allowedInputs lists the chunk inputs of the published content of the
// documents that pass the filter.
func allowedInputs(ctx context.Context, tx *sql.Tx, filter string, args []any) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.input_hash FROM documents d JOIN chunks c ON c.item_id=d.resource_id AND c.blob_id=d.blob_id AND c.published=1 WHERE `+filter, args...)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for rows.Next() {
		var hash string
		if err = rows.Scan(&hash); err != nil {
			break
		}
		allowed[hash] = true
	}
	return allowed, errors.Join(err, rows.Err(), rows.Close())
}
