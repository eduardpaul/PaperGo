-- The search store's own SQLite file (search.db): derived data that a
-- reindex rebuilds. A different schema_version recreates the file.
PRAGMA application_id = 1346851657; -- PGSI: PaperGo search index
CREATE TABLE state (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE documents (
 rowid INTEGER PRIMARY KEY,
 resource_id TEXT NOT NULL, surface TEXT NOT NULL, kind TEXT NOT NULL, views INTEGER NOT NULL,
 workspace_id TEXT NOT NULL, collection_id TEXT NOT NULL, parent_id TEXT NOT NULL, content_type_id TEXT NOT NULL,
 scope_id TEXT NOT NULL, revision_id TEXT NOT NULL, blob_id TEXT NOT NULL,
 name TEXT NOT NULL, keywords TEXT NOT NULL, body TEXT NOT NULL, language TEXT NOT NULL,
 created_by TEXT NOT NULL, created_at INTEGER NOT NULL, modified_by TEXT NOT NULL, modified_at INTEGER NOT NULL,
 stamp INTEGER NOT NULL,
 UNIQUE (resource_id, surface)
);
CREATE INDEX documents_scope ON documents (scope_id, views);
CREATE INDEX documents_collection ON documents (collection_id, resource_id);
CREATE INDEX documents_workspace ON documents (workspace_id, modified_at);
CREATE INDEX documents_modified ON documents (modified_at, resource_id);
CREATE INDEX documents_stamp ON documents (stamp);
CREATE TABLE document_terms (term_id TEXT NOT NULL, doc INTEGER NOT NULL REFERENCES documents(rowid) ON DELETE CASCADE, PRIMARY KEY (term_id, doc)) WITHOUT ROWID;
CREATE INDEX document_terms_doc ON document_terms (doc);
CREATE TABLE document_tags (tag TEXT NOT NULL, doc INTEGER NOT NULL REFERENCES documents(rowid) ON DELETE CASCADE, PRIMARY KEY (tag, doc)) WITHOUT ROWID;
CREATE INDEX document_tags_doc ON document_tags (doc);
-- Typed field values, encoded as PaperGo's field index encodes them.
CREATE TABLE document_fields (
 doc INTEGER NOT NULL REFERENCES documents(rowid) ON DELETE CASCADE,
 key TEXT NOT NULL, type TEXT NOT NULL, scale INTEGER NOT NULL, ordinal INTEGER NOT NULL,
 text TEXT, int INTEGER,
 PRIMARY KEY (doc, key, ordinal)
) WITHOUT ROWID;
CREATE INDEX document_fields_text ON document_fields (key, type, scale, text);
CREATE INDEX document_fields_int ON document_fields (key, type, scale, int);
-- The words of each document, without a copy of its text: heading holds the
-- name and keywords (ranked higher), body the metadata, file_text the file.
-- The stem columns hold the same words as stems, once per configured language.
CREATE VIRTUAL TABLE doc_fts USING fts5(heading, body, file_text, heading_stem, body_stem, file_stem, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2');
-- File content: chunks of the text of an item's file version (blob), under
-- a generation; one generation per (item, blob) is published. Staged chunks
-- are not searched. A part is the workflow step that staged them.
CREATE TABLE chunks (
 rowid INTEGER PRIMARY KEY,
 item_id TEXT NOT NULL, blob_id TEXT NOT NULL, generation TEXT NOT NULL, part TEXT NOT NULL, ordinal INTEGER NOT NULL,
 published INTEGER NOT NULL DEFAULT 0, page INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL, enrichment TEXT NOT NULL,
 input_hash TEXT NOT NULL, language TEXT NOT NULL,
 UNIQUE (generation, part, ordinal)
);
CREATE INDEX chunks_item ON chunks (item_id, blob_id, published);
CREATE INDEX chunks_hash ON chunks (input_hash);
-- The words of each chunk, for page hits and snippets.
CREATE VIRTUAL TABLE chunk_fts USING fts5(text, text_stem, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2');
-- Embedding vectors, per model and chunk input (input_hash): reused by every
-- chunk with the same input, across items and reindexes. Normalized float32,
-- little-endian. stamp orders writes, so the in-memory index loads only what
-- changed; unused_since marks vectors no chunk (or another model) uses, which
-- are deleted after a while.
CREATE TABLE vectors (
 rowid INTEGER PRIMARY KEY,
 model TEXT NOT NULL, input_hash TEXT NOT NULL, dims INTEGER NOT NULL, vector BLOB NOT NULL,
 stamp INTEGER NOT NULL, unused_since INTEGER,
 UNIQUE (model, input_hash)
);
CREATE INDEX vectors_stamp ON vectors (model, stamp);
