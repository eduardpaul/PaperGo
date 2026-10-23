# Search indexing and search stores (plan)

This plan ports PaperDotNet's search layer to PaperGo. That layer is one search across everything a person may read: keyword, semantic and hybrid search, page hits, typed field filters and facets. It sits behind a replaceable **search store**.

PaperGo gets two stores, built as separate packages:

- **`sqlite`**, the default: a separate `search.db`;
- **`zvec`**, optional: an embedded vector and full-text engine.

All indexing runs **asynchronously in workflows** that people can turn off and change per workspace or collection:

- `search.index` keeps the basic list and library data (names, fields, terms, permissions) in the index;
- `search.content` turns files into searchable content with activities: read text and OCR, chunk, optional AI steps, embed, publish.

Workflows chain through declared **outputs**. The search store also replaces the FTS5 tables in the main database: `search` in collection queries and `q` in listings use it too. Implementation status and P6 validation are recorded below; [search.md](../search.md) describes the running app.

Sources, PaperDotNet at `96df412`:

- ADRs [0012](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0012-full-text-search.md) (full text), [0027](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0027-semantic-and-hybrid-search.md) (semantic, hybrid, pages), [0043](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0043-search-indexing-as-workflows-over-a-search-store.md) (store contract, indexing as workflows) and [0044](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0044-zvec-search-store.md) (zvec), and the [zvec spike](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/performance-artifacts/zvec-spike/README.md).
- The code in `src/Modules/Search` and the full-text providers in `src/BuildingBlocks/PaperDotNet.Persistence.*`.

## 1. How PaperDotNet does it

### Layers

| Layer | PaperDotNet piece | What it does |
| --- | --- | --- |
| Producers | `ListItemSearchDocuments` (Lists), `IItemSearchContributor` (Documents: page text; Collaboration: comments) | Build one `SearchDocumentData` per list item in code, on integration events: title, body (text fields), keywords (high-weight fields, term labels), language, `ScopeId`, term ids, typed `Fields`, and `Pages` (file text, page by page) |
| Write contract | `ISearchIndex` | `UpsertAsync`, `DeleteAsync`, `SetScopesAsync` (permission change without touching text), `DeleteContainerAsync`, `DeleteSourceAsync` |
| Reindex | `ISearchSource` and `SearchReindexer` | Each producer pushes all its content again. Runs as the operation `POST /v1.0/search/reindex` |
| Store contract | `ISearchStore : ISearchIndex` | Adds `SearchAsync(StoreSearchQuery)`, `GetFieldsAsync`, `CountTermsAsync`, `GetPassagesToEmbedAsync` and `SetEmbeddingsAsync`. It declares `Capabilities` (Keyword, Vector, Facets). It is chosen by `Search:Store` and registered with `AddSearchStore<T>(name)` |
| Service | `SearchService` | Validates the request, picks the mode, parses `q` once (`FullTextQuery`) and `$filter` once (OData → store-neutral `SearchFilter` tree), resolves the caller's readable scopes and the child terms, embeds the query (cached 30 min), calls the store, and cuts snippets. It never touches tables |
| Embeddings | `EmbeddingModel` and the job `search.embeddings` (every minute, per tenant) | Embed passages that have no vector of the configured model, batch 64, at most 2,000 per run. A provider failure stops the run and the next run retries |
| Stores | `DatabaseSearchStore` (`database`, the default) and `ZvecSearchStore` (`zvec`, opt-in) | See below |
| Tests | `SearchStoreConformanceTests` | 20 tests per store, through `ISearchStore` only |

### The query model

- **`FullTextQuery`:** words (AND), `"phrases"`, `a OR b`, `-word` / `NOT word`, `prefix*`. At most 32 terms. Tokens are lower-case runs of letters and digits, so no store needs escaping. Exclusions alone are rejected.
- **`StoreFilter`:**
  - the readable scope ids (always applied);
  - workspace, container, content type and term ids (with descendants);
  - author, the updated-at range;
  - `Fields`, a `SearchFilter` tree (`Compare`, `In`, `HasValue`, `And`, `Or`, `Not`). Text kinds compare by equality only, a multi-value field matches when any value does, and `ne` also matches documents without the field.
- **Search fields:** kinds are `keyword`, `number`, `date`, `dateTime`, `boolean`, `reference` and `terms`. A value is either text or a double (dates as days since 1970, times as Unix ms, booleans as 0/1). A field name with one kind is one search field across all lists. A name used with several kinds is addressed as `{name}_{kind}`.
- **Modes:**
  - `keyword` gives exact counts and facets;
  - `semantic` takes passages by cosine similarity (at least `MinSimilarity`, 0.3), groups them per document by its best passage, and excluded words still remove hits;
  - `hybrid` takes the best 200 documents of each side and fuses them with reciprocal rank fusion (k = 60). Count and facets cover only those candidates.
  - The default is hybrid when a model is configured, else keyword. With filters and no `q`, documents are listed newest first.
- **Hits:**
  - the document's fields, plus `rank`;
  - `page` and the snippet text, from the best passage: the best full-text-ranked passage for keyword matches, else the most similar one;
  - `matchedBy`: `keyword`, `semantic` or both.
- **Facets:** workspace, container, content type and term, the top 20 of each.

### The `database` store: SQLite and PostgreSQL

The same EF Core code runs on both providers. Only the full-text engine differs, behind `IFullTextSearch`.

- **Tables:**
  - `documents`: title, keywords, body cut at 200,000 characters, language, scope, metadata;
  - `document_keywords`, one row per document and term;
  - `document_fields`, one row per value, indexed by (name, kind, text) and (name, kind, number);
  - `passages`: about 1,200 characters each, overlapping by 150, cut at word boundaries, at most 400 per document. Each passage has its page, the SHA-256 of title plus text (`ContentHash`), and a float32 embedding blob with the model and a `VectorStamp`.
- **SQLite:**
  - an FTS5 external-content table per entity, with sync triggers (`unicode61`);
  - ranked by `bm25` with weights 10/4/1 for title, keywords and body;
  - stemming is English only (`porter`), for every row.
- **PostgreSQL:**
  - a stored generated `tsvector` with a GIN index;
  - the `simple` configuration, plus a `CASE` per row language (15 Snowball languages);
  - punctuation becomes spaces first (`regexp_replace`), so words split as they do in FTS5;
  - each query term matches its exact form or its stem in any language;
  - ranked by `ts_rank`.
- **Vectors:**
  - searched in memory, by brute force over the normalized float32 values, per tenant and server (`VectorIndex`);
  - before each search, the store checks the count and the newest stamp, then loads only what changed. A 30-second skew window keeps several servers consistent without messages;
  - the workspace and container filters apply in memory; trimming and the other filters apply afterwards, in SQL.
- **Re-index:** a passage whose hash is unchanged keeps its row and its vector, so nothing is embedded twice.

### The `zvec` store

zvec is an embedded vector database (Apache-2.0), called through its C API.

- **Layout:**
  - one collection per tenant and embedding model, under `{data}/search/zvec/{tenant}`;
  - a head row per document, and a row per passage. Every row carries all the metadata, so one filter expression covers both kinds of row.
- **Full-text columns:** `metadata` and `content`; both contain encoded exact words, stems and prefixes. Native full-text tables have a substantial fixed memory cost. Name/keyword weighting and filter-only queries run against the Go catalog.
- **Field values:**
  - text kinds and booleans are tokens `{fieldkey}:{base64url(value)}` in one string-array column, so user text never appears raw in a filter;
  - numbers, dates and times get a numeric column each (`n_{key}`), added on first use, up to the 1,024-column limit.
- **Filters:** one builder writes them from `StoreFilter` and the field tree. Negations are pushed down, because the grammar has no general `NOT`.
- **Writes:** one writer per collection. Passages are reused by hash.
- **Queries:**
  - keyword and semantic are two zvec queries, fused in .NET;
  - counts and facets cover the first 10,000 candidates and are estimates beyond that.
- **Operations:**
  - at most 4 collections are open at once (LRU);
  - backups close the collections and copy the folder;
  - one server only, because zvec locks the folder;
  - the native library is built in CI from a pinned commit.

### What PaperDotNet has not built yet

ADR-0043 phases 2 to 5 are still only proposed in PaperDotNet:

- metadata and content indexed separately;
- `search.chunk`, `search.enrich`, `search.publish` and `search.embed` as workflow activities, and chunkers;
- the built-in workflow `search.content`;
- reindexing that never deletes first;
- vectors keyed by input hash;
- a separate `search.db`;
- `/search/migrate`.

PaperGo starts from that target design directly instead of repeating phase 1.

## 2. Review of `main`: change events and OCR

Two commits on `main` (merged into this branch) change what search can build on.

### Change events (`8e8ebd0`)

`permissions.changed` and `term.changed` exist. One of them must change for search, and folder events are still missing:

