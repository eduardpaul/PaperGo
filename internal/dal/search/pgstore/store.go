// Package pgstore is the search store of PostgreSQL deployments: its tables
// live in a schema of their own (papergo_search) in PaperGo's database, with
// GIN-indexed tsvectors for words and vectors searched in memory. It needs
// no PostgreSQL extension.
package pgstore

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"papergo/internal/search"
)

// Name is the store's SEARCH_STORE name.
const Name = "postgres"

// Schema is the PostgreSQL schema that holds the store.
const Schema = "papergo_search"

//go:embed schema.sql
var schema string

// schemaVersion changes with schema.sql or with how words are indexed: a
// schema of another version is dropped and created again, and a reindex
// refills it.
const schemaVersion = "1"

// writeLock is the transaction advisory lock every write takes, so writes of
// any process run one at a time, as on SQLite: "PGSW", PaperGo search write.
const writeLock = 1346851671

// Store is the PostgreSQL search store.
type Store struct {
	db *sql.DB
	// fresh is set when Open created the schema: the index is empty.
	fresh bool
	stem  *search.Stemmer
	// vectors is the in-memory index of semantic search.
	vectors search.VectorIndex
}

// Options configure the store.
type Options struct {
	// Languages are the Snowball languages whose stems are indexed
	// (SEARCH_LANGUAGES); none indexes words only.
	Languages []string
}

// Open opens the store in the database of db, creating its schema, or
// recreating it when it is of another version or was built for other
// languages. The caller keeps db open while the store is used, and closes it.
func Open(ctx context.Context, db *sql.DB, opts Options) (*Store, error) {
	stem, err := search.NewStemmer(opts.Languages)
	if err != nil {
		return nil, err
	}
	languages := strings.Join(stem.Languages(), ",")
	s := &Store{db: db, stem: stem}
	// Instances starting together create the schema once.
	err = s.tx(ctx, func(tx tx) error {
		var exists, ours bool
		if err := tx.row(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=?), to_regclass(?) IS NOT NULL`, Schema, Schema+".state").Scan(&exists, &ours); err != nil {
			return err
		}
		if exists && ours {
			var version, built string
			err := tx.row(ctx, `SELECT coalesce((SELECT value FROM state WHERE key='schema_version'),''), coalesce((SELECT value FROM state WHERE key='languages'),'')`).Scan(&version, &built)
			if err != nil {
				return err
			}
			if version == schemaVersion && built == languages {
				return nil
			}
		}
		if exists && !ours {
			var objects int
			if err := tx.row(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=?`, Schema).Scan(&objects); err != nil {
				return err
			}
			if objects != 0 {
				return fmt.Errorf("schema %s holds tables that are not a PaperGo search store", Schema)
			}
		}
		// Another schema: the store is derived data, so start over.
		if _, err := tx.exec(ctx, `DROP SCHEMA IF EXISTS `+Schema+` CASCADE`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := tx.exec(ctx, `INSERT INTO state(key,value) VALUES('schema_version',?),('languages',?)`, schemaVersion, languages); err != nil {
			return err
		}
		s.fresh = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open the search store: %w", err)
	}
	return s, nil
}

// Fresh reports whether Open created the store: it needs a reindex.
func (s *Store) Fresh() bool { return s.fresh }

func (s *Store) Name() string { return Name }

// Close leaves the database handle to its owner.
func (s *Store) Close() error { return nil }

// tx is a transaction whose statements name the store's tables unqualified
// and write ? for their parameters.
type tx struct{ *sql.Tx }

// rebind numbers the ? parameters of a statement, as PostgreSQL writes
// them. Statements hold no other question marks: text and words are
// parameters.
func rebind(statement string) string {
	if !strings.Contains(statement, "?") {
		return statement
	}
	var b strings.Builder
	n := 0
	for _, r := range statement {
		if r != '?' {
			b.WriteRune(r)
			continue
		}
		n++
		b.WriteString("$" + strconv.Itoa(n))
	}
	return b.String()
}

