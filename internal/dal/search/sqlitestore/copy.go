package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"

	"papergo/internal/search"
)

func (s *Store) Export(ctx context.Context, yield func(search.Record) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT rowid,resource_id,surface,kind,views,workspace_id,collection_id,parent_id,content_type_id,scope_id,revision_id,blob_id,name,keywords,body,language,created_by,created_at,modified_by,modified_at,stamp FROM documents ORDER BY resource_id,surface`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, created, modified, stamp int64
		var d search.Document
		if err = rows.Scan(&id, &d.ResourceID, &d.Surface, &d.Kind, &d.Views, &d.WorkspaceID, &d.CollectionID, &d.ParentID, &d.ContentTypeID, &d.ScopeID, &d.RevisionID, &d.BlobID, &d.Name, &d.Keywords, &d.Body, &d.Language, &d.CreatedBy, &created, &d.ModifiedBy, &modified, &stamp); err != nil {
			break
		}
		d.CreatedAt, d.ModifiedAt = fromNanos(created), fromNanos(modified)
		if err = exportFields(ctx, tx, id, &d); err != nil {
			break
		}
		if err = yield(search.Record{Document: &d, Stamp: stamp}); err != nil {
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT item_id,blob_id,generation,part,ordinal,published,page,kind,text,enrichment,input_hash,language FROM chunks ORDER BY generation,part,ordinal`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c search.StoredChunk
		v := &c.Chunk
		if err = rows.Scan(&c.ItemID, &c.BlobID, &c.Generation, &v.Part, &v.Ordinal, &c.Published, &v.Page, &v.Kind, &v.Text, &v.Enrichment, &v.InputHash, &v.Language); err != nil {
			break
		}
		if err = yield(search.Record{Chunk: &c}); err != nil {
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT model,input_hash,vector,unused_since FROM vectors ORDER BY model,input_hash`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v search.StoredVector
		var raw []byte
		var unused sql.NullInt64
		if err = rows.Scan(&v.Model, &v.InputHash, &raw, &unused); err != nil {
			break
		}
		if len(raw)%4 != 0 {
			err = errors.New("invalid vector bytes in search store")
			break
		}
		v.Vector = make([]float32, len(raw)/4)
		for i := range v.Vector {
			v.Vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		if unused.Valid {
			t := fromNanos(unused.Int64)
			v.UnusedSince = &t
		}
		if err = yield(search.Record{Vector: &v}); err != nil {
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	return tx.Commit()
}

func exportFields(ctx context.Context, tx *sql.Tx, id int64, d *search.Document) error {
	for query, target := range map[string]*[]string{`SELECT term_id FROM document_terms WHERE doc=? ORDER BY term_id`: &d.TermIDs, `SELECT keyword FROM document_keywords WHERE doc=? ORDER BY keyword`: &d.KeywordIDs} {
		rows, err := tx.QueryContext(ctx, query, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				break
			}
			*target = append(*target, value)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,type,scale,text,int FROM document_fields WHERE doc=? ORDER BY ordinal`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var f search.Field
		var text sql.NullString
		var number sql.NullInt64
		if err = rows.Scan(&f.Key, &f.Type, &f.Scale, &text, &number); err != nil {
			break
		}
		f.Values = []search.FieldValue{{Text: text.String, Int: number.Int64}}
		d.Fields = append(d.Fields, f)
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func (s *Store) Import(ctx context.Context, records []search.Record) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var stamp int64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(stamp),0)+1 FROM vectors`).Scan(&stamp); err != nil {
			return err
		}
		blobs := map[[2]string]bool{}
		for _, r := range records {
			switch {
			case r.Document != nil:
				if err := s.upsertDocuments(ctx, tx, []search.Document{*r.Document}, r.Stamp); err != nil {
					return err
				}
			case r.Chunk != nil:
				c := r.Chunk
				v := c.Chunk
				// Replace an individual chunk, preserving the rest of its part
				// across copy batches and retries.
				if err := deleteChunks(ctx, tx, `generation=? AND part=? AND ordinal=?`, c.Generation, v.Part, v.Ordinal); err != nil {
					return err
				}
				var rowid int64
				if err := tx.QueryRowContext(ctx, `INSERT INTO chunks(item_id,blob_id,generation,part,ordinal,published,page,kind,text,enrichment,input_hash,language) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING rowid`, c.ItemID, c.BlobID, c.Generation, v.Part, v.Ordinal, c.Published, v.Page, v.Kind, v.Text, v.Enrichment, v.InputHash, v.Language).Scan(&rowid); err != nil {
					return err
				}
				text := search.EmbeddingInput(v.Text, v.Enrichment)
				if _, err := tx.ExecContext(ctx, `INSERT INTO chunk_fts(rowid,text,text_stem) VALUES(?,?,?)`, rowid, text, s.stem.Text(text)); err != nil {
					return err
				}
				if c.Published {
					blobs[[2]string{c.ItemID, c.BlobID}] = true
				}
			case r.Vector != nil:
				v := r.Vector
				var unused any
				if v.UnusedSince != nil {
					unused = v.UnusedSince.UnixNano()
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO vectors(model,input_hash,dims,vector,stamp,unused_since) VALUES(?,?,?,?,?,?) ON CONFLICT(model,input_hash) DO NOTHING`, v.Model, v.InputHash, len(v.Vector), vectorBytes(v.Vector), stamp, unused); err != nil {
					return err
				}
			default:
				return errors.New("empty search export record")
			}
		}
		for blob := range blobs {
			if err := s.reindexBlob(ctx, tx, blob[0], blob[1]); err != nil {
				return err
			}
		}
		return nil
	})
}