| Event | Raised when | Data search uses |
| --- | --- | --- |
| `permissions.changed` | A collection, folder or item ACL is replaced (`action: replace`), or workspace membership changes (`member.set`, `member.remove`). **Today one event names the edited scope, not its inheriting descendants** | Becomes one delivery **per affected item and folder**, expanded by the dispatcher in batches (E2, section 7.1). `search.index` then simply re-syncs each item |
| `term.changed` | A term's name, labels, synonyms or other properties change (PUT `/v1/terms/{id}`). One event per live workspace | `term_id` and `changed_fields`. Only `name`, `labels` and `synonyms` change indexed text |
| `term.merged` (existing) | A term is merged | `source_term_id`, `target_term_id` |

Consequences for this plan:

- **`term.changed` needs `taxonomy.read`.** Dispatch skips subscriptions whose author lacks it, and each node rechecks it. `search.index` runs as the system subject, which always holds it (section 7.2).
- **Per-item `permissions.changed`** (E2) replaces the single scope event: a workflow runs for each item whose effective permissions changed, with the item as the run's item.
- **Folders still raise no events.** Collection queries and `q` on listings must find folders by name once the FTS5 tables go (section 8), so P1 adds `folder.*` events (E3).

### OCR and document workflows (`03d0b36`)

What arrived:

- `internal/ocr`, the port: `Engine.Detect` and `Recognize` return lines, words and positions.
- `internal/ocr/paddle`, the only engine: PaddleOCR on ONNX Runtime through cgo SDKs.
- `internal/imaging`, `internal/pdf` (a searchable PDF **writer**), `internal/compose` and `internal/optimize`.
- Heavy-work activities (prepare → work outside any transaction → commit).
- Staged files, the `file.added` trigger, concurrency policies, `forEach`, and tasks.
- Two built-ins, `documents.optimize` and `documents.photos_to_pdf`.

**Layering: it fits.**

- **Port and adapter.** `internal/workflow` imports only `internal/ocr`. The engine is constructed in `cmd/api` (and `cmd/ocr`) and handed over in `workflow.Tools`. Engines never read storage or the database, so a second engine (Tesseract, a GLM-OCR endpoint) needs no change in callers. This is the pattern the search stores follow: `internal/workflow` sees `search.Store`, and `cmd/api` picks the implementation.
- **Native SDKs.** PaperGo uses cgo with maintained ONNX, PDFium and zvec Go SDKs. `scripts/native.py` installs pinned native libraries and headers. The Docker runtime uses `distroless/cc` with glibc and libstdc++.
- **Heavy work outside the writer.** The three-step activity shape (`run` → `Exec.Work` → `commit`) is what search activities need. Search writes go to another database, so `search.*` activities do their store writes in `Work` and keep their main-database steps short.
- **One wrinkle.** `internal/imaging` imports `internal/storage` only for the `storage.DocumentType` signature sniffer. It is harmless, but the sniffer belongs in a small leaf package (for example `internal/mediatype`), so imaging and later `internal/doctext` depend on no storage code. A cleanup, not a blocker.

**Portability: two gaps that matter for search.**

1. **Recognized text is not kept.**
   - `file.compose_pdf` writes OCR text into the PDF's invisible text layer, and `file.optimize_image` only measures text height.
   - Nothing stores page text, and nothing reads text from a PDF: `internal/pdf` only writes.
   - So reading text is still missing. Search needs it for file content and page hits. Section 7.3 adds it as the activity `document.read_text` on top of the OCR port, used inside the personalizable `search.content` workflow rather than as a separate fixed process.
2. **Content packages.**
   - Export and import (`internal/dms/portability.go`) cover workflow definitions but not tasks or staged files. Those count as installation state, like runs, so this is consistent.
   - Extracted page text is a new kind of derived data. It costs real time to recompute (OCR), so it is exported with the file bytes it belongs to: one entry per SHA-256, with the extractor and model hashes. An import keeps it instead of running OCR again (section 7.3). Search indexes stay out of packages and are rebuilt after an import, as the typed indexes are today.

**License note from that commit.** `docs/dependencies.md` records that ONNX Runtime's release binaries include Eigen, partly under MPL-2.0. PaperGo has no written allow list yet. When one is written, it should cover this and the zvec notices (section 11) together.

## 3. What is different in PaperGo

These differences shape the Go design. Each changes something in the port.

| PaperGo fact | Consequence for search |
| --- | --- |
| One organization per deployment, no tenant ID | No tenant column and no per-tenant collections. Store isolation is per deployment |
| SQLite (modernc, `CGO_ENABLED=1`), one writer shared by requests and the DBOS runner | Search data must not compete for the main writer. The `sqlite` store lives in its own file, `search.db`, with its own writer and WAL. zvec is loaded through cgo SDKs |
| Head and published surfaces; `read` and `read_draft` are separate | A search document is a **(resource, surface)** pair: an item or folder has one or two documents, and the caller's surface choice (auto, head or published) must apply exactly as in collection queries |
| Authorization is immediate and transactional ([architecture](../architecture.md): "any asynchronous projection must retain authorization checks at query time") | The store trims by readable scopes to get candidates. The caller's SQL in the main database then applies the authoritative permission and surface predicate (sections 4 and 8). A stale index can never show what the caller may no longer read |
| `resources.scope_id`: the nearest exclusive permission scope, maintained by triggers; null for rows without one | Scope trimming works like PaperDotNet's ADR-0035 model. The null scope is a real value: it is stored as `""` |
| Exact numbers: `integer` (int64), `decimal` (int64 units, fixed scale), `number`/`currency` (exact decimal domain with fixed-width ordering keys) | Search field kinds keep exactness: `integer` and `decimal` are int64, and `number` uses the ordering key. No silent float conversion |
| Organization field catalog with stable `key`, `type` and `scale` | A search field's identity is (key, kind), with the same `{key}_{kind}` rule when a key is reused with another type |
| Filters use PaperGo's JSON query filter language (`and`/`or`/`not`, typed operators, `under` for terms) | `filter` on search reuses that language. There is no OData parser. The validated tree becomes `search.Filter` |
| Everything that reacts to changes is a workflow, and people can change workflows | Indexing is two built-ins, `search.index` and `search.content`, that workspaces and collections can turn off, copy and recompose. Fixed activities and query-time authorization keep permissions out of reach (section 7) |
| `item_surface_search` and `resource_search` (FTS5 in `papergo.db`) serve `search` and `q` today | They are dropped. Collection queries and listings read the search store (section 8), with no second index to keep in step |

## 4. Target shape

```
              main database (papergo.db)                               search store (search.db | zvec)
 ┌────────────────────────────────────────────────┐              ┌───────────────────────────────────────────────┐
 │ writes ─► domain_events (item.*, folder.*,     │              │ documents (resource, surface): metadata, text │
 │           permissions.changed per item [E2],   │              │ fields, terms, keywords                           │
 │           term.*, file.added)                  │              │ chunks (item, blob, generation) + full text   │
 │ resources / item_surfaces / field_values       │              │ vectors (input hash, model)                   │
 │ blob_texts (page text per SHA-256)             │              └───────▲───────────────────▲───────────────────┘
 └──┬───────────────────────┬─────────────────────┘                      │                   │
    │ dispatcher            │ file.added (media_types [E6])              │                   │
    ▼                       ▼                                            │                   │
 search.index            search.content  (workspace default, collection │                   │
 (role; editable)        override [E5]; editable)                        │                   │
 search.sync ────────────────────────────────────────────────────────────┘                   │
                         document.read_text ─► search.chunk ─► [ai.complete / search.enrich] │
                           ─► search.embed ─► search.publish ────────────────────────────────┘
                                   │
                                   ▼ role.search.content.completed {outputs: blob_id, text_id, …} [E1]
                         any workflow that follows (classify, notify, …)

 GET/POST /v1/search                      ─► search.Service ─► store.Search ─► authoritative SQL check ─► hits
 collection query `search`, listing `q`   ─► store.Match (ids) ─► the collection query's SQL (permissions, sort, keyset)
```

**Packages:**

| Package | Contents | Imports |
| --- | --- | --- |
| `internal/search` | Store interface and types, the `FullTextQuery` parser, `Filter` tree, `Service`, RRF, snippets, chunkers, the document builder (from a read snapshot to `Document`) | `dms` read API; standard library otherwise |
| `internal/dal/search/sqlitestore` | The default store in `search.db`, with its in-memory vector index | `modernc.org/sqlite` |
| `internal/dal/search/zvecstore` | Native zvec store through cgo | `github.com/zvec-ai/zvec-go` |
| `internal/search/storetest` | Conformance suite that each store's tests call | `search` |
| `internal/doctext` | Page text of a file: PDF text layer, rendering and OCR through the port, the text cache | `ocr`, `imaging`, a PDF reader (section 7.3) |
| `internal/ai` | Embeddings client (OpenAI-compatible `/v1/embeddings`), retries, query cache | standard library |
| `internal/workflow` (existing) | `search.*` and `document.read_text` activities and the built-ins | `search`, `doctext` through `Tools` |
| `internal/httpapi` (existing) | `/v1/search` routes | `search` |

Only `cmd/api` chooses the store (`SEARCH_STORE`) and passes it in `workflow.Tools` and to `dms`. Only `zvecstore` knows zvec, and only `sqlitestore` opens `search.db`.

