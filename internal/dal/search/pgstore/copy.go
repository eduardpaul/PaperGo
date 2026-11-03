package pgstore

import (
	"context"
	"database/sql"
	"errors"

	"papergo/internal/search"
)

func (s *Store) Export(ctx context.Context, yield func(search.Record) error) error {
	return s.read(ctx, func(tx tx) error {
		// Documents go in batches, each read before its terms and fields: a
		// connection runs one query at a time.
		var after int64
		for {
			rows, err := tx.query(ctx, `SELECT archived,archived_at,archived_by,id,resource_id,surface,kind,views,workspace_id,collection_id,parent_id,content_type_id,scope_id,revision_id,blob_id,name,keywords,body,language,created_by,created_at,modified_by,modified_at,stamp
 FROM documents WHERE id>? ORDER BY id LIMIT 500`, after)
			if err != nil {
				return err
			}
			var docs []search.Record
			var ids []int64
			for rows.Next() {
				var id, created, modified, stamp int64
				var archived sql.NullInt64
				var d search.Document
				if err = rows.Scan(&d.Archived, &archived, &d.ArchivedBy, &id, &d.ResourceID, &d.Surface, &d.Kind, &d.Views, &d.WorkspaceID, &d.CollectionID, &d.ParentID, &d.ContentTypeID, &d.ScopeID, &d.RevisionID, &d.BlobID, &d.Name, &d.Keywords, &d.Body, &d.Language, &d.CreatedBy, &created, &d.ModifiedBy, &modified, &stamp); err != nil {
					break
				}
				d.ArchivedAt = archiveTime(archived)
				d.CreatedAt, d.ModifiedAt = fromNanos(created), fromNanos(modified)
				docs, ids = append(docs, search.Record{Document: &d, Stamp: stamp}), append(ids, id)
			}
			if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
				return err
			}
			if len(docs) == 0 {
				break
			}
			if err = yieldDocuments(ctx, tx, docs, ids, yield); err != nil {
				return err
			}
			after = ids[len(ids)-1]
		}
		rows, err := tx.query(ctx, `SELECT item_id,blob_id,generation,part,ordinal,published,page,kind,text,enrichment,input_hash,language FROM chunks ORDER BY generation COLLATE "C", part COLLATE "C", ordinal`)
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
		rows, err = tx.query(ctx, `SELECT model,input_hash,vector,unused_since FROM vectors ORDER BY model COLLATE "C", input_hash COLLATE "C"`)
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
			if v.Vector, err = search.DecodeVector(raw); err != nil {
				break
			}
			if unused.Valid {
				t := fromNanos(unused.Int64)
				v.UnusedSince = &t
			}
			if err = yield(search.Record{Vector: &v}); err != nil {
				break
			}
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
}

// yieldDocuments reads the terms, keywords and fields of exported documents
// and yields them.
func yieldDocuments(ctx context.Context, tx tx, docs []search.Record, ids []int64, yield func(search.Record) error) error {
	if len(docs) == 0 {
		return nil
	}
	at := make(map[int64]*search.Document, len(ids))
	for i, id := range ids {
		at[id] = docs[i].Document
	}
	for _, list := range []struct {
		query string
		into  func(*search.Document) *[]string
	}{
		{`SELECT doc, term_id FROM document_terms WHERE doc = ANY(?::bigint[]) ORDER BY doc, term_id COLLATE "C"`, func(d *search.Document) *[]string { return &d.TermIDs }},
		{`SELECT doc, keyword FROM document_keywords WHERE doc = ANY(?::bigint[]) ORDER BY doc, keyword COLLATE "C"`, func(d *search.Document) *[]string { return &d.KeywordIDs }},
	} {
		rows, err := tx.query(ctx, list.query, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var value string
			if err = rows.Scan(&id, &value); err != nil {
				break
			}
			values := list.into(at[id])
			*values = append(*values, value)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
	}
	rows, err := tx.query(ctx, `SELECT doc,key,type,scale,text,int FROM document_fields WHERE doc = ANY(?::bigint[]) ORDER BY doc, ordinal`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var f search.Field
		var text sql.NullString
		var number sql.NullInt64
		if err = rows.Scan(&id, &f.Key, &f.Type, &f.Scale, &text, &number); err != nil {
			break
		}
		f.Values = []search.FieldValue{{Text: text.String, Int: number.Int64}}
		at[id].Fields = append(at[id].Fields, f)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, r := range docs {
		if err = yield(r); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Import(ctx context.Context, records []search.Record) error {
	return s.tx(ctx, func(tx tx) error {
		var stamp int64
		if err := tx.row(ctx, `SELECT nextval('vector_stamps')`).Scan(&stamp); err != nil {
			return err
		}
		for _, r := range records {
			switch {
			case r.Document != nil:
				if err := s.upsertDocuments(ctx, tx, []search.Document{*r.Document}, r.Stamp); err != nil {
					return err
				}
			case r.Chunk != nil:
				c := r.Chunk
				// Replace an individual chunk, preserving the rest of its part
				// across copy batches and retries.
				if _, err := tx.exec(ctx, `DELETE FROM chunks WHERE generation=? AND part=? AND ordinal=?`, c.Generation, c.Chunk.Part, c.Chunk.Ordinal); err != nil {
					return err
				}
				if err := s.insertChunk(ctx, tx, c.ItemID, c.BlobID, c.Generation, c.Published, c.Chunk); err != nil {
					return err
				}
			case r.Vector != nil:
				v := r.Vector
				var unused any
				if v.UnusedSince != nil {
					unused = v.UnusedSince.UnixNano()
				}
				if _, err := tx.exec(ctx, `INSERT INTO vectors(model,input_hash,dims,vector,stamp,unused_since) VALUES(?,?,?,?,?,?) ON CONFLICT(model,input_hash) DO NOTHING`, v.Model, v.InputHash, len(v.Vector), search.VectorBytes(v.Vector), stamp, unused); err != nil {
					return err
				}
			default:
				return errors.New("empty search export record")
			}
		}
		return nil
	})
}
