package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"papergo/internal/search"
)

// VectorState is the count and newest stamp of a model's vectors.
func (s *Store) VectorState(ctx context.Context, model string) (count, newest int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(max(stamp),0) FROM vectors WHERE model=?`, model).Scan(&count, &newest)
	return count, newest, err
}

// LoadVectors yields a model's vectors stamped after after, up to newest.
func (s *Store) LoadVectors(ctx context.Context, model string, after, newest int64, yield func(hash string, dims int, raw []byte) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT input_hash, dims, vector FROM vectors WHERE model=? AND stamp>? AND stamp<=? ORDER BY stamp`, model, after, newest)
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
				model, v.InputHash, len(v.Vector), search.VectorBytes(v.Vector), stamp); err != nil {
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
	snapshot, err := s.vectors.Snapshot(ctx, s, q.Model)
	if err != nil {
		return nil, err
	}
	if q.Text != nil && len(q.Text.Excluded) > 0 {
		filter += " AND d.rowid NOT IN (SELECT rowid FROM doc_fts WHERE doc_fts MATCH ?)"
		args = append(args, renderOn(&search.FullTextQuery{Groups: [][]search.Term{q.Text.Excluded}}, s.stem, docColumns))
	}
	candidates := snapshot.Nearest(q.Vector, search.PassageCandidates, search.MinSimilarity, nil)
	matches, err := documentsOf(ctx, tx, candidates, filter, args, limit)
	if err != nil || len(matches) >= limit || len(candidates) < search.PassageCandidates {
		return matches, err
	}
	// The best inputs belong mostly to documents the filters leave out:
	// search again among the inputs of the documents they keep.
	allowed, err := allowedInputs(ctx, tx, filter, args)
	if err != nil {
		return nil, err
	}
	return documentsOf(ctx, tx, snapshot.Nearest(q.Vector, search.PassageCandidates, search.MinSimilarity, allowed), filter, args, limit)
}

// documentsOf maps similar inputs to the documents whose published content
// holds them and that pass the filter, each with its best chunk.
func documentsOf(ctx context.Context, tx *sql.Tx, candidates []search.Similar, filter string, args []any, limit int) ([]semanticMatch, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	var list strings.Builder
	list.WriteByte('[')
	for i, c := range candidates {
		if i > 0 {
			list.WriteByte(',')
		}
		fmt.Fprintf(&list, `["%s",%g]`, c.Hash, c.Similarity)
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
