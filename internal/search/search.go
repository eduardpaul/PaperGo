// Package search is PaperGo's search layer: the store contract every search
// store implements, the query language, and the service that answers
// searches. Stores live in their own packages (sqlitestore, zvecstore) and
// cmd/api picks one. Indexing runs in workflows (search.* activities); the
// store holds derived data that a reindex rebuilds. docs/search.md is the
// user documentation.
package search

import (
	"context"
	"errors"
	"time"
)

// Surfaces of an item or folder, as in collection queries.
const (
	SurfaceHead      = "head"
	SurfacePublished = "published"
	SurfaceAuto      = "auto"
)

// Views says which surfaces a document serves. A published surface of an
// entry without a head surface (collections without publishing) serves both:
// draft readers read it as their head.
type Views uint8

const (
	ViewHead Views = 1 << iota
	ViewPublished
)

// Document is the searchable metadata of one surface of an item or folder.
type Document struct {
	ResourceID    string
	Kind          string // item or folder
	Surface       string // head or published
	Views         Views
	WorkspaceID   string
	CollectionID  string
	ParentID      string
	ContentTypeID string
	// ScopeID is the entry's permission scope; empty for entries without one.
	ScopeID    string
	RevisionID string
	// BlobID is the surface's current file, empty when none.
	BlobID string
	Name   string
	// Keywords rank between the name and the body: keywords, term labels and
	// synonyms, and high-weight fields.
	Keywords string
	// Body is the other searchable field text.
	Body string
	// Language is a Snowball language name, or empty.
	Language   string
	KeywordIDs []string
	TermIDs    []string
	// Fields are the surface's typed values, for filters.
	Fields     []Field
	CreatedBy  string
	CreatedAt  time.Time
	ModifiedBy string
	ModifiedAt time.Time
}

// Field is one typed field of a document. Values are encoded as PaperGo's
// field index encodes them, so filters compare exactly: Int for integer
// (the value), decimal (units at Scale) and boolean (0 or 1) fields, Text
// for the others (number and currency as their ordering key, dates as
// YYYY-MM-DD, datetimes as fixed-width UTC, references and terms as IDs).
type Field struct {
	Key    string
	Type   string
	Scale  int
	Values []FieldValue
}

// FieldValue is one value of a field: Int or Text, by the field's type.
type FieldValue struct {
	Text string
	Int  int64
}

// IntegerType reports whether values of a field type are kept in Int.
func IntegerType(fieldType string) bool {
	switch fieldType {
	case "integer", "decimal", "boolean":
		return true
	}
	return false
}

// FieldFilter is a filter on typed fields: a group (And, Or or Not) or a
// condition on the values of one field of one type and scale.
type FieldFilter struct {
	And   []FieldFilter
	Or    []FieldFilter
	Not   *FieldFilter
	Key   string
	Type  string
	Scale int
	// Op is eq, gt, gte, lt or lte (a value compares), in (any of Values),
	// ne (present, and no value equals), present or missing.
	Op     string
	Values []FieldValue
}

// Key identifies a document: a surface of an entry.
type Key struct{ ResourceID, Surface string }

// Access is what a caller may read: the permission scopes where they read
// drafts (head surfaces) and where they read published content. An empty
// scope ID stands for entries without a scope.
type Access struct {
	Draft, Read []string
}

// Filter narrows a search. Access and Surface always apply.
type Filter struct {
	Access Access
	// Surface is auto (drafts where the caller may, else published), head
	// (drafts only) or published.
	Surface       string
	Kinds         []string
	WorkspaceID   string
	CollectionID  string
	ContentTypeID string
	// TermIDs matches documents holding any of them; the service adds the
	// descendants of a requested term and terms merged into it.
	TermIDs      []string
	Keyword      string
	CreatedBy    string
	ModifiedBy   string
	ModifiedFrom time.Time
	ModifiedTo   time.Time
	// Fields filters on typed fields; nil filters nothing.
	Fields *FieldFilter
}

// Search modes: keyword matches words; semantic finds file content close
// in meaning to the query, by embedding vectors; hybrid fuses both lists by
// reciprocal rank.
const (
	ModeKeyword  = "keyword"
	ModeSemantic = "semantic"
	ModeHybrid   = "hybrid"
)