func (t tx) exec(ctx context.Context, statement string, args ...any) (sql.Result, error) {
	return t.ExecContext(ctx, rebind(statement), args...)
}

func (t tx) query(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	return t.QueryContext(ctx, rebind(statement), args...)
}

func (t tx) row(ctx context.Context, statement string, args ...any) *sql.Row {
	return t.QueryRowContext(ctx, rebind(statement), args...)
}

// tx runs fn in a write transaction that holds the store's write lock.
func (s *Store) tx(ctx context.Context, fn func(tx) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = t.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1), set_config('search_path', $2, true)`, writeLock, Schema); err == nil {
		err = fn(tx{t})
	}
	if err != nil {
		return errors.Join(err, t.Rollback())
	}
	return t.Commit()
}

// read runs fn in one read-only transaction, so counts, pages and facets
// agree.
func (s *Store) read(ctx context.Context, fn func(tx) error) error {
	t, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer t.Rollback()
	if _, err = t.ExecContext(ctx, `SELECT set_config('search_path', $1, true)`, Schema); err != nil {
		return err
	}
	if err = fn(tx{t}); err != nil {
		return err
	}
	return t.Commit()
}

func (s *Store) UpsertDocuments(ctx context.Context, docs []search.Document, stamp int64) error {
	if len(docs) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx tx) error {
		return s.upsertDocuments(ctx, tx, docs, stamp)
	})
}

func (s *Store) upsertDocuments(ctx context.Context, tx tx, docs []search.Document, stamp int64) error {
	for _, d := range docs {
		name, keywords, body := clean(d.Name), clean(d.Keywords), clean(d.Body)
		var id int64
		err := tx.row(ctx, `INSERT INTO documents (archived,archived_at,archived_by,resource_id,surface,kind,views,workspace_id,collection_id,parent_id,content_type_id,scope_id,revision_id,blob_id,
 name,keywords,body,language,created_by,created_at,modified_by,modified_at,stamp,words) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?::tsvector)
 ON CONFLICT(resource_id,surface) DO UPDATE SET archived=excluded.archived,archived_at=excluded.archived_at,archived_by=excluded.archived_by,kind=excluded.kind,views=excluded.views,workspace_id=excluded.workspace_id,collection_id=excluded.collection_id,
 parent_id=excluded.parent_id,content_type_id=excluded.content_type_id,scope_id=excluded.scope_id,revision_id=excluded.revision_id,blob_id=excluded.blob_id,
 name=excluded.name,keywords=excluded.keywords,body=excluded.body,language=excluded.language,created_by=excluded.created_by,created_at=excluded.created_at,
 modified_by=excluded.modified_by,modified_at=excluded.modified_at,stamp=excluded.stamp,words=excluded.words RETURNING id`,
			d.Archived, archiveNanos(d.ArchivedAt), d.ArchivedBy, d.ResourceID, d.Surface, d.Kind, int(d.Views), d.WorkspaceID, d.CollectionID, d.ParentID, d.ContentTypeID, d.ScopeID, d.RevisionID, d.BlobID,
			name, keywords, body, d.Language, d.CreatedBy, d.CreatedAt.UnixNano(), d.ModifiedBy, d.ModifiedAt.UnixNano(), stamp, documentWords(s.stem, name, keywords, body)).Scan(&id)
		if err != nil {
			return err
		}
		for _, statement := range []string{`DELETE FROM document_terms WHERE doc=?`, `DELETE FROM document_keywords WHERE doc=?`, `DELETE FROM document_fields WHERE doc=?`} {
			if _, err = tx.exec(ctx, statement, id); err != nil {
				return err
			}
		}
		ordinal := 0
		for _, f := range d.Fields {
			for _, v := range f.Values {
				var text, number any
				if search.IntegerType(f.Type) {
					number = v.Int
				} else {
					text = clean(v.Text)
				}
				// Ordinals count across fields, so a key repeated in Fields keeps every value.
				if _, err = tx.exec(ctx, `INSERT INTO document_fields(doc,key,type,scale,ordinal,text,int) VALUES (?,?,?,?,?,?,?)`, id, f.Key, f.Type, f.Scale, ordinal, text, number); err != nil {
					return err
				}
				ordinal++
			}
		}
		if terms := search.Unique(d.TermIDs); len(terms) > 0 {
			if _, err = tx.exec(ctx, `INSERT INTO document_terms(term_id,doc) SELECT unnest(?::text[]), ?`, terms, id); err != nil {
				return err
			}
		}
		if keywords := search.Unique(d.KeywordIDs); len(keywords) > 0 {
			if _, err = tx.exec(ctx, `INSERT INTO document_keywords(keyword,doc) SELECT unnest(?::text[]), ?`, keywords, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DeleteDocuments(ctx context.Context, sel search.Selection) error {
	return s.tx(ctx, func(tx tx) error {
		// Terms, keywords and fields go with their documents.
		if len(sel.ResourceIDs) > 0 {
			if _, err := tx.exec(ctx, `DELETE FROM documents WHERE resource_id = ANY(?::text[])`, sel.ResourceIDs); err != nil {
				return err
			}
			if _, err := tx.exec(ctx, `DELETE FROM chunks WHERE item_id = ANY(?::text[])`, sel.ResourceIDs); err != nil {
				return err
			}
		}
		for _, k := range sel.Keys {
			if _, err := tx.exec(ctx, `DELETE FROM documents WHERE resource_id=? AND surface=?`, k.ResourceID, k.Surface); err != nil {
				return err
			}
		}
		if sel.Before > 0 {
			if _, err := tx.exec(ctx, `DELETE FROM documents WHERE stamp<?`, sel.Before); err != nil {
				return err
			}
			// Content of entries without documents left.
			_, err := tx.exec(ctx, `DELETE FROM chunks c WHERE NOT EXISTS (SELECT 1 FROM documents d WHERE d.resource_id=c.item_id)`)
			return err
		}
		return nil
	})
}

func (s *Store) SetScopes(ctx context.Context, scopes map[string]string) error {
	return s.tx(ctx, func(tx tx) error {
		for id, scope := range scopes {
			if _, err := tx.exec(ctx, `UPDATE documents SET scope_id=? WHERE resource_id=? AND scope_id<>?`, scope, id, scope); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Keys(ctx context.Context, resourceIDs []string) ([]search.Key, error) {
	var out []search.Key
	err := s.read(ctx, func(tx tx) error {
		rows, err := tx.query(ctx, `SELECT resource_id, surface FROM documents WHERE resource_id = ANY(?::text[]) ORDER BY resource_id, surface`, list(resourceIDs))
		if err != nil {
			return err
		}
		for rows.Next() {
			var k search.Key
			if err = rows.Scan(&k.ResourceID, &k.Surface); err != nil {
				break
			}
			out = append(out, k)
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
	return out, err
}

func (s *Store) Stats(ctx context.Context) (search.Stats, error) {
	var st search.Stats
	err := s.read(ctx, func(tx tx) error {
		return tx.row(ctx, `SELECT (SELECT count(*) FROM documents), (SELECT count(*) FROM chunks WHERE published),
 (SELECT coalesce(sum(pg_total_relation_size(c.oid)),0) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=? AND c.relkind='r')`, Schema).
			Scan(&st.Documents, &st.Chunks, &st.Bytes)
	})
	return st, err
}

// list is a parameter for a text[]: never NULL.
func list(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func fromNanos(n int64) time.Time { return time.Unix(0, n).UTC() }

func archiveNanos(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

func archiveTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromNanos(n.Int64)
	return &t
}