## 5. Contracts (`internal/search`)

A sketch. Names follow PaperGo's Go style; the final signatures come out of P1.

```go
// A Document is the searchable metadata of one surface of an item or folder.
type Document struct {
    ResourceID, Kind   string   // kind: "item" or "folder"
    Surface            string   // "head" or "published"
    Views              Views    // surfaces this row serves: ViewHead|ViewPublished (see below)
    WorkspaceID        string
    CollectionID       string
    ParentID           string   // for parent_id / ancestor_id in collection queries
    ContentTypeID      string
    ScopeID            string   // "" = rows without a scope
    RevisionID         string
    BlobID             string   // current file of this surface, "" when none: selects its content
    Name               string
    Keywords           string   // high weight: keywords, term labels and synonyms, high-weight fields
    Body               string   // other field text, notes
    Language           string   // Snowball name or ""
    Keywords               []string
    TermIDs            []string
    Fields             []Field
    CreatedBy, ModifiedBy string
    CreatedAt, ModifiedAt time.Time
}

type FieldKind string // keyword, integer, decimal, number, date, datetime, boolean, reference, terms

type Value struct { // exactly one set
    Text   *string // keyword, reference, terms
    Int    *int64  // integer, decimal units, date (days), datetime (unix ns), boolean (0/1)
    Number *string // number/currency: the canonical decimal; stores derive an ordering key
}

type Field struct { Key string; Kind FieldKind; Scale int; Values []Value }

type Chunk struct { Ordinal int; Page int /* 0 = not on a page */; Text, Enrichment, InputHash, Language string }

type Store interface {
    Name() string
    Capabilities() Capabilities // Vector, ExactCounts, Stemming

    // Metadata, from search.sync. Never touches chunks or vectors.
    UpsertDocuments(ctx context.Context, docs []Document, stamp int64) error
    DeleteDocuments(ctx context.Context, sel Selection) error // resource ids, (resource, surface), older than a stamp
    SetScopes(ctx context.Context, scopes map[string]string) error // resource id -> scope id

    // Content, from the search.content activities.
    StageContent(ctx context.Context, itemID, blobID, generation string, chunks []Chunk) error
    PublishContent(ctx context.Context, itemID, blobID, generation string) error // drops other generations of (item, blob)
    DeleteContent(ctx context.Context, itemID string, keepBlobs []string) error

    // Vectors, keyed by input hash and model: reused across items, reindexes and stores.
    ChunksToEmbed(ctx context.Context, model string, limit int) ([]ChunkToEmbed, error)
    FindVectors(ctx context.Context, model string, hashes []string) (map[string][]float32, error)
    SetVectors(ctx context.Context, model string, vectors []HashVector) error

    // Global search: ranked documents with snippets and pages.
    Search(ctx context.Context, q Query) (Result, error)
    // Collection queries and listings: resource ids whose surface text matches, in one collection
    // (or among a parent's children), at most limit; capped reports that more exist.
    Match(ctx context.Context, m MatchQuery) (ids []string, capped bool, err error)

    Fields(ctx context.Context) ([]FieldInfo, error)
    CountTerms(ctx context.Context, termIDs []string) (map[string]int, error)
    Export(ctx context.Context, emit func(Record) error) error // copying between stores
    Stats(ctx context.Context) (Stats, error)
    Close() error
}
```

**Surfaces and views.** Collections without publishing keep only a published surface, and draft readers read it as their head. Every document row carries `Views`:

- a published surface of a resource **with** a head surface serves `published` only;
- a published surface **without** one serves both views;
- a head surface serves `head`.

The readable scopes become one predicate:

```
(views has head      AND scope IN draftScopes)
OR (views has published AND scope IN readScopes AND scope NOT IN draftScopes)   -- surface=auto
```

`surface=head` keeps the first branch only. `surface=published` keeps `views has published AND scope IN (draft ∪ read)`. This is `scopeAccess.sql` from `internal/dms/queryscopes.go`, extended to the whole organization.

**Readable scopes.** Add `dms.SearchScopes(ctx, subject) (draft, read []string, nullDraft, nullRead bool)`:

- It takes one representative live row per distinct `scope_id` across the organization (a loose index scan over a new `resources(scope_id)` index).
- It evaluates `permissionSQL` once per scope, with the pattern of `collectionScopes` but without the 64-scope cap.
- It is measured in P1. If it is slow, cache it per subject and keep the cache valid with a generation counter, which triggers on `grants`, memberships and `resources.scope_id` maintain. A stale cache cannot leak, because of the authoritative check; it can only miss items.

**The authoritative check.** Global search (section 10) calls `dms.VisibleSurfaces(ctx, subject, ids, surface)` on one snapshot after `store.Search`:

- It drops hits that are deleted, not readable, or on another surface than the caller reads now.
- It fetches the next candidates when hits were dropped, up to three rounds.
- The count then becomes `total_estimated`.

Collection queries need no extra check: the store only supplies candidate IDs, and the query's existing SQL applies permissions, surfaces, filters, sort and paging (section 8).

## 6. Stores

The two stores are separate packages with nothing in common but the `search` contract and the conformance suite. Neither imports the other, and each can be tested and shipped alone.

### 6.1 `sqlite` (default)

- **File:** `SEARCH_DATABASE_PATH`, default `search.db` next to the database.
  - It has its own `*sql.DB` with the pragmas of `database.DSN`: WAL, `_txlock=immediate`, busy timeout.
  - It has its own write mutex. Indexing never waits on application writes and never blocks them.
- **Schema:** embedded in `sqlitestore/schema.sql`, with a `state(schema_version)` row. The store creates the file, and a different version makes it delete the file and request a reindex (decision D3).
- **Tables:**
  - `documents`:
    - key `(resource_id, surface)`, plus `rowid`;
    - `kind`, `views`, `workspace_id`, `collection_id`, `parent_id`, `content_type_id`, `scope_id`, `revision_id`, `blob_id`;
    - `name`, `keywords`, `body`, `language`;
    - author and time columns, `stamp`;
    - indexes on `(collection_id, parent_id)`, `(scope_id)`, `(workspace_id)` and `(modified_at)`.
  - `document_terms(doc, term_id)` and `document_keywords(doc, keyword)`.
  - `document_fields(doc, key, kind, ordinal, text, int, ordkey)`, indexed by `(key, kind, text)`, `(key, kind, int)` and `(key, kind, ordkey)`. `ordkey` is the fixed-width decimal ordering key `field_values` already uses, so `number` filters are exact.
  - `chunks(id, item_id, blob_id, generation, published, ordinal, page, text, enrichment, input_hash, language)`, indexed by `(item_id, blob_id, published)` and `(input_hash)`.
  - `vectors(model, input_hash, dims, vector BLOB, stamp)`: normalized float32 little-endian, primary key `(model, input_hash)`, plus an index on `(model, stamp)` for cheap change checks.
  - `state(key, value)`: the schema version and the reindex stamps.
- **Full text:**
  - `doc_fts`: a **contentless** FTS5 table (`content=''`, `contentless_delete=1`) with two columns:
    - `heading`: name and keywords;
    - `text`: body plus all published chunk text of the surface's blob.

    It finds documents whose words are spread across fields and pages without storing the text twice. It is rewritten when a document's metadata or its published content changes. P0 checks `contentless_delete` in modernc's SQLite.
  - `chunk_fts`: external content on `chunks(text, enrichment)`, for page hits and snippets.
  - Ranking: `bm25(doc_fts, 10, 1)` plus a share of the best chunk's `bm25` among the candidates, tuned on the relevance set (section 12).
  - Tokenizer: `unicode61 remove_diacritics 2`, and the Go tokenizer of `FullTextQuery` follows the same rule (letters and digits as runs, lower-cased). A test checks that the two agree.
- **Stemming in every language** (P2):
  - Stem in Go with a Snowball library and index the stems in extra columns `heading_stem` and `text_stem`.
  - Each query term becomes `(exact OR stem_lang1 OR …)` over `SEARCH_LANGUAGES` (default `english`), the semantics of PaperDotNet's PostgreSQL `RenderWithLanguages`.
  - Library (chosen in P2): `github.com/blevesearch/snowballstem` (BSD-3-Clause), the Snowball project's generated Go stemmers: 18 languages and no dependencies. `kljensen/snowball` (MIT) covers only seven (no German, Italian or Dutch).
- **Prefixes and phrases:** native FTS5 (`"tok"*`, `"a b"`). The listing behaviour, where the last word matches as a prefix as people type, is a flag on `MatchQuery`.
- **Vectors:** an in-memory index inside the package, a port of PaperDotNet's `VectorIndex`:
  - an immutable snapshot of (chunk, item, views, scope, workspace, collection, vector);
  - refreshed from `(model, stamp)`, and kept current directly by `SetVectors` in this process;
  - top-k by a heap, with workspace and collection filters in memory and the other filters in SQL over the candidates;
  - excluded words remove semantic candidates;
  - memory is chunks × dimensions × 4 bytes, reported in `Stats`.

  P0 measures a pure-Go dot product, unrolled by 8, at 100k × 768. If it is too slow, P3 adds an int8 scan with a float32 rescore of the top 1,000. Larger indexes use zvec.
