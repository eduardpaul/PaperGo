package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"papergo/internal/search"
)

// vectorSource reads vectors in a search's transaction, so the index of
// semantic search loads what the search sees, on the connection it holds.
type vectorSource struct{ tx tx }

func (v vectorSource) VectorState(ctx context.Context, model string) (count, newest int64, err error) {
	err = v.tx.row(ctx, `SELECT count(*), coalesce(max(stamp),0) FROM vectors WHERE model=?`, model).Scan(&count, &newest)
	return count, newest, err
}

func (v vectorSource) LoadVectors(ctx context.Context, model string, after, newest int64, yield func(hash string, dims int, raw []byte) error) error {
	rows, err := v.tx.query(ctx, `SELECT input_hash, dims, vector FROM vectors WHERE model=? AND stamp>? AND stamp<=? ORDER BY stamp`, model, after, newest)
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
		if err = yield(hash, dims, raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) GenerationToEmbed(ctx context.Context, model, generation string) ([]search.ChunkToEmbed, int, error) {
	var inputs int
	var todo []search.ChunkToEmbed
	err := s.read(ctx, func(tx tx) error {
		if err := tx.row(ctx, `SELECT count(DISTINCT input_hash) FROM chunks WHERE generation=?`, generation).Scan(&inputs); err != nil {
			return err
		}
		var err error
		todo, err = toEmbed(ctx, tx, `c.generation=?`, model, generation, nil)
		return err
	})
	return todo, inputs, err
}

func (s *Store) ChunksToEmbed(ctx context.Context, model string, limit int) ([]search.ChunkToEmbed, error) {
	var todo []search.ChunkToEmbed
	err := s.read(ctx, func(tx tx) error {
		var err error
		todo, err = toEmbed(ctx, tx, `c.published`, model, nil, limit)
		return err
	})
	return todo, err
}

// toEmbed lists the distinct inputs of the chunks where selects, without a
// vector of the model, in the order they were written; limit nil lists all.
func toEmbed(ctx context.Context, tx tx, where, model string, arg any, limit any) ([]search.ChunkToEmbed, error) {
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	rows, err := tx.query(ctx, `SELECT c.input_hash, c.text, c.enrichment FROM chunks c JOIN (SELECT c.input_hash, min(c.id) AS first FROM chunks c WHERE `+where+`
 AND NOT EXISTS (SELECT 1 FROM vectors v WHERE v.model=? AND v.input_hash=c.input_hash) GROUP BY c.input_hash) inputs ON c.id=inputs.first
 ORDER BY inputs.first LIMIT ?`, append(args, model, limit)...)
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
	return s.tx(ctx, func(tx tx) error {
		var stamp int64
		if err := tx.row(ctx, `SELECT nextval('vector_stamps')`).Scan(&stamp); err != nil {
			return err
		}
		for _, v := range vectors {
			if v.InputHash == "" || len(v.Vector) == 0 {
				return fmt.Errorf("a vector needs an input hash and values")
			}
			// One input has one vector per model: a second write changes nothing.
			if _, err := tx.exec(ctx, `INSERT INTO vectors(model,input_hash,dims,vector,stamp) VALUES (?,?,?,?,?) ON CONFLICT(model,input_hash) DO NOTHING`,
				model, v.InputHash, len(v.Vector), search.VectorBytes(v.Vector), stamp); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CollectVectors(ctx context.Context, model string, now time.Time, keep time.Duration) (int, error) {
	var deleted int64
	err := s.tx(ctx, func(tx tx) error {
		used := `model=? AND input_hash IN (SELECT input_hash FROM chunks)`
		if _, err := tx.exec(ctx, `UPDATE vectors SET unused_since=NULL WHERE unused_since IS NOT NULL AND `+used, model); err != nil {
			return err
		}
		if _, err := tx.exec(ctx, `UPDATE vectors SET unused_since=? WHERE unused_since IS NULL AND NOT (`+used+`)`, now.UnixNano(), model); err != nil {
			return err
		}
		res, err := tx.exec(ctx, `DELETE FROM vectors WHERE unused_since<=?`, now.Add(-keep).UnixNano())
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
	err := s.read(ctx, func(tx tx) error {
		return tx.row(ctx, `SELECT (SELECT count(*) FROM vectors WHERE model=?), (SELECT coalesce(sum(dims),0)*4 FROM vectors WHERE model=?),
 (SELECT count(DISTINCT c.input_hash) FROM chunks c WHERE c.published AND NOT EXISTS (SELECT 1 FROM vectors v WHERE v.model=? AND v.input_hash=c.input_hash))`,
			model, model, model).Scan(&st.Vectors, &st.MemoryBytes, &st.Pending)
	})
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
func (s *Store) semantic(ctx context.Context, tx tx, q search.Query, filter clause, limit int) ([]semanticMatch, error) {
	if !q.Content {
		return nil, nil
	}
	snapshot, err := s.vectors.Snapshot(ctx, vectorSource{tx}, q.Model)
	if err != nil {
		return nil, err
	}
	if q.Text != nil && len(q.Text.Excluded) > 0 {
		excluded := matching(&search.FullTextQuery{Groups: [][]search.Term{q.Text.Excluded}}, s.stem, true)
		filter = clause{filter.sql + " AND NOT (" + excluded.sql + ")", append(append([]any{}, filter.args...), excluded.args...)}
	}
	candidates := snapshot.Nearest(q.Vector, search.PassageCandidates, search.MinSimilarity, nil)
	matches, err := documentsOf(ctx, tx, candidates, filter, limit)
	if err != nil || len(matches) >= limit || len(candidates) < search.PassageCandidates {
		return matches, err
	}
	// The best inputs belong mostly to documents the filters leave out:
	// search again among the inputs of the documents they keep.
	allowed, err := allowedInputs(ctx, tx, filter)
	if err != nil {
		return nil, err
	}
	return documentsOf(ctx, tx, snapshot.Nearest(q.Vector, search.PassageCandidates, search.MinSimilarity, allowed), filter, limit)
}

// documentsOf maps similar inputs to the documents whose published content
// holds them and that pass the filter, each with its best chunk.
func documentsOf(ctx context.Context, tx tx, candidates []search.Similar, filter clause, limit int) ([]semanticMatch, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	hashes := make([]string, len(candidates))
	similarities := make([]float64, len(candidates))
	for i, c := range candidates {
		hashes[i], similarities[i] = c.Hash, float64(c.Similarity)
	}
	rows, err := tx.query(ctx, `WITH cand(hash, similarity) AS (SELECT * FROM unnest(?::text[], ?::float8[]))
 SELECT doc, chunk, similarity FROM (SELECT DISTINCT ON (d.id) d.id AS doc, c.id AS chunk, cand.similarity, d.resource_id
 FROM cand JOIN chunks c ON c.input_hash=cand.hash AND c.published JOIN documents d ON d.resource_id=c.item_id AND d.blob_id=c.blob_id
 WHERE `+filter.sql+` ORDER BY d.id, cand.similarity DESC, c.id) best ORDER BY similarity DESC, resource_id COLLATE "C" LIMIT ?`,
		append(append([]any{hashes, similarities}, filter.args...), limit)...)
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
func allowedInputs(ctx context.Context, tx tx, filter clause) (map[string]bool, error) {
	rows, err := tx.query(ctx, `SELECT DISTINCT c.input_hash FROM documents d JOIN chunks c ON `+published+` WHERE `+filter.sql, filter.args...)
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
