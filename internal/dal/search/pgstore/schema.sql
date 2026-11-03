-- The search store's schema in PaperGo's PostgreSQL database: derived data
-- that a reindex rebuilds, kept out of the DAL schema and its migrations. A
-- different schema_version drops and recreates the schema. It needs no
-- extension: words are tsvectors of lexemes PaperGo computes, and vectors
-- are bytes searched in memory.
CREATE SCHEMA papergo_search;
CREATE TABLE papergo_search.state (key text PRIMARY KEY, value text NOT NULL);
-- Stamps of vector writes, so the in-memory index of every process loads
-- only what changed.
CREATE SEQUENCE papergo_search.vector_stamps;
CREATE TABLE papergo_search.documents (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 archived boolean NOT NULL DEFAULT false, archived_at bigint, archived_by text NOT NULL DEFAULT '',
 resource_id text NOT NULL, surface text NOT NULL, kind text NOT NULL, views integer NOT NULL,
 workspace_id text NOT NULL, collection_id text NOT NULL, parent_id text NOT NULL, content_type_id text NOT NULL,
 scope_id text NOT NULL, revision_id text NOT NULL, blob_id text NOT NULL,
 name text NOT NULL, keywords text NOT NULL, body text NOT NULL, language text NOT NULL,
 created_by text NOT NULL, created_at bigint NOT NULL, modified_by text NOT NULL, modified_at bigint NOT NULL,
 stamp bigint NOT NULL,
 -- The words of the name and keywords (weights A, B) and of the body (C, D).
 words tsvector NOT NULL,
 UNIQUE (resource_id, surface)
);
CREATE INDEX documents_scope ON papergo_search.documents (scope_id, views);
CREATE INDEX documents_collection ON papergo_search.documents (collection_id, resource_id);
CREATE INDEX documents_workspace ON papergo_search.documents (workspace_id, modified_at);
CREATE INDEX documents_modified ON papergo_search.documents (modified_at, resource_id);
CREATE INDEX documents_stamp ON papergo_search.documents (stamp);
CREATE INDEX documents_blob ON papergo_search.documents (resource_id, blob_id);
CREATE INDEX documents_words ON papergo_search.documents USING gin (words);
CREATE TABLE papergo_search.document_terms (term_id text NOT NULL, doc bigint NOT NULL REFERENCES papergo_search.documents(id) ON DELETE CASCADE, PRIMARY KEY (term_id, doc));
CREATE INDEX document_terms_doc ON papergo_search.document_terms (doc);
CREATE TABLE papergo_search.document_keywords (keyword text NOT NULL, doc bigint NOT NULL REFERENCES papergo_search.documents(id) ON DELETE CASCADE, PRIMARY KEY (keyword, doc));
CREATE INDEX document_keywords_doc ON papergo_search.document_keywords (doc);
-- Typed field values, encoded as PaperGo's field index encodes them.
CREATE TABLE papergo_search.document_fields (
 doc bigint NOT NULL REFERENCES papergo_search.documents(id) ON DELETE CASCADE,
 key text NOT NULL, type text NOT NULL, scale integer NOT NULL, ordinal integer NOT NULL,
 text text, int bigint,
 PRIMARY KEY (doc, key, ordinal)
);
CREATE INDEX document_fields_text ON papergo_search.document_fields (key, type, scale, text);
CREATE INDEX document_fields_int ON papergo_search.document_fields (key, type, scale, int);
-- File content: chunks of the text of an item's file version (blob), under
-- a generation; one generation per (item, blob) is published. Staged chunks
-- are not searched. A part is the workflow step that staged them.
CREATE TABLE papergo_search.chunks (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 item_id text NOT NULL, blob_id text NOT NULL, generation text NOT NULL, part text NOT NULL, ordinal integer NOT NULL,
 published boolean NOT NULL DEFAULT false, page integer NOT NULL, kind text NOT NULL, text text NOT NULL, enrichment text NOT NULL,
 input_hash text NOT NULL, language text NOT NULL,
 -- The words of the text and enrichment (A) and their stems (B).
 words tsvector NOT NULL,
 UNIQUE (generation, part, ordinal)
);
CREATE INDEX chunks_item ON papergo_search.chunks (item_id, blob_id, published);
CREATE INDEX chunks_hash ON papergo_search.chunks (input_hash);
CREATE INDEX chunks_words ON papergo_search.chunks USING gin (words);
-- Embedding vectors, per model and chunk input (input_hash): reused by every
-- chunk with the same input, across items and reindexes. Normalized float32,
-- little-endian. stamp orders writes, so the in-memory index loads only what
-- changed; unused_since marks vectors no chunk (or another model) uses, which
-- are deleted after a while.
CREATE TABLE papergo_search.vectors (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 model text NOT NULL, input_hash text NOT NULL, dims integer NOT NULL, vector bytea NOT NULL,
 stamp bigint NOT NULL, unused_since bigint,
 UNIQUE (model, input_hash)
);
CREATE INDEX vectors_stamp ON papergo_search.vectors (model, stamp);
