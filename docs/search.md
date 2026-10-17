# Search

PaperGo keeps a search index of every item and folder in a store of its own, next to the database. Global search (`GET /v1/search`) and the `q` search of collection queries and resource listings both read it. The index is kept current by a [workflow](workflows.md) that people can see, turn off per collection, or replace, and every query is authorized against the database as it is now, whatever the index holds.

## What is indexed

The index holds one **document** per item or folder and surface. A collection with publishing has a `head` document (the draft) and, once published, a `published` one. A collection without publishing keeps only the `published` surface, which is also what head reads use.

| Part | From |
| --- | --- |
| Name | the surface's name |
| Keywords | the surface's tags, and the names, localized labels and synonyms of the terms in its term and keywords fields; matches here and in the name rank highest |
| Body | the text of the surface's text, note, email, URL, choice and number fields. Dates, booleans, people and lookups are not text; term and keywords fields give their words to keywords. A field's `search_weight` moves its text to keywords (`high`) or leaves it out (`none`) |
| Stems | the words of the name, keywords and body as their stems in each of `SEARCH_LANGUAGES` |
| Fields | every field value except notes, typed as collection queries index them, for [field filters](#field-filters) |
| Filters | kind, workspace, collection, parent folder, content type, tags, terms, creator, last modifier, creation and modification times |
| Access | the entry's permission scope |

The text of files is indexed too, as [file content](#file-content), separately from metadata so it is searched only when the caller has `document.read`.

**Field weights.** `search_weight` is a field option (`none`, `normal`, the default, or `high`). Search reads each field's current weight when it indexes an entry, so after changing it, entries follow as they change; `POST /v1/search/reindex` applies it to all of them at once.

**Languages.** `SEARCH_LANGUAGES` lists the Snowball languages whose stems are indexed, comma-separated (default `english`; `none` indexes words only): arabic, danish, dutch, english, finnish, french, german, hungarian, irish, italian, norwegian, portuguese, romanian, russian, spanish, swedish, tamil and turkish. A word or phrase in a query matches itself or its stem in any of them, so `invoices` finds `invoice`; words that match as they are rank above words that match by stem. Prefixes (`invo*`) match words only. Changing the languages rebuilds the index at startup.

## Keeping it current

The **`search.index`** [built-in workflow](workflows.md#built-in-workflows) fills the `search.index` [role](workflows.md#roles). It is on by default in every workspace, runs on the system queue as the system subject, and `coalesce`s: a burst of changes to one entry costs at most two runs, and the last one sees the final state.

- **Triggers.** Item and folder changes (`created`, `updated`, `published`, `unpublished`, `deleted`, `restored`, `purged`), `permissions.changed` (once per affected entry, for `replace` and `move`), `term.changed` and `term.merged`, and `manual`.
- **`search.sync`** re-reads the entry and writes its documents, or removes them when it is gone. It reads state, not the event, so running it again, late or out of order is harmless.
- **Terms.** A term whose name, labels or synonyms change, and both terms of a merge, are re-read for every item that holds them or an alias merged into them: `search.items_with_term` lists them 500 at a time for `search.sync` in a `forEach`. A collection with its own `search.index` filler is left to it.
- **Turning it off.** `PUT /v1/workspaces/{id}/workflow-builtins/search.index` with a `collection_id` and `"enabled": false` gives the collection its own, disabled filler. Searching inside that collection then answers `400 search_off`, because its documents would go stale; global search keeps its entries as last indexed, still trimmed by current permissions. Copying the built-in replaces it with an editable workflow that fills the same role.

Because indexing is asynchronous, a change appears in search after its run, usually within a second.

## File content

The **`search.content`** built-in fills the `search.content` role. It is on by default in every workspace, runs on the system queue and `coalesce`s. When a PDF or an image is added (uploaded, through WebDAV, restored, from a workflow, or by a content reindex), it:

1. reads the file's text with [`document.read_text`](workflows.md#reading-text) (text layers, OCR for scans and photos when configured), reused for identical files;
2. splits it into chunks with [`search.chunk`](workflows.md#file-content-steps), under a new generation, staged and not yet searched;
3. gives the chunks their vectors with [`search.embed`](workflows.md#file-content-steps), when an [embedding model](#semantic-and-hybrid-search) is configured;
4. makes them the file's searchable content with [`search.publish`](workflows.md#file-content-steps), replacing the content it had.

Its parameters are `ocr`, `max_pages`, `chunker` (`window`, `pages`, `paragraphs`, `headings`, `whole`), `max_chars`, `overlap`, `max_chunks` and `embed` (`immediate` or `later`). A list or library can turn it on with other parameters, turn it off, or copy and recompose it (for example adding a summary chunk). Its runs end with outputs (`blob_id`, `text_id`, `pages`, `language`, `generation`, `chunks`), and every filler of the role raises `role.search.content.completed`, so a workflow can follow content indexing in any collection.

- **Content belongs to a file version.** A new file starts without content until its run publishes; a rename or a field change rewrites the document from the stored chunks and reads nothing again. Content of versions no surface references is dropped.
- **Fixed checks.** `search.publish` publishes only a generation of its own run, for a file one of the item's surfaces still has; otherwise it follows `stale` and changes nothing. Access always comes from the item's document, never from content.
- **Page hits.** When the file's text matches, a hit gives the `page` of its best matching chunk and cuts the snippet from that chunk.
- **Enrichment.** Context that [`search.enrich`](workflows.md#ai-steps) writes for a chunk is searched and embedded with it, but snippets show only the file's own text.

## Authorization

Search never shows more than the caller may read now:

1. Before a query, PaperGo decides once per permission scope whether the caller reads drafts there, published content only, or nothing. The store keeps only matching documents of those scopes, on the surface the caller reads: `auto` gives draft readers the head and other readers the published surface.
2. Each hit is checked again in the database: an entry that was deleted, moved out of reach or lost its publication since it was indexed is left out, and the result says `total_estimated: true`.
3. Facets are returned only after every matching source, including entries outside the current page, passes the same check. When a stale source is found, facets are omitted and `total_estimated` is true until the index catches up.

File text participates in matching and snippets only with `document.read`. A caller with `list.read` alone can search metadata, including in collection queries and resource listings.

A revoked permission or membership therefore applies to the next search at once, before anything is reindexed.

## Global search

`GET /v1/search` finds items (and, with `kind=folder` or `kind=item,folder`, folders) across every workspace the caller reads. `POST /v1/search` takes the same parameters as a JSON body (`kind` as an array), plus a `filter`; `$top` and `$skiptoken` stay in the URL, and a next page repeats the body. Both need `list.read`.

| Parameter | Meaning |
| --- | --- |
| `q` | words; see below. Without `q`, at least one filter is required |
| `mode` | `keyword`, `semantic` or `hybrid`; the default is `hybrid` when an embedding model is configured and `q` is given, else `keyword` |
| `surface` | `auto` (default), `head` or `published` |
| `kind` | `item` (default), `folder`, or both comma-separated |
| `workspace_id`, `collection_id`, `content_type_id` | restrict to one |
| `term_id` | items holding the term, a descendant of it, or a term merged into it |
| `tag`, `created_by`, `modified_by` | exact values |
| `modified_from`, `modified_to` | RFC 3339 times |
| `facets` | `false` leaves out the facet counts |
| `$top` | page size, default 25, at most 100 |
| `$skiptoken` | continues a search from `@odata.nextLink`; it is bound to the caller and the query, so it cannot page another search |

`q` takes words, which all must match, quoted `"phrases"`, `OR` between words, `-word` or `NOT word` to exclude, and `word*` for a prefix; at most 32 terms and 256 bytes. Matching ignores case and diacritics.

The response holds `value` (hits), `total`, `mode` (the mode the search ran in), `facets` (counts by workspace, collection, content type, term and tag) and `@odata.nextLink`. Each hit gives `id`, `kind`, `surface`, `workspace_id`, `collection_id`, `content_type_id`, `name`, `rank`, `modified_at`, `modified_by`, `matched_by`, and a `snippet` around the first match with `highlights` (byte ranges in the snippet): from the best matching chunk of the file, with its `page`, when the file's text matched, else from the body. Hits are ordered by relevance, the name and keywords weighing ten times the body. `matched_by` says how a hit was found: `keyword`, `semantic` or both.

## Semantic and hybrid search

With an embedding model, search also finds file content close in meaning to `q`, without every word in it.

- **Model.** `AI_EMBEDDINGS_PROVIDER` chooses it, `none` by default:
  - `local` runs EmbeddingGemma 2 (default) or Harrier in the process, on the ONNX Runtime OCR uses ([embeddings](embeddings.md)): `ONNXRUNTIME_LIB`, `AI_EMBEDDINGS_MODELS_PATH` (defaults to `assets/models/embedding/{model}` beside the executable), and optionally `AI_EMBEDDINGS_MODEL`, `AI_EMBEDDINGS_DIMENSIONS` (Gemma: 768, 512, 256 or 128), `AI_EMBEDDINGS_THREADS` and `AI_EMBEDDINGS_MAX_TOKENS` (512). Nothing loads until the first embedding; a chunk longer than the token limit is embedded by its beginning.
  - `openai` calls any OpenAI-compatible `/v1/embeddings` endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp): `AI_EMBEDDINGS_ENDPOINT` (default OpenAI's), `AI_EMBEDDINGS_MODEL`, optionally `AI_EMBEDDINGS_API_KEY_FILE` and `AI_EMBEDDINGS_DIMENSIONS` (else measured once). Requests carry 64 texts, time out after two minutes, and are retried on 429 and 5xx, honouring `Retry-After` up to 30 seconds.
- **Vectors.** Chunks are embedded by their text, not their file's name, and vectors are kept per chunk input and model: identical text embeds once, a rename or reindex embeds nothing, and another model embeds everything again under its own key (`{provider}:{model}:…`). Vectors are normalized float32. SQLite compares the model's vectors in memory; zvec uses a native HNSW index. Status reports the cached vector bytes (vectors × dimensions × 4), excluding native index overhead.
- **When content is embedded.** `search.embed` embeds before publishing. With `embed: later`, or when the model fails, content is published without vectors and keyword search finds it at once. The embedding sweep (every 15 minutes) embeds published chunks without a vector of the current model, 2,000 per run, which also covers a model change. Vectors no chunk has used for 7 days are deleted then.
- **Semantic.** The query's words, without operators, quotes and excluded words, are embedded (cached for 30 minutes) and searched against the chunk vectors. A file matches with its most similar chunk at a cosine similarity of at least 0.3, which gives the `page` and the snippet. Up to 200 entries, best first; excluded words still exclude. Only file content is embedded, so metadata alone never matches semantically, and file content needs `document.read`.
- **Hybrid.** The best 200 keyword matches and the best 200 semantic matches are fused by reciprocal rank (k = 60): an entry found both ways ranks highest. `total` and facets cover the fused entries.
- **Failures.** `semantic` or `hybrid` without a model is a validation error on `mode`. When embedding the query fails, the search runs by keyword and says `mode: keyword`. A next page continues in the first page's mode.

## Field filters

`filter` (POST only) filters on fields, in the [collection query](application-foundation.md) filter language: `{"field": "amount", "op": "gt", "value": 100}`, groups `{"and": [...]}`, `{"or": [...]}` and `{"not": {...}}`, at most 32 nodes and depth 6. Values compare exactly, as in collection queries: integers past float precision, decimals at their scale, numbers and currencies by value, dates and times in UTC.

| Field types | Operators |
| --- | --- |
| text, email, URL, integer, decimal, number, currency, date, datetime | `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `in`, `present`, `missing` |
| choice, boolean, person, lookup | `eq`, `ne`, `in`, `present`, `missing` |
| term, keywords | `eq`, `ne`, `in`, `under` (the term and its descendants), `present`, `missing`; terms merged into a term count as it |

- `ne` means the field is present and no value equals, as in collection queries; `missing` that the entry does not have the field.
- A key names the field in every list and library the caller may read (narrowed by `workspace_id` or `collection_id`); a key with several types or scales matches each. An unknown key, an operator the field does not take, or a value of the wrong type answers `422`.
- `GET /v1/search/fields` (optional `workspace_id`, `collection_id`) lists the fields the caller can filter on, with `key`, `label`, `type`, `scale`, `multiple`, `choices`, `term_set_id` and `operators`. Notes are text, not filters.

## Search in collections and listings

`q` on collection queries (`POST /v1/resources/{id}/query`) and on resource listings (`GET /v1/resources/{id}/children`, `GET /v1/resources?workspace_id=`) matches a literal phrase whose last word is a prefix, as people type: `acm` finds Acme. It matches words as they are, not stems, in names, fields and file content. The index supplies the matching entries of the collection (or workspace), up to 10,000; the query then authorizes, filters, sorts and pages them in the database as usual. When more entries match, the page says `search_capped: true` and covers only the first 10,000 matches; narrow the search. Workspaces, lists and libraries in listings match by name.

## The store

`SEARCH_STORE=sqlite|zvec` selects the store (`zvec` by default). Both implement keyword, semantic and hybrid search, exact field filters, permissions and surfaces. Both hold derived data independently of the main database.

| Setting | Default | Purpose |
| --- | --- | --- |
| `SEARCH_DATABASE_PATH` | `search.db` beside `DATABASE_PATH` | SQLite search file, with FTS5 and vectors |
| `SEARCH_ZVEC_PATH` | `search/zvec` beside `DATABASE_PATH` | Native zvec directory |
| `SEARCH_ZVEC_KEYWORD_CANDIDATES` | `10000` | Native keyword candidate cap (1–99,999); capped totals and facets set `total_estimated: true` |

SQLite does not load the zvec library. zvec is a **single-process, single-node** store: one API owns its directory, enforced with an exclusive lease. It serializes mutations through a bounded writer and queries a consistent index. Source/FTS records and embedded passages have separate native collections, so HNSW contains only real, non-null vectors. Every 30 seconds and on close, maintenance seals buffered writes and builds/compacts the configured indexes; recent writes are searchable before maintenance. Reads of an empty store do not create native collections. Incomplete native write batches quarantine the store; restarting rebuilds it from the main database.

zvec keeps source documents, staged and published chunks, enrichments and cached vectors in native records. Its two full-text fields, `metadata` and `content`, use encoded words, Snowball stems and prefixes, including one-letter prefixes, with exact phrase verification. Name and keyword ranking is applied in Go. Semantic search uses native cosine HNSW with scopes and surfaces applied before candidate selection. Typed field filters use an in-memory catalog of those source records to restrict native document keys before top-k; this preserves exact multi-value, int64 and decimal comparisons. It does not open SQLite. The catalog and native FTS/HNSW indexes add memory beyond the vector bytes in status.

On Linux/glibc, PaperGo limits native allocator arenas and trims unused allocator pages during the 30-second maintenance pass and on close. This releases temporary indexing/query buffers without unloading live indexes. The SDK buffer pool is capped at 128 MiB and respects the SDK's detected host/container budget; this does not cap total RSS. The SDK's RocksDB full-text tables still have a fixed memory cost, even for small collections. [Idle memory measurements](performance-artifacts/memory/README.md) distinguish that cost from Go heap and model memory.

After five minutes without a native search or write, maintenance flushes and closes both native collections. The idle deadline is checked every 30 seconds, after active readers and writers finish. The next full-text/vector search or write reopens the persisted indexes without rebuilding or embedding again. The source catalog and cached vectors stay in Go memory, so status checks, exports, embedding work lists and filter-only queries remain available without waking the native indexes. The store keeps its exclusive ownership lease until shutdown.

`python3 scripts/native.py` installs the pinned zvec v0.7.0 SDK for Linux amd64/arm64, macOS ARM64 and Windows amd64, along with the other native dependencies. Source `.tools/native/env.sh` or `.tools/native/env.ps1` before invoking Go directly. The Go SDK owns native calls, resource handles and thread-local errors; PaperGo uses its copied string-array setter for tag/term filters. CI tests real native stores. Platform ZIPs contain the native libraries and notices alongside the executables. Extract the shared assets ZIP into the same directory to supply models. The Docker image includes that directory layout under `/opt/papergo`.

Search paths must differ from the application database and its sidecar files, including through symbolic or hard links. The zvec directory must not overlap the database, blob directory or SQLite search file. Startup rejects unrelated databases at the search path.

- **Rebuild.** A new store, or one whose layout changed in a new release, is rebuilt from the database at startup. Until the rebuild completes, search answers `503 search_unavailable`; collection queries and listings without `q` keep working.
- **Reindex.** `POST /v1/search/reindex` (`organization.manage`) rewrites every document as an [operation](application-foundation.md) (202 with `Location: /v1/operations/{id}`), in batches of 200, and removes documents of entries that no longer exist. Search keeps answering meanwhile. With `{"content": true}` it also raises `file.updated` with the `reindex` source for the current file of every item without content (`"force": true`: every item), so the workflow filling `search.content` for its collection indexes it again; text read before is reused. A rebuild and an import do this too.
- **Status.** `GET /v1/search/status` (`organization.manage`) gives the store's name, whether it is rebuilding, its document and chunk counts and its size; with an embedding model, its `model` and `embeddings`: stored `vectors`, chunk inputs `pending` a vector, and the `memory_bytes` semantic search holds.
- **Backups.** SQLite search is excluded and removed on restore, then rebuilt. With `SEARCH_STORE=zvec`, stop the API first so the collection flushes and closes; `papergo-admin backup --file backup.tar.gz` includes the complete native directory with checksums. Restore it with the same backend and search path configuration. Active API/native leases reject offline snapshots.

### Copying between stores

Stop the API, leave the source index in place and choose an absent destination:

```sh
papergo-admin search copy --from sqlite --to zvec
# Set SEARCH_STORE=zvec, then start the API.
```

For the other direction, use `--from zvec --to sqlite`. `--database`, `--sqlite`, `--zvec` and `--languages` override environment/defaults; use the source's `SEARCH_LANGUAGES`. Copy preserves document stamps, typed fields, all generations of staged/published chunks, enrichments, vectors and their collection timestamps. It calls no OCR or embedding provider. The target is built beside its final path and installed only after a successful copy and close. Existing destinations are refused. The first semantic query selects its cached model's native vector space when necessary, without recomputing embeddings.

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/search | Global search; POST takes a JSON body with a field `filter` |
| GET | /v1/search/fields | The fields the caller can filter on |
| POST | /v1/search/reindex | Rebuild every document (operation) |
| GET | /v1/search/status | Store state |

The search page offers a content-type selector and matching type facets. `GET /v1/search/content-types` lists IDs, keys and names attached to readable collections, optionally narrowed by `workspace_id` and `collection_id`; pass `content_type_id` to `/v1/search` to filter. Expand Relationships on a result to load its authorized incoming/outgoing links and open a related item.