// How a hit was found (Hit.MatchedBy).
const (
	MatchedKeyword  = "keyword"
	MatchedSemantic = "semantic"
)

// Semantic search defaults, as PaperDotNet's.
const (
	// FusionK is the constant of reciprocal rank fusion: a document's score
	// is the sum of 1 / (FusionK + rank) over both lists.
	FusionK = 60
	// MinSimilarity is the least cosine similarity of a semantic match.
	MinSimilarity float32 = 0.3
	// CandidateLimit is how many documents each side of a hybrid search
	// contributes, and how many a semantic search finds at most.
	CandidateLimit = 200
)

// Query is one search: Text nil lists the filtered documents newest first.
// Semantic and hybrid searches need Vector, the normalized embedding of the
// query by Model; there, Text gives the keyword side and the excluded words.
type Query struct {
	Mode          string
	Text          *FullTextQuery
	Vector        []float32
	Model         string
	MinSimilarity float32
	Filter        Filter
	// Content includes file text in matching and snippets; otherwise only
	// metadata is searched. Callers must authorize document.read first.
	Content bool
	Offset  int
	Limit   int
	Facets  bool
	// VerifyFacets checks every source document against current permissions,
	// in bounded batches. Facets are omitted if any source is no longer visible.
	VerifyFacets func(context.Context, []Key) (bool, error)
}

// Hit is a matching document. Text is the text to cut a snippet from.
type Hit struct {
	ResourceID    string
	Kind          string
	Surface       string
	WorkspaceID   string
	CollectionID  string
	ContentTypeID string
	RevisionID    string
	Name          string
	// Text is where the snippet is cut from: the best matching chunk of
	// the file when the file matched, else the body.
	Text string
	// Page is the page of that chunk, 0 when the match is not on a page.
	Page int
	// MatchedBy says how the hit was found: keyword, semantic or both;
	// empty when no text was searched.
	MatchedBy  []string
	Rank       float64
	ModifiedBy string
	ModifiedAt time.Time
}

// FacetValue is a value and how many matches have it.
type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Facets count the matches per workspace, collection, content type, term
// and keyword: the most frequent values of each.
type Facets struct {
	Workspace   []FacetValue `json:"workspace"`
	Collection  []FacetValue `json:"collection"`
	ContentType []FacetValue `json:"content_type"`
	Term        []FacetValue `json:"term"`
	Keyword     []FacetValue `json:"keyword"`
}

// FacetSize is how many values each facet lists.
const FacetSize = 20

// Result is a page of hits. Total counts every match.
type Result struct {
	Hits  []Hit
	Total int
	// Estimated reports a bounded candidate set, rather than an exact count.
	Estimated bool
	Facets    *Facets
}

// MatchQuery finds the entries of one collection, or of one workspace,
// whose text matches, for collection queries and listings, which authorize,
// filter, sort and page the candidates themselves.
type MatchQuery struct {
	CollectionID string
	WorkspaceID  string
	Text         *FullTextQuery
	Content      bool
	Limit        int
}

// Matches are the entries whose head surface matches and those whose
// published surface matches; Capped says more matched than the limit.
type Matches struct {
	Head, Published []string
	Capped          bool
}

// Selection picks documents to delete.
type Selection struct {
	// ResourceIDs deletes every surface of these entries.
	ResourceIDs []string
	// Keys deletes these surfaces.
	Keys []Key
	// Before deletes documents written with a stamp below it (a reindex).
	Before int64
}

// Stats describe a store.
type Stats struct {
	Documents int64 `json:"documents"`
	// Chunks counts the published chunks of file content.
	Chunks int64 `json:"chunks"`
	Bytes  int64 `json:"bytes"`
}

// StagedChunk is a chunk of a generation, as search.enrich reads it.
type StagedChunk struct {
	Part       string
	Ordinal    int
	Page       int
	Kind       string
	Text       string
	Enrichment string
}

// Enrichment is model-written context for one chunk of a generation.
type Enrichment struct {
	Part    string
	Ordinal int
	Text    string
}

// ChunkToEmbed is a distinct chunk input that has no vector of a model.
type ChunkToEmbed struct {
	InputHash string
	// Input is the text to embed: the chunk's text and enrichment.
	Input string
}