- **Facets:** exact `GROUP BY` over the keyword match set, or over the fused candidates. Workspace, collection, content type, terms and keywords, top 20 each.
- **Scope lists:** passed as one JSON parameter (`scope_id IN (SELECT value FROM json_each(?))`), so 20,000 scopes are not 20,000 placeholders.

### 6.2 `zvec` (optional)

A port of `PaperDotNet.Search.Zvec`, with the lessons of its implementation notes.

- **Binding:** the official zvec Go SDK through cgo, pinned to the string-array fix in `eduardpaul/zvec-go`. The SDK owns native error handling and memory.
  - Document handles are closed explicitly. String arrays use the SDK's copying setter.
- **Layout:** ZvecLayout without the tenant.
  - One collection per embedding model and dimension, under `SEARCH_ZVEC_PATH` (default `data/search/zvec/{model-key}-{dims}`). Text-only installations use `text-only`.
  - Head rows per (resource, surface); chunk rows per (item, blob).
  - Every row carries `kind`, `views`, `surface`, `blob_id`, `scope_id`, `collection_id`, `parent_id`, the metadata columns, `field_tokens` and the `n_{key}` columns.
  - Full-text columns: `metadata` and `content`, carrying encoded exact words, stems and prefixes. Name and keyword weighting runs in Go.
  - A chunk that serves both surfaces (same blob) carries the union of their `views`. Metadata writes update chunk rows with `update`, never rewriting text or vectors.
- **Exact numbers:**
  - `integer`, `decimal` (units) and `datetime` (ns) go in INT64 columns.
  - P0 checks whether zvec compares STRING columns by range. If it does, `number` and `currency` use the ordering key. Otherwise they are DOUBLE columns, documented as approximate beyond 15–17 significant digits, with a conformance test at the boundary.
- **Writes:** one writer goroutine with a bounded channel, batching upserts, updates and `delete_by_filter`. Every write is an idempotent upsert, because zvec batches are not atomic.
- **Queries:**
  - keyword (`metadata` or `content`, with name and keyword weighting in Go) and semantic (HNSW) are separate queries, fused in Go;
  - `matchedBy` comes from both lists;
  - counts and facets are estimates beyond `SEARCH_ZVEC_KEYWORD_CANDIDATES` (10,000).
  - `Match` for collection queries is a keyword query filtered on `collection_id`, `parent_id` and views. Its cap is the same candidate limit (section 8).
