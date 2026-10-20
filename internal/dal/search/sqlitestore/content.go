package sqlitestore

import (
	"context"
	"database/sql"
	"errors"

	"papergo/internal/search"
)

// deleteChunks removes the chunks selected by where, with their words.
func deleteChunks(ctx context.Context, tx *sql.Tx, where string, args ...any) error {
	rows, err := tx.QueryContext(ctx, `SELECT rowid FROM chunks WHERE `+where, args...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		for _, statement := range []string{`DELETE FROM chunk_fts WHERE rowid=?`, `DELETE FROM chunks WHERE rowid=?`} {
			if _, err = tx.ExecContext(ctx, statement, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) StageContent(ctx context.Context, itemID, blobID, generation string, chunks []search.Chunk) error {
	if itemID == "" || blobID == "" || generation == "" {
		return errors.New("content needs an item, a file version and a generation")
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		parts := map[string]bool{}
		for _, c := range chunks {
			parts[c.Part] = true
		}
		for part := range parts {
			if err := deleteChunks(ctx, tx, `generation=? AND part=?`, generation, part); err != nil {
				return err
			}
		}
		// A generation stays unpublished while it grows, also when an
		// earlier part was published.
		var published bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chunks WHERE generation=? AND published=1)`, generation).Scan(&published); err != nil {
			return err
		}
		for _, c := range chunks {
			hash := c.InputHash
			if hash == "" {
				hash = c.Hash()
			}
			var rowid int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO chunks (item_id,blob_id,generation,part,ordinal,published,page,kind,text,enrichment,input_hash,language)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?) RETURNING rowid`, itemID, blobID, generation, c.Part, c.Ordinal, published, c.Page, c.Kind, c.Text, c.Enrichment, hash, c.Language).Scan(&rowid); err != nil {
				return err
			}
			text := c.Text
			if c.Enrichment != "" {
				text += "\n" + c.Enrichment
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO chunk_fts(rowid,text,text_stem) VALUES (?,?,?)`, rowid, text, s.stem.Text(text)); err != nil {
				return err
			}
		}
		if published {
			return s.reindexBlob(ctx, tx, itemID, blobID)
		}
		return nil
	})
}

func (s *Store) PublishContent(ctx context.Context, itemID, blobID, generation string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := deleteChunks(ctx, tx, `item_id=? AND blob_id=? AND generation<>?`, itemID, blobID, generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chunks SET published=1 WHERE item_id=? AND blob_id=? AND generation=?`, itemID, blobID, generation); err != nil {
			return err
		}
		return s.reindexBlob(ctx, tx, itemID, blobID)
	})
}

// reindexBlob rewrites the words of the item's documents of a file version.
func (s *Store) reindexBlob(ctx context.Context, tx *sql.Tx, itemID, blobID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT rowid, name, keywords, body FROM documents WHERE resource_id=? AND blob_id=?`, itemID, blobID)
	if err != nil {
		return err
	}
	type doc struct {
		rowid                int64
		name, keywords, body string
	}
	var docs []doc
	for rows.Next() {
		var d doc
		if err = rows.Scan(&d.rowid, &d.name, &d.keywords, &d.body); err != nil {
			break
		}
		docs = append(docs, d)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, d := range docs {
		if _, err = tx.ExecContext(ctx, `DELETE FROM doc_fts WHERE rowid=?`, d.rowid); err != nil {
			return err
		}
		if err = s.indexDocument(ctx, tx, d.rowid, d.name, d.keywords, d.body, itemID, blobID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteContent(ctx context.Context, itemID string, keep []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		return deleteChunks(ctx, tx, `item_id=? AND blob_id NOT IN (SELECT value FROM json_each(?))`, itemID, jsonList(keep))
	})
}

func (s *Store) Content(ctx context.Context, itemID, blobID string) (search.Content, error) {
	var c search.Content
	var generation sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT max(generation), count(*) FROM chunks WHERE item_id=? AND blob_id=? AND published=1`, itemID, blobID).Scan(&generation, &c.Chunks)
	c.Generation = generation.String
	return c, err
}

func (s *Store) GenerationChunks(ctx context.Context, generation string) ([]search.StagedChunk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT part, ordinal, page, kind, text, enrichment FROM chunks WHERE generation=? ORDER BY part, ordinal`, generation)
	if err != nil {
		return nil, err
	}
	var out []search.StagedChunk
	for rows.Next() {
		var c search.StagedChunk
		if err = rows.Scan(&c.Part, &c.Ordinal, &c.Page, &c.Kind, &c.Text, &c.Enrichment); err != nil {
			break
		}
		out = append(out, c)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

func (s *Store) SetEnrichments(ctx context.Context, generation string, enrichments []search.Enrichment) error {
	if len(enrichments) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		blobs := map[[2]string]bool{}
		for _, e := range enrichments {
			var rowid int64
			var item, blob, kind, text string
			var published bool
			err := tx.QueryRowContext(ctx, `SELECT rowid, item_id, blob_id, kind, text, published FROM chunks WHERE generation=? AND part=? AND ordinal=?`, generation, e.Part, e.Ordinal).
				Scan(&rowid, &item, &blob, &kind, &text, &published)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			c := search.Chunk{Kind: kind, Text: text, Enrichment: e.Text}
			if _, err = tx.ExecContext(ctx, `UPDATE chunks SET enrichment=?, input_hash=? WHERE rowid=?`, e.Text, c.Hash(), rowid); err != nil {
				return err
			}
			words := search.EmbeddingInput(text, e.Text)
			if _, err = tx.ExecContext(ctx, `DELETE FROM chunk_fts WHERE rowid=?`, rowid); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO chunk_fts(rowid,text,text_stem) VALUES (?,?,?)`, rowid, words, s.stem.Text(words)); err != nil {
				return err
			}
			if published {
				blobs[[2]string{item, blob}] = true
			}
		}
		for b := range blobs {
			if err := s.reindexBlob(ctx, tx, b[0], b[1]); err != nil {
				return err
			}
		}
		return nil
	})
}