// EmbeddingInput is the text embedded for a chunk: not its file's name, so
// a rename embeds nothing again.
func EmbeddingInput(text, enrichment string) string {
	if enrichment == "" {
		return text
	}
	return text + "\n" + enrichment
}

// HashVector is the vector of a chunk input.
type HashVector struct {
	InputHash string
	Vector    []float32
}

// EmbeddingStats describe the vectors of a model.
type EmbeddingStats struct {
	// Vectors counts the stored vectors of the model.
	Vectors int64 `json:"vectors"`
	// Pending counts published chunk inputs without a vector of the model.
	Pending int64 `json:"pending"`
	// MemoryBytes is what the model's vectors take in memory to search.
	MemoryBytes int64 `json:"memory_bytes"`
}

// Content is the published content of a file version of an item.
type Content struct {
	Generation string
	Chunks     int
}

// Store keeps search documents and answers searches. Every write is an
// idempotent upsert or delete, so a workflow step that runs again is
// harmless.
type Store interface {
	Name() string
	// UpsertDocuments writes documents, each replacing the document of its
	// key, with the stamp of the write (a reindex deletes older stamps).
	UpsertDocuments(ctx context.Context, docs []Document, stamp int64) error
	DeleteDocuments(ctx context.Context, sel Selection) error
	// SetScopes moves entries to other permission scopes (resource ID to
	// scope ID), keeping their text.
	SetScopes(ctx context.Context, scopes map[string]string) error
	// StageContent writes chunks of a file version's content under a
	// generation, replacing the chunks a part staged before. Staged chunks
	// are not searched until the generation is published.
	StageContent(ctx context.Context, itemID, blobID, generation string, chunks []Chunk) error
	// PublishContent makes a generation the searchable content of the item's
	// file version: its other generations are dropped, and the documents of
	// that version match its text.
	PublishContent(ctx context.Context, itemID, blobID, generation string) error
	// DeleteContent drops the content of the item's file versions not in
	// keep, staged or published.
	DeleteContent(ctx context.Context, itemID string, keep []string) error
	// Content reports the published content of a file version: an empty
	// generation when there is none.
	Content(ctx context.Context, itemID, blobID string) (Content, error)
	// GenerationChunks lists the chunks of a generation, staged or
	// published, by part and ordinal.
	GenerationChunks(ctx context.Context, generation string) ([]StagedChunk, error)
	// SetEnrichments sets the enrichment of chunks of a generation, which
	// changes their input hash; chunks it does not name keep theirs.
	SetEnrichments(ctx context.Context, generation string, enrichments []Enrichment) error
	// GenerationToEmbed lists the distinct chunk inputs of a generation,
	// staged or published, without a vector of the model, and how many
	// distinct inputs it has: vectors are kept per input, so the others are
	// reused.
	GenerationToEmbed(ctx context.Context, model, generation string) (todo []ChunkToEmbed, inputs int, err error)
	// ChunksToEmbed lists up to limit distinct inputs of published chunks
	// without a vector of the model.
	ChunksToEmbed(ctx context.Context, model string, limit int) ([]ChunkToEmbed, error)
	// SetVectors stores normalized vectors of a model by chunk input.
	SetVectors(ctx context.Context, model string, vectors []HashVector) error
	// CollectVectors deletes vectors that no chunk input, or a model other
	// than model, has used since before keep ago, and reports how many.
	CollectVectors(ctx context.Context, model string, now time.Time, keep time.Duration) (int, error)
	// Embeddings describes the vectors of a model.
	Embeddings(ctx context.Context, model string) (EmbeddingStats, error)
	Search(ctx context.Context, q Query) (Result, error)
	Match(ctx context.Context, q MatchQuery) (Matches, error)
	// Keys lists the documents of the entries that exist in the store.
	Keys(ctx context.Context, resourceIDs []string) ([]Key, error)
	Stats(ctx context.Context) (Stats, error)
	// Export streams the source records, including staged chunks and cached
	// vectors, for an offline copy without OCR or model calls.
	Export(ctx context.Context, yield func(Record) error) error
	// Import applies exported records idempotently, in their export order.
	Import(ctx context.Context, records []Record) error
	Close() error
}

// ErrUnavailable means the store cannot answer now, for example while it is
// rebuilt.
var ErrUnavailable = errors.New("search is unavailable")