- **Operations:**
  - opened lazily, and reads never create a collection;
  - backups flush, close and copy the folder through `internal/backup`;
  - one node only (zvec's `LOCK`);
  - about 120–160 MB per open collection, and there is one collection per deployment.
- **Packaging:**
  - CI downloads the pinned zvec v0.7.0 SDK using `scripts/native.py` and checks its archive hash;
  - it ships in the image next to ONNX Runtime and PDFium;
  - libaio (LGPL) is only `dlopen`ed when present, never linked or shipped;
  - license notices as in section 11.

**PostgreSQL** is out of scope. A PostgreSQL store (tsvector with languages, pgvector) can follow when PaperGo's main database supports PostgreSQL, as a third package behind the same contract and suite.

## 7. Indexing pipeline: workflows people can change

Everything after a change is a workflow ([workflows](../workflows.md#building-features-on-workflows)). Search ships **two** built-ins:

- **`search.index`** keeps the basic list and library data in the index: name, keywords, fields, terms, permissions and surfaces.
- **`search.content`** turns a file into searchable content. It is composed of activities (`read text → chunk → [AI steps] → embed → publish`), and every library or workspace can change it.

Both can be turned off, replaced or copied and edited, per workspace or per collection, like any built-in. The engine and a few fixed activities guarantee what must never vary (section 7.4).

### 7.1 Workflow engine additions

Search needs six engine features. Each is general, not search-specific.

#### E1. Workflow outputs, for chaining workflows

A workflow definition gains `outputs` and `output_schema`:

```jsonc
{
  "name": "Index file content",
  "triggers": [{"type": "file.added", "media_types": ["application/pdf", "image/*"]}],
  "output_schema": {"type": "object", "properties": {
    "blob_id": {"type": "string"}, "text_id": {"type": "string"}, "pages": {"type": "integer"},
    "language": {"type": "string"}, "generation": {"type": "string"}, "chunks": {"type": "integer"}}},
  "outputs": {
    "blob_id": "{trigger:data.blob_id}", "text_id": "{step:read.text_id}", "pages": "{step:read.pages}",
    "language": "{step:read.language}", "generation": "{step:chunk.generation}", "chunks": "{step:chunk.chunks}"},
  "flow": { "...": "..." }
}
```

- **When outputs are produced.** They are evaluated in `finish`, the last step of a run that **completed**, from the run's tokens (`{step:…}`, `{var:…}`, `{trigger:…}`).
- **Validation.** They are checked against `output_schema` with the existing form validator (`internal/forms`). A single-token value keeps its JSON type. The result is limited to 16 KB, so large data travels as references, such as a `text_id` or a `generation`, never as text.
- **Where they show up:**
  - stored on the run and shown in `GET /v1/workflow-runs/{id}`;
  - carried in `wf.{key}.completed` as `data.outputs`. A workflow that follows another reads them as `{trigger:data.outputs.text_id}`.
- **Failures.** An output that fails its schema fails the run at `finish`, with the path in the error, so a broken chain shows where it broke.
- **Roles have an output contract.** The runner plan's roles (one active workflow per scope fills a role, and raises the role's events) gain a required `output_schema`. A custom workflow that fills `search.content` must declare outputs compatible with the role's schema, which saving checks. Consumers trigger on the **role** (`role.search.content.completed`, raised by every filler beside its own `wf.{key}` event; built in P4), not on a built-in key, so they keep working when a library swaps the default for its own.
- **Chaining stays event-based** (PaperDotNet's rule: workflows follow each other by events, never by code that calls another). Outputs give those events a typed, documented payload.

Examples that chaining enables without touching search:

- an "AI classify" workflow on `role.search.content.completed` that reads the text by `text_id` and sets fields;
- a "notify on contract" workflow that reads `language` and `pages`.

#### E2. `permissions.changed` for every affected item

Today one `permissions.changed` names the edited scope (`8e8ebd0`). It becomes an **item event for every live item and folder whose effective permissions changed**. A workflow then runs per item, with the item as the run's item, and item conditions and `collection_id` apply as they do to other item events.

- **Which items count.**
  - **An ACL replace on a collection, folder or item:** every live item and folder that inherits from it, at any depth, stopping at nested resources with their own permissions. That is the resources whose `scope_id` is the edited resource after the change, or was before it, when inheritance was broken or reset.
  - **A workspace membership change** (`member.set`, `member.remove`): every live item and folder of the workspace that inherits from the workspace, again stopping at their own permissions. A grant that names the workspace role is resolved at query time, so this is the set whose effective access changed.
- **Bounded fan-out, without unbounded transactions.**
  - **The request writes one event.** Its transaction writes one `domain_events` row, the **root** event, with the root resource, the action, and the previous and current scope. It does not write one row per descendant, so breaking inheritance on a 200,000-item library stays one small write. Authorization itself is already immediate, because triggers update `scope_id` in the same transaction.
  - **The dispatcher expands it.** It expands the root into per-item `permissions.changed` deliveries in batches of 500, by keyset over the affected resources on a snapshot taken when the expansion starts. Each batch commits its runs together with an expansion cursor kept on the root event (`fanout_cursor`). A crash resumes from the cursor, and run IDs (`wf:{workflowID}:{eventID}:{itemID}`) make repeats harmless.
  - **No expansion when nobody listens.** The dispatcher expands only when an enabled workflow subscribes to `permissions.changed` for that workspace, collection and action. The trigger gains `actions` (`replace`, `member.set`, `member.remove`; default all), so `search.index` subscribes to `replace` only and a membership change costs nothing for search.
- **Event data per item:**
  - `root_id`, `root_kind`, `action`;
  - `previous_scope_id` and `current_scope_id` of **this** item;
  - for membership actions, `member_subject`, `previous_role` and `role`.

  The root's full ACL (`previous`, `current`, `effective_grants`) stays in the root event, readable through `root_event_id`. That keeps per-item rows small.
- **Contract changes:** `docs/workflows.md` loses "without events for inheriting descendants", and `permissions.changed` moves into the item-event group (item conditions apply). `internal/dms/changeevents_test.go` and `internal/runner/changeevents_test.go` gain:
  - descendants at several depths;
  - a nested resource with its own permissions that is skipped;
  - expansion that resumes after a crash;
  - no expansion without subscribers.

#### E3. Folder events

`folder.created`, `folder.updated` (rename, move), `folder.deleted` and `folder.restored`. They work as item events with the folder as the run's item, but have no fields or files. `search.index` subscribes to them, so folders are searchable by name in collection search and listings. They are separate types rather than item events with `kind: folder`, so no existing item workflow suddenly starts on folders.

#### E4. `coalesce` concurrency

A start while a run on the same item is **queued** starts nothing. A start while one is **running** queues one follow-up, which starts when that run ends. An edit burst costs at most two runs, and the last one sees the final state. The existing `replace` would cancel a run that may already have written, and `skip` could lose the last change. `search.index` and `search.content` use it; any workflow that rebuilds state from the current item can.

#### E5. Workspace-wide built-ins with collection overrides

Built-ins today are either per collection (`PerCollection`) or workspace-wide (no `collection_id`).

- **Precedence.** For a role, the most specific active filler wins: a collection's own workflow, else the workspace's. A workspace can set one content process, and its contracts library can override it.
- **Turning off.** `enabled: false` on a collection turns the role off there, even when the workspace has it on.
- **Visibility.** `GET /v1/resources/{collectionID}/workflow-roles` shows which workflow fills each role and from where, so people can see why their files are, or are not, indexed.

#### E6. Media-type filters on `file.added`

`file.added` gains `media_types`: exact types or `type/*` wildcards, matched against `data.content_type`. A process can skip files it cannot or should not read, such as archives, audio, or encrypted PDFs once those uploads are supported, without starting a run. A flow can also branch on `{trigger:data.content_type}` with `if` for finer choices.

### 7.2 `search.index`: the basic data

- **The built-in:**
  - workspace-wide, on by default, filling the role `search.index`;
  - it runs on the system queue with concurrency `coalesce`, and acts as the system subject, which reads every resource and holds `taxonomy.read`.
- **Triggers:**
  - `item.created`, `item.updated` (also moves), `item.published`, `item.unpublished`, `item.restored`, `item.deleted`, `item.purged`;
  - `folder.*`;
  - `permissions.changed` (actions `replace`);
  - `term.changed` (when `name`, `labels` or `synonyms` changed) and `term.merged`.
- **The default flow is one node, `search.sync`.**
  - It reads the resource's **current** surfaces on one read snapshot: name, keywords, fields with their kinds, term labels and synonyms, scope, workspace, collection, parent, revision and current blob.
  - It upserts them, and deletes surfaces that are gone along with content of blobs no surface references any more.
  - It reads state, not event payloads, so replays, duplicates and order do not matter. Per-item `permissions.changed` (E2) makes scope moves plain syncs: no batch activity is needed.
- **Term events have no item.** For `term.changed` and `term.merged`, a `forEach` over `search.items_with_term` (pages of 500 IDs of the workspace's resources holding the term) runs `search.sync` per ID.
- **How it can be changed:**
  - **Turned off for a collection or a workspace.** The collection's items then leave global search, and collection `search` and listing `q` return `400 search_off` for that collection, so nobody mistakes "not indexed" for "no matches".
  - **Copied and edited.** For example, a condition that keeps a confidential list out of global search, or a node before `search.sync` that adds text. `search.sync` takes `extra_keywords` and `extra_body`, single tokens such as `{step:summary.text}`, so an AI step can make metadata richer.
- **What stays fixed** is inside `search.sync` (section 7.4).
- **Fields mapping:**

  | PaperGo field type | Kind | Searchable text | Filter operators |
  | --- | --- | --- | --- |
  | text, email, url, choice | keyword | body | `eq`, `ne`, `in`, `present`, `missing` |
  | note | (none) | body | (none) |
  | integer | integer | body | `eq ne gt gte lt lte in present missing` |
  | decimal | decimal (units at the field's scale) | body | same |
  | number, currency | number (exact ordering key) | body | same |
  | boolean | boolean | (none) | `eq`, `ne` |
  | date, datetime | date (days), datetime (ns) | (none) | as integer |
  | person, lookup | reference | display name, or the target item's name for lookups | `eq ne in` |
  | term, keywords | terms | labels and synonyms in **keywords** | `eq ne in under` |
  | `$name`, `$keywords` | (system) | name in heading, keywords in keywords | the `keyword` parameter |

  All scalar fields are filterable in search, not only the `indexed` ones, because the search store is separate from the main writer. A field option `search_weight: none|normal|high` is added in P2: `none` leaves the field's text out, and `high` moves it to keywords.
- **Cost.** One run per change, bounded by `coalesce`, the system queue and short retention of system runs. P1 measures request write throughput with and without the built-in, and the lag at the 95th percentile. The exit criterion is a loss of at most 15%.

### 7.3 `search.content`: files, composed of activities

#### Activities

Each does its heavy work in an `Exec.Work` step outside the main transaction, and every store write is an idempotent upsert.

| Activity | Inputs | Does | Output and ports |
| --- | --- | --- | --- |
| `document.read_text` | `ocr` (`auto`, `always`, `never`), `max_pages` (500), `languages` (default: the file version's hint) | PDF: the text layer of each page; a page without one is rendered and OCRed when `ocr` allows. Images: decode (`internal/imaging`), EXIF orientation, then `ocr.Engine.Recognize`. A composed PDF already carries its OCR text, so it is never OCRed again. The result is stored once per physical file (`blob_texts`) and reused for identical files | `text_id`, `pages`, `chars`, `language`, `ocr_pages`, `truncated`; ports `done`, `no_text`, `unsupported` |
| `ai.complete` (P7) | `prompt`, `input` (text or `text_id` with `max_chars`), `output_schema` (optional, for JSON answers) | Calls the configured chat model (`AI_CHAT_*`, OpenAI-compatible, off by default). It is a generic step for summaries, extraction and cleanup, cached by the hash of model, prompt and input | `text` or `json`, `cached` |
| `search.chunk` | `chunker` (`window` default, `pages`, `paragraphs`, `headings`, `whole`), `max_chars` (1,200), `overlap` (150), `max_chunks` (400), `source` (`text_id` default, or `text` given by a token), `kind` (`content` default, or `summary`, `extract`), `generation` (to append to one already started) | Splits the text and stages chunks under the generation. Each chunk's `input_hash` covers its text and enrichment, **not the name**, so a rename embeds nothing | `generation`, `chunks`, `truncated` |
| `search.enrich` (P7) | `instructions`, `scope` (`chunk` or `document`) | Adds model-written context to each staged chunk, cached by chunk hash plus instructions hash | `enriched` |
| `search.embed` | `mode`: `immediate` (default) or `later` | Embeds the **staged** chunks of the generation: reuses vectors by `input_hash`, then embeds the rest in batches. A provider failure leaves the chunks to the sweep and follows `deferred`, never failing the run | `embedded`, `reused`; ports `done` and `deferred` |
| `search.publish` | (none) | **Fixed checks**, then makes the generation the file's searchable content and rewrites the full text of the documents of that blob | `published`; ports `done` and `stale` |

**Decided in P7:** `ai.complete` takes `input` (text) or `text_id`, and `search.chunk` takes text given directly as `text`. Both AI steps use the generic `cached_results` store in the main database, with activity namespaces, installation scope, opaque JSON answers and request hashes including model and endpoint identity. Each successful answer is persisted in the work phase, preserving completed chunks on retry. Entries expire after the idle lifetime set by `RESULT_CACHE_TTL`, and the retention job collects expired entries. `search.enrich` writes the chunk's `enrichment`, which changes its input hash, so `search.embed` after it embeds the enriched text. The chat provider is `openai` (any OpenAI-compatible `/v1/chat/completions`), sharing the HTTP client, retries and `Retry-After` handling of the embeddings client. The examples are `samples/workflows/receipts.json` and `contracts.json`.

Chunkers are a Go interface, `Chunker{ Key() string; Split(text Text, o Options) ([]Chunk, bool) }`. `window` is an exact port of PaperDotNet's `Passages.Windows` and `Normalize`, kept identical by a golden test. The others split by page, by paragraph (merged up to the size), by heading, or not at all. Metadata text is never chunked: `page` is null for matches outside the file.

#### Reading text and where it is kept

- **PDF reading.** PDFium chromium/8076 through `klippa-app/go-pdfium`'s cgo backend, behind `doctext.PDF`. The linked SDK provides text extraction and page rendering for OCR. The native installer, Docker image and release archives include its library and notices.
  - **Not chosen:** PDFium as WebAssembly in wazero (no native library, but a few seconds to compile per process and about 2× slower; tried first and replaced), pdfcpu (processes PDFs but neither extracts text nor renders pages), and a pure-Go text reader (no rendering, so scanned PDFs would stay without text).
  - **Alternative:** a pure-Go text-layer reader, with no rendering. Scanned PDFs would then stay without text until a renderer exists.
- **No OCR configured.** When `ONNXRUNTIME_LIB` is empty, `ocr: auto` reads text layers only and reports the pages it skipped. It does not fail.
- **Storage.** One row per physical file, `blob_texts(sha256, pages, chars, language, extractor, models, object_key)`, in `papergo.db`. The page texts are one JSON object in the blob store, written in the work step and pinned by the row, so large text stays out of the database.
  - `text_id` names the row, so later nodes and chained workflows read the text by reference.
  - Identical files share one extraction, so a re-upload skips the work.
  - Different processes may ask for different OCR settings, so the key includes the extractor settings: an `ocr: always` run never reuses a text-layer-only result.
- **Portability.** Content-package export writes each `blob_texts` entry next to its file bytes, with the extractor and model SHA-256. Import restores it. Text is source data, unlike search indexes.
- **Language.**
  - the file version's language hint (`languages`, `X-OCR-Languages`), mapped to Snowball names;
  - otherwise a cheap detector limited to the configured languages (P2; for example `github.com/pemistahl/lingua-go`, Apache-2.0);
  - otherwise none.

#### The default built-in, `search.content` ("Index file content")

- **Scope:** workspace-wide, on by default, filling the role `search.content`; collections can override it (E5).
- **Triggers:** `file.added` with all sources (reading text never replaces the file, so it cannot loop), `media_types` (`application/pdf`, `image/*`), and `manual`.
- **Runs:** concurrency `coalesce`, on the system queue.
- **Parameters:** `ocr`, `chunker`, `max_chars`, `overlap`, `max_chunks` and `max_pages`.
- **Outputs:** `blob_id`, `text_id`, `pages`, `language`, `generation`, `chunks`. This is the role's output contract (E1).

```jsonc
{ "start": "read", "nodes": {
  "read":    { "activity": "document.read_text", "inputs": { "ocr": "{param:ocr}" },
               "next": { "done": "chunk", "no_text": "end", "unsupported": "end" } },
  "chunk":   { "activity": "search.chunk", "inputs": { "chunker": "{param:chunker}", "max_chars": "{param:max_chars}" }, "next": { "done": "embed" } },
  "embed":   { "activity": "search.embed", "next": { "done": "publish", "deferred": "publish" } },
  "publish": { "activity": "search.publish" },
  "end":     { "activity": "end" } } }
```

#### Examples of changes people can make

The process is personalizable with ordinary workflow editing, with no code:

| Need | Change |
| --- | --- |
| A library of photos that needs no text | Turn `search.content` off for that library (E5). Names and fields stay searchable |
| Archives, audio or other files never read | `media_types` on the trigger (E6), or an `if` on `{trigger:data.content_type}` |
| Scanned letters: always OCR | Parameter `ocr: always` |
| Receipts: one chunk with a summary | `chunker: whole`, then `ai.complete` ("summarize in two sentences"), then `search.chunk` with `source: "{step:summary.text}"`, `kind: summary` and the same `generation` |
| Contracts: sections with context | `chunker: headings`, `max_chars: 2000`, then `search.enrich` ("one sentence: which contract and clause") |
| Poor OCR: clean the text first | `ai.complete` on `text_id` with a cleanup prompt, then `search.chunk` with `source` set to its text |
| Extract fields with a model, as part of indexing | A node that calls `ai.complete` with an `output_schema`, then `item.update` with the JSON. The item's `item.updated` re-runs `search.index`, never `search.content` |
| Something after indexing | Another workflow on `role.search.content.completed`, reading `{trigger:data.outputs…}` (E1) |

Turning the role off for a collection removes that collection's file text from search, and the collection's settings show it (E5). The embedding sweep and the reindex still cover what the active process produced.

### 7.4 What a custom process cannot change

Personalization changes quality and cost, never permissions or inclusion.

- **`search.sync` builds a document only from data the engine reads itself:** scope, surfaces, views, workspace, collection and revision. Inputs can add text, never access data. It writes only the run's item.
- **`search.publish` checks first:** the run's item is live; one of its surfaces still references the blob; and the generation belongs to this run. Otherwise it follows `stale` and changes nothing. A slow run cannot overwrite a newer file's content.
- **Every query authorizes in the main database** (sections 5 and 8). An index that a custom workflow left incomplete or stale can hide results, never reveal them.
- **Scope and surface come from `search.sync` alone.** Content activities never write them: chunks inherit visibility from the documents that reference their blob.
- **Exactly once:** `generation` is derived from the run ID and the step ID that started it, so a replayed step stages the same generation again.

### 7.5 Reindex, sweep and copying

- **Reindex:** `POST /v1/search/reindex` (`organization.manage`) is the operation `search.reindex`, also available as `papergo-admin search reindex`.
  - **Metadata:** it starts the active `search.index` filler's manual trigger for every live resource, in bounded batches (E5 decides which workflow that is per collection). Then it deletes documents not touched since the operation started, by stamp. Because it goes through the active workflow, customizations such as `extra_keywords` apply.
  - **Content, with `content: true`:** it raises `file.added` with the source `reindex` for the current file of items whose file version has no published content, or of all items with `force: true` after a process change. Dispatch then starts whichever workflow fills `search.content` for each collection (built in P4; only triggers that list the `reindex` source react).
  - Search stays complete during a reindex, and vectors are found again by hash.
- **Embedding sweep:** a DBOS-scheduled system function, `papergo.search_embed`, every 15 minutes. It embeds published chunks without a vector of the current model, 64 per call and at most 2,000 per run. It covers `deferred`, `later` and model changes.
- **First start or lost store:** a missing or outdated `search.db` starts the reindex on start-up. Search, global and in collections, returns `503 search_unavailable` until metadata is complete.
- **Copying:** `papergo-admin search copy --to zvec`, offline, through `Export`. Nothing is computed again.
- **Imports:** a content-package import restores `blob_texts` and starts a reindex with `content: true`, so OCR does not run again; only chunking and embedding do.

## 8. Collection queries and listings on the store

`item_surface_search` and `resource_search`, their triggers and their SQL go away. `search` in `POST /v1/resources/{id}/query`, in views and in smart folders, and `q` in listings, now read the search store:

1. The query compiles as today. Where it used `p.rowid IN (SELECT rowid FROM item_surface_search WHERE … MATCH ?)`, it now calls `store.Match` first, with the collection, `parent_id`/`ancestor_id`, the surface and the caller's scopes. `Match` returns up to `SEARCH_MATCH_LIMIT` resource IDs (default 10,000).
2. The IDs go into the SQL as one JSON parameter (`r.id IN (SELECT value FROM json_each(?))`). Everything else is unchanged: permissions, surfaces, filters, sort, keyset paging, totals and group counts are computed in `papergo.db` over those candidates. The results are therefore authoritative even when the index lags.
3. When `Match` was capped, the response says so:
   - `search_capped: true` on the page;
   - totals are `total_capped`, with the existing meaning.

   Narrowing the words or filters gives complete results. Cursors bind the ID set's hash, so paging is stable across pages of one search.
4. Listing semantics stay: the input is a literal phrase whose last word matches as a prefix. `MatchQuery.Literal` asks for that, and nothing the user types is parsed as query syntax there.
5. A collection where `search.index` is turned off returns `400 search_off` for `search` and `q`, instead of empty results.
6. Resources that are not items or folders (workspaces, lists, libraries) are few and their names short. Listings of them filter with `instr(lower(name), ?)` over the already-authorized rows, so they need no index.

**What changes for people:**

- **Content:** `search` in a library now also matches **file text**: the published chunk text is part of `doc_fts`.
- **Freshness:** a write is findable once `search.index` has run, normally within a second; before, it was findable within the same transaction. Edit screens show the item from the API, not from search, so this affects only search results.
- **Cap:** a common word in a very large collection is capped at 10,000 candidates, where today it is exact.

**Optional optimization (P1 decides after measuring):** with the `sqlite` store, the read connection of `papergo.db` can `ATTACH` `search.db` read-only. `Match` then becomes a sub-select in the same statement, with no cap and no ID transfer. zvec always uses the ID path. Both must give the same results, which a test covers.

## 9. Embeddings (`internal/ai`)

- **Providers (decided in P5):** `local` runs EmbeddingGemma 2 or Harrier in the process through `internal/embedding` (ONNX Runtime shared with OCR, `AI_EMBEDDINGS_MODELS_PATH`; key `local:{engine ID}`, query and document prompts of the model, a chunk over the token limit embedded by its beginning); `openai` is the HTTP client below. Both sit behind `ai.Embedder`, with one query cache.
- **Configuration:**
  - `AI_EMBEDDINGS_PROVIDER=none|local|openai` (`none` by default);
  - `AI_EMBEDDINGS_ENDPOINT` (any OpenAI-compatible `/v1/embeddings`: OpenAI, Ollama, LM Studio, vLLM, llama.cpp);
  - `AI_EMBEDDINGS_MODEL`;
  - `AI_EMBEDDINGS_API_KEY_FILE`, read like `PROXY_SECRET_FILE`;
  - `AI_EMBEDDINGS_DIMENSIONS`, optional; when it is not set, it is measured once.
- **Client:** `net/http` and `encoding/json` only. Batch requests, a timeout, and `Retry-After`-aware retries for 429 and 5xx. Vectors are normalized when they are stored. The model key is `{provider}:{model}:{dims}`.
- **Query embeddings:** cached in an LRU (30 minutes, about 1,000 entries). The text is the semantic form of `q`: operators, quotes and excluded words removed, as in `SemanticText`.
- **Garbage collection:** vectors whose `input_hash` no chunk uses are deleted by the sweep after 7 days, so a re-upload or an undo does not pay for embedding again.

## 10. Global search API

`GET /v1/search`, or `POST /v1/search` with a JSON body when a `filter` is given. Both need `item.read`; reindex and status need `organization.manage`.

| Parameter | Meaning |
| --- | --- |
| `q` | Words (all must match), `"phrase"`, `a OR b`, `-word` / `NOT word`, `prefix*`. At most 256 bytes and 32 terms. Optional when a filter is given |
| `mode` | `keyword`, `semantic` or `hybrid`. The default is hybrid when embeddings are configured, else keyword |
| `surface` | `auto` (default), `head` or `published` |
| `kind` | `item` (default), `folder` or both |
| `workspace_id`, `collection_id`, `content_type_id`, `keyword`, `created_by`, `modified_by`, `modified_from`, `modified_to` | Filters |
| `term_id` | Matches the term, its descendants and terms merged into it |
| `filter` (POST body) | PaperGo filter nodes on search fields: `{"field":"amount","op":"gt","value":"100"}`. At most 32 nodes and depth 6. Unknown fields return 400 |
| `$top` (1–100, default 25), `$skiptoken` | Paging. The token is opaque and binds the caller, the query and an offset into the candidates |
| `facets` | `true` by default |

- **Each hit** has `id`, `kind`, `surface`, `workspace_id`, `collection_id`, `content_type_id`, `name`, `snippet`, `highlights`, `page`, `matched_by`, `rank`, `modified_at` and `modified_by`.
- **The response** has `value`, `total`, `total_estimated`, `facets` (workspace, collection, content type, terms with labels when the caller has `taxonomy.read`, and keywords), `mode` and `@odata.nextLink`.
- `GET /v1/search/fields` lists the filterable search fields.
- `GET /v1/search/status` reports the store, the queued `search.index`/`search.content` runs, chunks without vectors, the model, the reindex in progress, the size and the vector memory.
- **Failure modes:**
  - semantic or hybrid without a model returns 400 on `mode`;
  - an unavailable store returns 503 `search_unavailable`;
  - an embedding failure at query time falls back to keyword and reports `mode: keyword`.
- **Contract and UI:** OpenAPI in `api/openapi.json`. Web: a search page with facets, snippets, page hits that open the viewer at that page, and the command palette.

## 11. Licenses

**Go modules:** no new direct module for the stores:

- SQLite uses `modernc.org/sqlite`, including DBOS's SQLite driver; native bindings use maintained cgo SDKs;
- the Snowball stemmers are `github.com/blevesearch/snowballstem` (BSD-3-Clause, recorded in `docs/dependencies.md`); the language-detection library is checked when P3 needs it.

**zvec native library.** Statically included, from PaperDotNet's spike (S9):

| Component | License |
| --- | --- |
| zvec, CRoaring, FastPFOR, RaBitQ-Library, Arrow, Thrift | Apache-2.0 |
| RocksDB | dual GPL-2.0 / Apache-2.0, used under Apache-2.0, with LevelDB's BSD notice |
| cppjieba, limonp, magic_enum, yaml-cpp, utf8proc | MIT |
| antlr4 runtime, gflags, glog, sparsehash, snowball, re2, xsimd | BSD-3-Clause |
| lz4 | BSD-2-Clause |
| zlib | zlib License |
| Boost | BSL-1.0 |

**Review: acceptable.**

- **zlib License** is permissive. It requires only that the origin is not misrepresented and altered versions are marked. Source notices must stay in source distributions; binary distribution needs no notice.
- **BSL-1.0** is permissive and OSI-approved. Its notice is required with source copies, not with binaries ("unless such copies or derivative works are solely in the form of machine-executable object code").
- Neither is copyleft, and neither restricts linking or the license of PaperGo. Both are compatible with shipping alongside Apache-2.0 and MIT code.

**What we do anyway:**

- ship a `THIRD-PARTY-NOTICES` file for `libzvec_c_api` in the image next to ONNX Runtime's `ThirdPartyNotices.txt`, listing every component above with its license text. It is generated by the CI build from the pinned sources;
- record RocksDB's dual license and our choice (Apache-2.0);
- never link or ship libaio (LGPL): zvec `dlopen`s it only when present.

**PDFium** (chosen in P3, through cgo SDKs) is BSD-3-Clause and Apache-2.0, plus the notices of its bundled libraries (FreeType FTL, libjpeg-turbo, OpenJPEG, lcms2, zlib), all permissive. It ships with its own notices file.

**A written allow list.** `docs/dependencies.md` gets one: MIT, Apache-2.0, BSD, ISC, zlib, BSL-1.0 and PostgreSQL allowed; GPL, LGPL, AGPL and SSPL not linked. Its one exception is MPL-2.0 inside the ONNX Runtime binary (Eigen, header-only), which is file-level copyleft and satisfied by shipping the notices and pointing to the unmodified source.

## 12. Tests

- **Conformance suite** (`internal/search/storetest`): `Run(t, func(t) search.Store)`. Both stores run it.
  - Ported from PaperDotNet:
    - keyword search on name, body, keywords and pages; name ranks above body;
    - query syntax; trimming by scopes;
    - each filter, and filters alone listed newest first; paging and counts;
    - the facets; upsert replaces;
    - deletes; scope moves; term counts;
    - field filters by kind;
    - semantic search with a page; hybrid search, where exclusions still exclude;
    - reuse of vectors.
  - New for PaperGo:
    - **surfaces and views:** a published-only reader never matches head text;
    - folders; the null scope;
    - **exact** integer, decimal and number boundaries (`9007199254740993`, scale 9);
    - staged content invisible until published; a `stale` publish;
    - content of an unreferenced blob never matched;
    - vector reuse by `input_hash`;
    - reindex by stamp without a gap;
    - 20,000 readable scopes;
    - `Match` with `parent_id`, `ancestor_id`, the literal-prefix rule and the cap;
    - an `Export` round trip into the other store.
  - **How each store runs:** both stores use the installed native build environment. Native zvec tests run without a library-path setting or skip gate.
- **Collection query parity:** the existing `search` tests of collection queries, views, smart folders, listings and WebDAV-adjacent listings run against both stores, with a helper that waits for `search.index` to finish. Results, ordering and totals must equal what the FTS5 tables gave, except that file text now also matches.
- **Unit tests and fuzzing:**
  - parser, `SemanticText`, snippets, RRF;
  - chunkers (golden files against PaperDotNet);
  - the field filter compiler;
  - `go test -fuzz` on the parser, the FTS5 `MATCH` renderer and the zvec filter builder.
- **Engine tests** (`internal/workflow`, `internal/runner`):
  - **outputs:** evaluation in `finish`; schema failure fails the run; `data.outputs` in `wf.{key}.completed`; a role filler with incompatible outputs is refused on save;
  - **per-item `permissions.changed`:** descendants at several depths; nested resources with their own permissions skipped; expansion resumes after a crash; no expansion without subscribers; `actions` filter;
  - folder events; `coalesce`; workspace built-in with a collection override and a collection turned off; `media_types`.
- **Workflow tests** (`internal/runner`, real runner):
  - `search.index` keeps the store in step through create, update, move, publish, unpublish, delete, restore, purge, folder rename, scope change, term rename and term merge;
  - `coalesce` collapses a burst to at most two runs with the final state;
  - `document.read_text` on a text PDF, a scanned PDF (OCR), an image, a composed PDF (no second OCR), and with OCR off;
  - a customized `search.content` (receipt summary chunk, `headings` chunker, a skipped media type) and a chained workflow reading its outputs;
  - `search.publish` refuses a stale generation from a slow custom run;
  - `search.content` with a fake OpenAI-compatible server (`httptest`, deterministic vectors from token hashes);
  - a cancelled `search.content` run leaves only an unpublished generation.
- **API tests:**
  - a removed grant hides the hit on the next request;
  - draft and published readers;
  - the capped `Match`;
  - backup and restore with and without the search store;
  - export and import keep `blob_texts`.
- **Relevance set:** about 50 documents and 30 queries with the expected top 3, checked in CI.
- **Performance:** extend `cmd/loadtest` and `docs/performance.md` with:
  - request write throughput with and without `search.index`, and the indexing lag;
  - files per second for text and content indexing;
  - query p50 and p95 (keyword, filters, hybrid, collection `search`);
  - memory;
  - at 20k and 200k items, with 1k and 20k readable scopes, for both stores.

## 13. Phases

Sizes are rough working days for one person. Each phase updates its docs in the same change: a new `docs/search.md` describing the current behaviour only, plus `architecture.md`, `workflows.md`, `dependencies.md` and the OpenAPI contract. The FTS5 tables are deleted in the phase that replaces them (AGENTS.md).

| Phase | Scope | Exit criteria | Size |
| --- | --- | --- | --- |
| **P0 Spike** | `contentless_delete` FTS5 in modernc; `SearchScopes` cost at 20k scopes; pure-Go dot product at 100k × 768; the zvec cgo SDK (string arrays and native filters); zvec string range comparison; PDFium through cgo SDKs (text and rendering) versus a pure-Go reader; Snowball and language-detection libraries | A short results section in this plan; go or no-go per choice | 4 |
| **P1 Engine additions and keyword search** | E1 workflow outputs and role output contracts; E2 per-item `permissions.changed` with dispatcher expansion and `actions`; E3 folder events; E4 `coalesce`; E5 workspace built-ins with collection overrides and the roles view; system queue and short retention for built-in runs; `internal/search` contracts, parser and service; `sqlitestore` (documents, terms, keywords, `doc_fts`, `Match`); `search.sync`, `search.items_with_term`; the `search.index` built-in; `dms.SearchScopes` and `VisibleSurfaces`; `GET /v1/search` (keyword, filters, facets, paging); **collection queries and listings move to `Match`, and `item_surface_search` and `resource_search` are deleted**; reindex and start-up rebuild (backups leave `search.db` out; a restore removes it, so it rebuilds at start); conformance (keyword part) | Global search works across workspaces, trimmed by permissions and surfaces; collection-query parity tests pass on the store; turning `search.index` off for a collection gives `search_off`; the write-throughput criterion of 7.2 holds | 16 |
| **P2 Fields and languages** | `document_fields` with exact kinds; `filter` on search; `/v1/search/fields`; the `search_weight` option; Snowball stemming columns; `/v1/search/status`; web search page | Conformance field tests pass; `invoices` finds `invoice` in the configured languages | 6 |
| **P3 File text** | E6 `media_types`; `internal/doctext` (PDF reader as decided in P0, OCR through the port); `document.read_text`; `blob_texts` with export and import; language hints and detection | A text PDF, a scanned PDF and a photo get page text; a composed PDF is not OCRed again; an import keeps text | 7 |
| **P4 Content as workflows** | Chunks and `chunk_fts`; `search.chunk` (all sources and kinds), `search.publish`; the chunkers; the `search.content` built-in with its outputs; page hits and snippets; file text in `doc_fts`, so collection `search` matches content; reindex with `content: true` | Uploading a PDF makes its text findable with `page`, also in the library's search; a library overrides the chunker; a chained workflow reads `text_id`; a rename re-chunks nothing | 6 |
| **P5 Semantic and hybrid** | `internal/ai` embeddings; `vectors` and the in-memory index; `search.embed` on staged generations; the sweep; semantic and hybrid modes with RRF and `matched_by` | Semantic and hybrid conformance tests pass with the fake server; memory reported | 6 |
| **P6 zvec store — implemented** | cgo SDK, layout, writer, queries including `Match`, backup; CI native build in the image; notices; `search copy` | Full conformance and collection-query parity on zvec; soak test with real dimensions; documented as one node | 10 |
| **P7 AI steps** | `ai.complete` (chat provider, `AI_CHAT_*`, cached by hash, JSON answers with `output_schema`); `search.enrich`; the receipts and contracts examples of 7.3 as samples | The examples run end to end against a fake chat server; equal inputs reuse unexpired results | 5 |

P1 and P2 give global search over all items and folders, and move collection search to the store. P3 to P5 add files and meaning. P6 can start after P1, in parallel. P7 makes the AI steps in section 7.3 real.

## 14. Decisions

- **D1. All indexing is asynchronous workflows that people can change.** `search.index` (basic data) and `search.content` (files) are built-ins filling roles. Each can be turned off, copied and recomposed per workspace, with collection overrides. Workflows chain by events with declared outputs. What must not vary is enforced by the fixed checks inside `search.sync` and `search.publish` and by query-time authorization, not by locking the workflows. `coalesce`, the system queue and short retention bound the cost on the main writer, and P1 measures it.
- **D2. Trim in the store, authorize in the main database.** The store's scopes give candidates, counts and facets. Global search checks the returned page against `papergo.db`. Collection queries run their normal SQL over the store's IDs. Visibility is never eventual.
- **D3. The search store is not managed by Atlas.** It is disposable derived data, rebuilt by a reindex. The store owns its schema and recreates itself when the schema version changes. The single migration file keeps covering `papergo.db`, which now gains `blob_texts` and loses the two FTS5 tables.
- **D4. Exact numbers.** The `sqlite` store keeps int64 and decimal semantics. zvec is approximate for `number` and `currency` only if P0 finds no exact option, and that is documented.
- **D5. The FTS5 tables in `papergo.db` are dropped.** The search store is the one full-text index for collection queries, listings and global search. Collection queries keep their exact SQL for everything but the word match. They gain file text and lose same-transaction freshness and uncapped word matches (section 8).
- **D6. Two stores, built separately.** `sqlite` is the default, and `zvec` is optional, loaded through cgo SDKs like ONNX Runtime. They share only the contract and the conformance suite. One store per deployment (`SEARCH_STORE`), and one embedding model per store.
- **D7. Page text is source data.** `blob_texts` lives in the main database and the blob store, travels in content packages, and is shared by identical files. Search chunks and vectors are derived and never exported.
- **D8. `permissions.changed` is per item.** The request commits one root event, and the dispatcher expands it to every affected item and folder in resumable batches, only when someone listens. Workflows see ordinary item events, and requests stay small however large the subtree.

## 15. Risks and open questions

- **Main-writer load from `search.index`.** One run per change is the price of D1. If P1's measurement misses the 15% criterion, the fallbacks, in order, are:
  1. batch runs per collection, so one run syncs all items that changed since the last;
  2. let `search.sync` skip the step marker for read-only preparation.

  The model stays a workflow either way.
- **Permission fan-out volume.** Breaking inheritance on a 200,000-item library becomes 200,000 `search.sync` runs.
  - The dispatcher expands in batches and the system queue keeps people's workflows moving.
  - Until the runs finish, the store's scopes are stale, which costs only recall, never visibility, because of D2.
  - If P1 shows the lag is too long, `search.sync` gets a batch form: one run per expansion batch of 500, reading the batch's items on one snapshot.
- **Personalized processes can leave gaps.** A custom `search.content` that never reaches `search.publish` leaves files without content. `/v1/search/status` reports per collection which workflow fills each role, and how many current files have no published content. The collection settings show the same.
- **Collection search caps.** `SEARCH_MATCH_LIMIT` bounds `Match`. The `ATTACH` path removes the cap for `sqlite` if measurements favour it.
- **Rebuild window.** After a lost `search.db`, collection `search` returns 503 until metadata is rebuilt (minutes at 200k items). Backups leave the store out, so a restore pays this window once; including it is a later option.
- **`doc_fts` rewrite cost.** A metadata change on an item with a large file re-tokenizes its published chunk text. If that hurts, split it into a metadata-only table and a per-blob content table that changes only on publish.
- **Readable scopes at scale.** Long scope lists are passed as JSON parameters on SQLite and `IN` lists on zvec (60–75 ms at 20k in PaperDotNet's spike), and measured in P0 and P1.
- **Native code in the process.** ONNX Runtime, PDFium and zvec run in-process: a crash in one stops PaperGo. Each stays behind a port and is pinned; PDFium reads untrusted PDFs, so its build is updated with PaperGo's releases. Each stays behind a port, pinned and fuzzed at its builders. zvec, pre-1.0, ships only after a soak test.
- **Open questions:**
  - ~~Should `search.enrich` text appear in snippets, or only be used for ranking?~~ Decided in P7: it is matched and embedded, and snippets show only the file's text.
  - Should `max_chunks` and `max_pages` stay parameters of the built-ins, or become library settings?
  - Should the `ATTACH` optimization become the only `sqlite` path?

## P6 implementation and validation

`internal/dal/search/zvecstore` implements the full contract independently through cgo SDKs. Native source records keep documents, staged/published chunks, enrichment and cached vectors; native FTS and cosine HNSW serve queries. `SEARCH_STORE` selects the backend, and the native directory is owned by one process. Offline CLI copy and native backup/restore preserve chunks and vectors without provider calls.

Two layout refinements preserve the contract with the pinned C API: encoded whitespace FTS tokens provide exact words, stems and tiny prefixes instead of trigram substring matches; exact typed fields use the native source-record catalog to restrict document keys before FTS/HNSW candidate selection, because dynamically adding STRING columns is unsupported. Neither number nor currency filters use floats. A separate vector collection contains only embedded passage rows and carries each surface's metadata, so scope/filter restrictions apply before ANN top-k. Native index maintenance seals/optimizes buffered writes every 30 seconds and on close. Cached model activation rebuilds the native vector space without embedding calls.

The pinned SDK archive checksums are in `scripts/native.json`; `scripts/native.py` installs the SDKs and copies native notices. CI runs conformance, lifecycle/crash, offline copy, native backup, service/HTTP/workflow parity, fuzz builders and real-dimension soak tests. Docker and native releases bundle the libraries. SQLite remains the default store.

Measured local soak results and reproduction commands: [zvec validation](../performance-artifacts/zvec/README.md). This is a single-node backend; native index/catalog overhead is additional to cached vector bytes reported by the API.
