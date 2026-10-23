// Package documentsearch is PaperGo's search, an extension a deployment
// runs when it configures a search store. It keeps items and folders, and
// the text of their files, in the store, through workflows each collection
// may pause or replace:
//
//   - The search.index built-in queues POST /v1/workspaces/{id}/search/sync
//     for each changed entry; the search worker writes their documents in
//     batches, outside any database transaction.
//   - The search.content built-in reads a file's text and runs the content
//     steps: POST /v1/items/{id}/search/chunks stages chunks under a
//     generation, GET /v1/search/generations/{id}/chunks and PUT
//     .../enrichments read and annotate them, and POST .../embeddings and
//     .../publish embed them and make them the file's searchable content.
//
// It answers global search (GET and POST /v1/search), matches the search
// text of listings and collection queries in the store, and reports its
// status and storage. Without it, listings match search text by name.
package documentsearch

import (
	"context"
	_ "embed"
	"encoding/json"
	"sync/atomic"

	"papergo/internal/ai"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/search"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

// Operation kinds.
const (
	KindChunk   = "search.chunk"
	KindEmbed   = "search.embed"
	KindPublish = "search.publish"
	// KindSweep embeds published chunks that lack vectors; PaperGo queues
	// it itself.
	KindSweep = "search.sweep"
	// KindSync writes the search documents of entries that changed; the
	// worker runs queued syncs together.
	KindSync = "search.sync"
	// KindReindex rewrites the index from every live item and folder.
	KindReindex = "search.reindex"
)

// Roles the search built-ins fill.
const (
	// RoleIndex keeps items' and folders' names, fields, terms, keywords
	// and permissions in the search store.
	RoleIndex = "search.index"
	// RoleContent keeps the text of items' files searchable.
	RoleContent = "search.content"
)

// Search runs search on the deployment's search store.
type Search struct {
	Storage storage.Store
	Store   search.Store
	// Embeddings is nil when semantic search is off.
	Embeddings ai.Embedder

	svc *dms.Service
	// rebuilding is set while the store is rebuilt from scratch: searches
	// answer search_unavailable and listings match by name until it is done.
	rebuilding atomic.Bool
}

var (
	//go:embed builtin/search.index.ts
	indexSource string
	//go:embed builtin/search.content.ts
	contentSource string
)

// Register makes s run the search operations, match listing text in the
// store, report its storage and rebuild it after imports, and adds the
// search roles and built-in workflows, which every collection runs by
// default.
func (x *Search) Register(s *dms.Service) error {
	x.svc = s
	workflow.RegisterRole(&workflow.Role{Key: RoleIndex, Name: "Search index",
		Description: "Keeps the names, fields, terms, keywords and permissions of items and folders searchable. Turned off for a collection, searches inside it match names only, and global search keeps its entries as last indexed, still trimmed by current permissions."})
	workflow.RegisterRole(&workflow.Role{Key: RoleContent, Name: "File content in search",
		Description: "Reads, chunks and publishes the text of files so search finds it, with the page. Turned off for a collection, its files' text leaves search as their content is replaced.",
		OutputSchema: forms.MustParse(`{"type": "object", "required": ["blob_id"], "properties": {
			"blob_id": {"type": "string", "description": "The file version indexed."},
			"text_id": {"type": "string", "description": "The text read."},
			"pages": {"type": "integer"}, "language": {"type": "string"},
			"generation": {"type": "string", "description": "The content published."},
			"chunks": {"type": "integer"}}}`)})
	workflow.RegisterBuiltIn(&workflow.BuiltIn{Key: "search.index", Name: "Index for search", Role: RoleIndex, Default: true, System: true, Source: indexSource,
		Description:      "Keeps items and folders searchable: on every change, permission change and term relabel, queues the entry for the search index.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`)})
	workflow.RegisterBuiltIn(&workflow.BuiltIn{Key: "search.content", Name: "Index file content", Role: RoleContent, Default: true, System: true, Source: contentSource,
		Description: "When a PDF or an image is added, reads its text (OCR for scans and photos when configured), splits it into chunks, embeds them for semantic search when an embedding model is configured, and makes it searchable with its pages. A reindex with content runs it again for files without content.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"ocr": {"type": "string", "title": "OCR", "enum": ["auto", "always", "never"], "default": "auto"},
			"max_pages": {"type": "integer", "title": "Maximum pages", "minimum": 1, "maximum": 2000, "default": 500},
			"chunker": {"type": "string", "title": "Chunker", "enum": ["window", "pages", "paragraphs", "headings", "whole"], "default": "window"},
			"max_chars": {"type": "integer", "title": "Chunk size", "minimum": 100, "maximum": 20000, "default": 1200},
			"overlap": {"type": "integer", "title": "Overlap", "minimum": 0, "maximum": 5000, "default": 150},
			"max_chunks": {"type": "integer", "title": "Maximum chunks", "minimum": 1, "maximum": 2000, "default": 400},
			"embed": {"type": "string", "title": "Embedding", "enum": ["immediate", "later"], "default": "immediate", "description": "When an embedding model is configured: embed the chunks before publishing, or leave them to the sweep."}
		}}`)})
	for kind, h := range map[string]dms.OperationHandler{
		KindChunk:   {Scope: "document.write", Validate: x.validateChunk, Execute: x.chunk},
		KindEmbed:   {Scope: "document.write", Validate: x.validateGeneration, Execute: x.embed},
		KindPublish: {Scope: "document.write", Validate: x.validateGeneration, Execute: x.publish},
		KindSweep:   {Scope: "organization.manage", Validate: refuseSweep, Execute: x.sweep},
		KindSync:    {Scope: "item.read", Validate: validateSync, Admission: syncAdmission, ExecuteGroup: x.sync},
		KindReindex: {Scope: "organization.manage", Validate: validateReindex, Execute: x.reindex},
	} {
		if err := s.RegisterOperationHandler(kind, h); err != nil {
			return err
		}
	}
	s.SetTextMatcher(x)
	s.AddStorageReporter(x.storage)
	s.OnImport(func(ctx context.Context, t *dms.Service) error {
		// The imported text is reused, not read again.
		raw, _ := json.Marshal(reindexPayload{Content: true})
		return t.EnsureSystemOperation(ctx, KindReindex, raw)
	})
	return nil
}

// Start queues a rebuild of a store that is new or was recreated, unless
// one is queued, or resumes the rebuild a restart interrupted. Search is
// unavailable until it completes.
func (x *Search) Start(ctx context.Context) error {
	if x.Store.Fresh() {
		raw, _ := json.Marshal(reindexPayload{Rebuild: true, Content: true})
		if err := x.svc.EnsureSystemOperation(ctx, KindReindex, raw); err != nil {
			return err
		}
		x.rebuilding.Store(true)
		return nil
	}
	pending, err := x.svc.DAL.Operations().Exists(ctx, operations.Filter{Kinds: []string{KindReindex}, Statuses: []operations.Status{operations.StatusPending}, CreatedBy: dms.SystemSubject})
	if err == nil {
		x.rebuilding.Store(pending)
	}
	return err
}
