package pgstore

import (
	"context"
	"database/sql"
	"errors"

	"papergo/internal/search"
)

// insertChunk writes a chunk with its words.
func (s *Store) insertChunk(ctx context.Context, tx tx, itemID, blobID, generation string, published bool, c search.Chunk) error {
	text, enrichment := clean(c.Text), clean(c.Enrichment)
	_, err := tx.exec(ctx, `INSERT INTO chunks (item_id,blob_id,generation,part,ordinal,published,page,kind,text,enrichment,input_hash,language,words)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?::tsvector)`, itemID, blobID, generation, c.Part, c.Ordinal, published, c.Page, c.Kind, text, enrichment, c.InputHash, c.Language,
		chunkWords(s.stem, search.EmbeddingInput(text, enrichment)))
	return err
}

func (s *Store) StageContent(ctx context.Context, itemID, blobID, generation string, chunks []search.Chunk) error {
	if itemID == "" || blobID == "" || generation == "" {
		return errors.New("content needs an item, a file version and a generation")
	}
	return s.tx(ctx, func(tx tx) error {
		parts := map[string]bool{}
		for _, c := range chunks {
			parts[c.Part] = true
		}
		for part := range parts {
			if _, err := tx.exec(ctx, `DELETE FROM chunks WHERE generation=? AND part=?`, generation, part); err != nil {
				return err
			}
		}
		// A generation stays unpublished while it grows, also when an
		// earlier part was published.
		var published bool
		if err := tx.row(ctx, `SELECT EXISTS(SELECT 1 FROM chunks WHERE generation=? AND published)`, generation).Scan(&published); err != nil {
			return err
		}
		for _, c := range chunks {
			if c.InputHash == "" {
				c.InputHash = c.Hash()
			}
			if err := s.insertChunk(ctx, tx, itemID, blobID, generation, published, c); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) PublishContent(ctx context.Context, itemID, blobID, generation string) error {
	return s.tx(ctx, func(tx tx) error {
		var newer bool
		if err := tx.row(ctx, `SELECT EXISTS (SELECT 1 FROM chunks WHERE item_id=? AND blob_id=? AND generation>? COLLATE "C" AND published)`, itemID, blobID, generation).Scan(&newer); err != nil {
			return err
		}
		if newer {
			return search.ErrSuperseded
		}
		if _, err := tx.exec(ctx, `DELETE FROM chunks WHERE item_id=? AND blob_id=? AND generation<? COLLATE "C"`, itemID, blobID, generation); err != nil {
			return err
		}
		_, err := tx.exec(ctx, `UPDATE chunks SET published=true WHERE item_id=? AND blob_id=? AND generation=?`, itemID, blobID, generation)
		return err
	})
}

func (s *Store) DeleteContent(ctx context.Context, itemID string, keep []string) error {
	return s.tx(ctx, func(tx tx) error {
		_, err := tx.exec(ctx, `DELETE FROM chunks WHERE item_id=? AND NOT blob_id = ANY(?::text[])`, itemID, list(keep))
		return err
	})
}

func (s *Store) Content(ctx context.Context, itemID, blobID string) (search.Content, error) {
	var c search.Content
	var generation sql.NullString
	err := s.read(ctx, func(tx tx) error {
		return tx.row(ctx, `SELECT max(generation COLLATE "C"), count(*) FROM chunks WHERE item_id=? AND blob_id=? AND published`, itemID, blobID).Scan(&generation, &c.Chunks)
	})
	c.Generation = generation.String
	return c, err
}

func (s *Store) GenerationChunks(ctx context.Context, generation string) ([]search.StagedChunk, error) {
	var out []search.StagedChunk
	err := s.read(ctx, func(tx tx) error {
		rows, err := tx.query(ctx, `SELECT part, ordinal, page, kind, text, enrichment FROM chunks WHERE generation=? ORDER BY part COLLATE "C", ordinal`, generation)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c search.StagedChunk
			if err = rows.Scan(&c.Part, &c.Ordinal, &c.Page, &c.Kind, &c.Text, &c.Enrichment); err != nil {
				break
			}
			out = append(out, c)
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
	return out, err
}

func (s *Store) SetEnrichments(ctx context.Context, generation string, enrichments []search.Enrichment) error {
	if len(enrichments) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx tx) error {
		for _, e := range enrichments {
			var id int64
			var kind, text string
			err := tx.row(ctx, `SELECT id, kind, text FROM chunks WHERE generation=? AND part=? AND ordinal=?`, generation, e.Part, e.Ordinal).Scan(&id, &kind, &text)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			enrichment := clean(e.Text)
			c := search.Chunk{Kind: kind, Text: text, Enrichment: enrichment}
			if _, err = tx.exec(ctx, `UPDATE chunks SET enrichment=?, input_hash=?, words=?::tsvector WHERE id=?`, enrichment, c.Hash(), chunkWords(s.stem, search.EmbeddingInput(text, enrichment)), id); err != nil {
				return err
			}
		}
		return nil
	})
}
