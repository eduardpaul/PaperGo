// Package sqlitestore is the default search store: an SQLite file of its own
// (search.db) with an FTS5 index, so indexing never waits on PaperGo's
// database writer and never blocks it.
package sqlitestore

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"papergo/internal/dal/database"
	"papergo/internal/search"
)

// Name is the store's SEARCH_STORE name.
const Name = "sqlite"

//go:embed schema.sql
var schema string

// schemaVersion changes with schema.sql: a file of another version is
// recreated, and a reindex refills it.
const schemaVersion = "5"

const applicationID = 1346851657

// Store is the SQLite search store.
type Store struct {
	db   *sql.DB
	path string
	// write serializes this process's writes, which take SQLite's lock when
	// they begin.
	write sync.Mutex
	// fresh is set when Open created the file: the index is empty.
	fresh bool
	stem  *search.Stemmer
	// vectors is the in-memory index of semantic search.
	vectors vectorIndex
}

// Options configure the store.
type Options struct {
	// Languages are the Snowball languages whose stems are indexed
	// (SEARCH_LANGUAGES); none indexes words only.
	Languages []string
	// NoRebuild refuses missing or incompatible stores (offline copy sources).
	NoRebuild bool
}

// Open opens the store at path, creating it, or recreating it when its
// schema is of another version or it was built for other languages.
func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	stem, err := search.NewStemmer(opts.Languages)
	if err != nil {
		return nil, err
	}
	languages := strings.Join(stem.Languages(), ",")
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if opts.NoRebuild {
		if info, err := os.Stat(abs); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("source search store is missing")
		}
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	s, err := open(ctx, abs, stem)
	if err != nil {
		return nil, err
	}
	var owner, tables int
	if err = s.db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&owner); err == nil && owner != applicationID {
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&tables)
		if err == nil && tables != 0 {
			err = fmt.Errorf("%s is not a PaperGo search store", abs)
		}
	}
	if err != nil {
		s.db.Close()
		return nil, err
	}
	var version, built string
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT value FROM state WHERE key='schema_version'), coalesce((SELECT value FROM state WHERE key='languages'),'')`).Scan(&version, &built)
	switch {
	case err == nil && version == schemaVersion && built == languages:
		return s, nil
	case err == nil || strings.Contains(err.Error(), "no such table"):
		if opts.NoRebuild {
			s.db.Close()
			return nil, errors.New("source search schema or languages require a reindex")
		}
		// Another schema: the store is derived data, so start over.
		s.db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if e := os.Remove(abs + suffix); e != nil && !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
		}
		if s, err = open(ctx, abs, stem); err != nil {
			return nil, err
		}
		if _, err = s.db.ExecContext(ctx, schema); err == nil {
			_, err = s.db.ExecContext(ctx, `INSERT INTO state(key,value) VALUES('schema_version',?),('languages',?)`, schemaVersion, languages)
		}
		if err != nil {
			s.db.Close()
			return nil, fmt.Errorf("create the search store: %w", err)
		}
		s.fresh = true
		return s, nil
	default:
		s.db.Close()
		return nil, err
	}
}

func open(ctx context.Context, abs string, stem *search.Stemmer) (*Store, error) {
	db, err := sql.Open("sqlite", database.DSN(abs, database.DefaultOptions))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open the search store: %w", err)
	}
	return &Store{db: db, path: abs, stem: stem}, nil
}

// Fresh reports whether Open created the store: it needs a reindex.
func (s *Store) Fresh() bool { return s.fresh }

// Path is the store's file.
func (s *Store) Path() string { return s.path }

// DB is the store's handle, for backups.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Name() string { return Name }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	s.write.Lock()
	defer s.write.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func (s *Store) UpsertDocuments(ctx context.Context, docs []search.Document, stamp int64) error {
	if len(docs) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		return s.upsertDocuments(ctx, tx, docs, stamp)
	})
}

func (s *Store) upsertDocuments(ctx context.Context, tx *sql.Tx, docs []search.Document, stamp int64) error {
	for _, d := range docs {
		var rowid int64
		err := tx.QueryRowContext(ctx, `INSERT INTO documents (resource_id,surface,kind,views,workspace_id,collection_id,parent_id,content_type_id,scope_id,revision_id,blob_id,
 name,keywords,body,language,created_by,created_at,modified_by,modified_at,stamp) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(resource_id,surface) DO UPDATE SET kind=excluded.kind,views=excluded.views,workspace_id=excluded.workspace_id,collection_id=excluded.collection_id,
 parent_id=excluded.parent_id,content_type_id=excluded.content_type_id,scope_id=excluded.scope_id,revision_id=excluded.revision_id,blob_id=excluded.blob_id,
 name=excluded.name,keywords=excluded.keywords,body=excluded.body,language=excluded.language,created_by=excluded.created_by,created_at=excluded.created_at,
 modified_by=excluded.modified_by,modified_at=excluded.modified_at,stamp=excluded.stamp RETURNING rowid`,
			d.ResourceID, d.Surface, d.Kind, int(d.Views), d.WorkspaceID, d.CollectionID, d.ParentID, d.ContentTypeID, d.ScopeID, d.RevisionID, d.BlobID,
			d.Name, d.Keywords, d.Body, d.Language, d.CreatedBy, d.CreatedAt.UnixNano(), d.ModifiedBy, d.ModifiedAt.UnixNano(), stamp).Scan(&rowid)
		if err != nil {
			return err
		}
		for _, statement := range []string{`DELETE FROM doc_fts WHERE rowid=?`, `DELETE FROM document_terms WHERE doc=?`, `DELETE FROM document_keywords WHERE doc=?`, `DELETE FROM document_fields WHERE doc=?`} {
			if _, err = tx.ExecContext(ctx, statement, rowid); err != nil {
				return err
			}
		}
		if err = s.indexDocument(ctx, tx, rowid, d.Name, d.Keywords, d.Body, d.ResourceID, d.BlobID); err != nil {
			return err
		}
		ordinal := 0
		for _, f := range d.Fields {
			for _, v := range f.Values {
				var text, number any
				if search.IntegerType(f.Type) {
					number = v.Int
				} else {
					text = v.Text
				}
				// Ordinals count across fields, so a key repeated in Fields keeps every value.
				if _, err = tx.ExecContext(ctx, `INSERT INTO document_fields(doc,key,type,scale,ordinal,text,int) VALUES (?,?,?,?,?,?,?)`, rowid, f.Key, f.Type, f.Scale, ordinal, text, number); err != nil {
					return err
				}
				ordinal++
			}
		}
		for _, id := range unique(d.TermIDs) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO document_terms(term_id,doc) VALUES (?,?)`, id, rowid); err != nil {
				return err
			}
		}
		for _, keyword := range unique(d.KeywordIDs) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO document_keywords(keyword,doc) VALUES (?,?)`, keyword, rowid); err != nil {
				return err
			}
		}
	}
	return nil
}

// indexDocument writes a document's words: the name and keywords, and its
// body separately from the published content of its file version.
func (s *Store) indexDocument(ctx context.Context, tx *sql.Tx, rowid int64, name, keywords, body, resourceID, blobID string) error {
	var content string
	if blobID != "" {
		var err error
		content, err = publishedText(ctx, tx, resourceID, blobID)
		if err != nil {
			return err
		}
	}
	heading := name + "\n" + keywords
	_, err := tx.ExecContext(ctx, `INSERT INTO doc_fts(rowid,heading,body,file_text,heading_stem,body_stem,file_stem) VALUES (?,?,?,?,?,?,?)`, rowid, heading, body, content, s.stem.Text(heading), s.stem.Text(body), s.stem.Text(content))
	return err
}

// publishedText is the text of a file version's published chunks.
func publishedText(ctx context.Context, tx *sql.Tx, itemID, blobID string) (string, error) {
	var text sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT group_concat(text || CASE WHEN enrichment<>'' THEN char(10) || enrichment ELSE '' END, char(10))
 FROM (SELECT text, enrichment FROM chunks WHERE item_id=? AND blob_id=? AND published=1 ORDER BY part, ordinal)`, itemID, blobID).Scan(&text)
	return text.String, err
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := values[:0:0]
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// deleteRows removes the documents selected by where, with their index rows.
func deleteRows(ctx context.Context, tx *sql.Tx, where string, args ...any) error {
	rows, err := tx.QueryContext(ctx, `SELECT rowid FROM documents WHERE `+where, args...)
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
		for _, statement := range []string{`DELETE FROM doc_fts WHERE rowid=?`, `DELETE FROM document_terms WHERE doc=?`, `DELETE FROM document_keywords WHERE doc=?`, `DELETE FROM document_fields WHERE doc=?`, `DELETE FROM documents WHERE rowid=?`} {
			if _, err = tx.ExecContext(ctx, statement, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DeleteDocuments(ctx context.Context, sel search.Selection) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if len(sel.ResourceIDs) > 0 {
			if err := deleteRows(ctx, tx, `resource_id IN (SELECT value FROM json_each(?))`, jsonList(sel.ResourceIDs)); err != nil {
				return err
			}
			if err := deleteChunks(ctx, tx, `item_id IN (SELECT value FROM json_each(?))`, jsonList(sel.ResourceIDs)); err != nil {
				return err
			}
		}
		for _, k := range sel.Keys {
			if err := deleteRows(ctx, tx, `resource_id=? AND surface=?`, k.ResourceID, k.Surface); err != nil {
				return err
			}
		}
		if sel.Before > 0 {
			if err := deleteRows(ctx, tx, `stamp<?`, sel.Before); err != nil {
				return err
			}
			// Content of entries without documents left.
			return deleteChunks(ctx, tx, `item_id NOT IN (SELECT resource_id FROM documents)`)
		}
		return nil
	})
}

func (s *Store) SetScopes(ctx context.Context, scopes map[string]string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for id, scope := range scopes {
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET scope_id=? WHERE resource_id=? AND scope_id<>?`, scope, id, scope); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Keys(ctx context.Context, resourceIDs []string) ([]search.Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT resource_id, surface FROM documents WHERE resource_id IN (SELECT value FROM json_each(?)) ORDER BY resource_id, surface`, jsonList(resourceIDs))
	if err != nil {
		return nil, err
	}
	var out []search.Key
	for rows.Next() {
		var k search.Key
		if err = rows.Scan(&k.ResourceID, &k.Surface); err != nil {
			break
		}
		out = append(out, k)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}

func (s *Store) Stats(ctx context.Context) (search.Stats, error) {
	var st search.Stats
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM chunks WHERE published=1)`).Scan(&st.Documents, &st.Chunks); err != nil {
		return st, err
	}
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(s.path + suffix); err == nil {
			st.Bytes += info.Size()
		}
	}
	return st, nil
}

func jsonList(values []string) string {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func fromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }
