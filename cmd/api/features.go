package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/builtin"
	mcpserver "papergo/extensions/mcp/server"
	"papergo/internal/ai"
	"papergo/internal/aiops"
	"papergo/internal/apps"
	"papergo/internal/buildinfo"
	"papergo/internal/comments"
	"papergo/internal/config"
	"papergo/internal/connections"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/database"
	"papergo/internal/dal/resultcache"
	"papergo/internal/dal/search/pgstore"
	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/doctext/pdfium"
	"papergo/internal/documentfiles"
	"papergo/internal/documentpreview"
	"papergo/internal/documentsearch"
	"papergo/internal/documenttext"
	"papergo/internal/httpapi"
	"papergo/internal/integration"
	"papergo/internal/ocr"
	"papergo/internal/search"
	"papergo/internal/solutions"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

// feature is one compiled feature: all the executable knows of it. A
// feature the configuration leaves off is not in the list, so it has no
// operations, routes, live frames, workers or maintenance. Each part is
// optional.
type feature struct {
	name string
	// register plugs the feature into the service: its operation handlers
	// and extension hooks.
	register func(*dms.Service) error
	// start runs once every feature registered, before the API serves.
	start func(context.Context) error
	// modules are its routes and live frames.
	modules []httpapi.Module
	// workers run its operation kinds, and maintenance beside them.
	workers     []worker.Definition
	maintenance []worker.Maintenance
	// close releases its engines, after the workers stopped.
	close func()
}

// features are the features a deployment runs.
type features []feature

// environment is what features are built from.
type environment struct {
	config    config.Config
	db        *database.Database
	service   *dms.Service
	store     storage.Store
	workflows *workflow.Service
	// secrets seal the configuration of integrations and connections.
	secrets *integration.Secrets
	log     *slog.Logger
}

// mcpContributions are the compiled MCP tool contributions a deployment
// may serve with MCP_EXTENSIONS.
var mcpContributions []mcp.Contribution

// enabled builds the feature list from configuration. To add a feature, add
// it here.
func enabled(ctx context.Context, e environment) (list features, err error) {
	defer func() {
		if err != nil {
			list.close()
			list = nil
		}
	}()
	documents, err := documentFeatures(e)
	if err != nil {
		return list, err
	}
	list = append(list, documents...)
	list = append(list, feature{name: "apps", modules: []httpapi.Module{apps.Module()}})
	list = append(list, connectionsFeature(e))
	if e.config.Comments {
		list = append(list, feature{name: "comments", modules: []httpapi.Module{comments.Module()}})
	}
	assistant, closeModels, err := openAI(e)
	if err != nil {
		return list, err
	}
	list = append(list, aiRetentionFeature(assistant, e.service))
	if on := aiFeature(assistant, closeModels); on != nil {
		list = append(list, *on)
	}
	searching, err := openSearch(ctx, e)
	if err != nil {
		return list, err
	}
	if searching != nil {
		list = append(list, *searching)
	}
	// Solutions say what they need of a deployment, so they come once the
	// features that provide it are known.
	list = append(list, feature{name: "solutions", modules: []httpapi.Module{solutions.Module(map[string]bool{
		"ocr": e.workflows.OCR, "ai.chat": assistant.Chat != nil, "ai.batch": assistant.Batch != nil,
		"search": searching != nil, "embeddings": searching != nil && e.config.Embeddings.Provider != "" && e.config.Embeddings.Provider != "none",
	})}})
	if e.config.MCP {
		registry, err := mcp.NewRegistry(builtin.Tools(), mcpContributions, e.config.MCPExtensions)
		if err != nil {
			return list, fmt.Errorf("MCP_EXTENSIONS: %w", err)
		}
		list = append(list, mcpFeature(mcpserver.New(mcpserver.Config{Version: buildinfo.Version, Logger: e.log, AppURL: e.config.PublicURL, RequestTimeout: e.config.RequestTimeout}, registry)))
	}
	return list, nil
}

// connectionsFeature holds the workspaces' MQTT brokers and HTTP services,
// which apps and workflows reach through named channels.
func connectionsFeature(e environment) feature {
	s := connections.New(connections.Config{DMS: e.service, Secrets: e.secrets, Workflows: e.workflows, Networks: e.config.ConnectionNetworks, Log: e.log})
	return feature{
		name:        "connections",
		register:    s.Register,
		start:       s.Start,
		modules:     []httpapi.Module{s.Module()},
		workers:     []worker.Definition{{Name: "connections", Kinds: []string{connections.KindPublish, connections.KindCall}, Concurrency: 4}},
		maintenance: []worker.Maintenance{s.Maintenance()},
		close:       s.Close,
	}
}

// documentFeatures read, preview and make document files, with the PDF and
// OCR engines they share. Every deployment runs them. Only OCR is optional.
func documentFeatures(e environment) (features, error) {
	c := e.config
	// PDFium's compiled code is kept beside the database, as derived data
	// that backups leave out and the next start rebuilds when it is gone.
	pdf := pdfium.New(pdfium.Options{CacheDir: filepath.Join(filepath.Dir(c.DatabasePath), "cache", "pdfium"), Log: e.log})
	e.log.Info("pdf reading enabled", "reader", pdf.Name())
	var reader ocr.Engine
	closeOCR := func() {}
	if c.OCRLibrary != "" {
		engine, err := openOCR(c)
		if err != nil {
			return nil, err
		}
		if engine == nil {
			e.log.Info("ocr disabled", "models", c.OCRModels)
		} else {
			reader, closeOCR = engine, func() { engine.Close() }
			e.log.Info("ocr enabled", "engine", engine.Info().Engine, "library", c.OCRLibrary, "models", c.OCRModels)
		}
	}
	e.workflows.OCR = reader != nil
	documents := documentsFeature(
		&documenttext.Reader{Storage: e.store, PDF: pdf, OCR: reader, Languages: c.SearchLanguages},
		&documentfiles.Maker{Storage: e.store, OCR: reader, MaxFileBytes: c.MaxUpload})
	documents.start = func(context.Context) error {
		warmPDF(e.log, pdf)
		return nil
	}
	documents.close = closeOCR
	return features{documents, previewsFeature(&documentpreview.Renderer{Storage: e.store, PDF: pdf})}, nil
}

// documentsFeature reads text and makes files, on one worker.
func documentsFeature(text *documenttext.Reader, files *documentfiles.Maker) feature {
	return feature{
		name: "documents",
		register: func(s *dms.Service) error {
			if err := text.Register(s); err != nil {
				return err
			}
			return files.Register(s)
		},
		modules: []httpapi.Module{text.Module(), files.Module()},
		workers: []worker.Definition{{Name: "documents", Kinds: []string{documenttext.Kind, documentfiles.KindCompose}}},
	}
}

func previewsFeature(previews *documentpreview.Renderer) feature {
	return feature{
		name:     "previews",
		register: previews.Register,
		modules:  []httpapi.Module{previews.Module()},
		workers:  []worker.Definition{{Name: "previews", Kinds: []string{documentpreview.Kind}}},
	}
}

// openAI starts the models the configuration names. closeModels releases
// them.
func openAI(e environment) (assistant *aiops.Assistant, closeModels func(), err error) {
	c := e.config
	chat, err := ai.NewChat(c.Chat, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure the chat model: %w", err)
	}
	if chat != nil {
		e.log.Info("ai steps enabled", "model", chat.Key())
	}
	batch, err := ai.NewBatch(c.Batch, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("configure the batch model: %w", err)
	}
	if batch != nil {
		e.log.Info("ai batch enabled", "model", batch.Key())
	}
	assistant = &aiops.Assistant{Storage: e.store, Chat: chat, Batch: batch, Results: resultcache.New(e.db.SQL), ResultTTL: c.ResultCacheTTL,
		Records: activitystore.New(e.db.SQL), DefaultExecution: c.AIExecution, BatchDeadline: c.BatchDeadline}
	closeModels = func() {}
	if c.Decisions.Models != "" {
		decisions, err := decision.New(c.Decisions)
		if err != nil {
			return nil, nil, fmt.Errorf("configure decisions: %w", err)
		}
		assistant.Decisions, closeModels = decisions, func() { decisions.Close() }
		e.log.Info("local decisions enabled", "model", decisions.ID())
	}
	return assistant, closeModels, nil
}

// aiFeature runs AI with the models that started, each with its own worker.
// With none the feature is off.
func aiFeature(a *aiops.Assistant, closeModels func()) *feature {
	if a.Chat == nil && a.Batch == nil && a.Decisions == nil {
		return nil
	}
	f := &feature{name: "ai", register: a.Register, modules: []httpapi.Module{a.Module()}, close: closeModels}
	if a.Chat != nil {
		f.workers = append(f.workers, worker.Definition{Name: "ai", Kinds: []string{aiops.KindComplete}, Concurrency: 2})
	}
	if a.Batch != nil {
		// Batch completions go to the provider in groups of 20, and whatever
		// waits goes once a day.
		flush, _ := worker.ParseSchedule("0 1 * * *", "UTC", worker.MissedOnce)
		f.workers = append(f.workers, worker.Definition{Name: "ai-batch", Kinds: []string{aiops.KindBatch}, Admission: worker.Admission{Policy: worker.Threshold, Threshold: 20, Batch: 20, Flush: flush}})
		f.maintenance = append(f.maintenance, worker.Maintenance{Name: "AI batches", Every: time.Minute, Run: a.Poll})
	}
	if a.Decisions != nil {
		f.workers = append(f.workers, worker.Definition{Name: "decisions", Kinds: []string{aiops.KindDecide, aiops.KindTaxonomy}})
	}
	return f
}

// aiRetentionFeature looks after what AI left in the database, with or
// without models: answers and batch records keep expiring, and work queued
// for a model that is gone ends.
func aiRetentionFeature(a *aiops.Assistant, s *dms.Service) feature {
	return feature{
		name:        "ai-retention",
		start:       func(ctx context.Context) error { return a.EndOrphans(ctx, s) },
		maintenance: []worker.Maintenance{{Name: "AI records", Every: time.Hour, Run: a.Collect}},
	}
}

// openSearch opens the search store and the embedding model. Without a
// store there is no search, and listings match search text by name.
func openSearch(ctx context.Context, e environment) (*feature, error) {
	c := e.config
	var index search.Store
	var err error
	switch c.SearchStore {
	case config.SearchSQLite:
		index, err = sqlitestore.Open(ctx, c.SearchPath, sqlitestore.Options{Languages: c.SearchLanguages})
	case config.SearchPostgres:
		index, err = pgstore.Open(ctx, e.db.SQL, pgstore.Options{Languages: c.SearchLanguages})
	}
	if err != nil {
		return nil, fmt.Errorf("open the search store: %w", err)
	}
	if index == nil {
		return nil, nil
	}
	embeddings, err := ai.NewEmbedder(c.Embeddings, nil)
	if err != nil {
		index.Close()
		return nil, fmt.Errorf("configure embeddings: %w", err)
	}
	if embeddings != nil {
		e.log.Info("semantic search enabled", "provider", c.Embeddings.Provider, "model", c.Embeddings.Model)
	}
	f := searchFeature(&documentsearch.Search{Storage: e.store, Store: index, Embeddings: embeddings})
	f.close = func() {
		if embeddings != nil {
			embeddings.Close()
		}
		index.Close()
	}
	return &f, nil
}

func searchFeature(x *documentsearch.Search) feature {
	return feature{
		name:     "search",
		register: x.Register,
		start: func(ctx context.Context) error {
			if err := x.Start(ctx); err != nil {
				return fmt.Errorf("start the search index: %w", err)
			}
			return nil
		},
		modules: []httpapi.Module{x.Module()},
		workers: []worker.Definition{
			{Name: "search", Kinds: []string{documentsearch.KindChunk, documentsearch.KindEmbed, documentsearch.KindPublish, documentsearch.KindSweep, documentsearch.KindReindex}},
			// Entries that changed are written to the index together: whatever
			// is queued, up to 200 at a time, so syncs that arrive while a
			// batch runs make the next one.
			{Name: "search-sync", Kinds: []string{documentsearch.KindSync}, Admission: worker.Admission{Policy: worker.Threshold, Threshold: 1, Batch: 200}},
		},
		maintenance: []worker.Maintenance{{Name: "embedding sweep", Every: 15 * time.Minute, Run: x.Sweep}},
	}
}

func mcpFeature(server *mcpserver.Server) feature {
	return feature{name: "mcp", modules: []httpapi.Module{server.Module()}}
}

// register makes s run the features' operations.
func (list features) register(s *dms.Service) error {
	for _, f := range list {
		if f.register == nil {
			continue
		}
		if err := f.register(s); err != nil {
			return err
		}
	}
	return nil
}

// start starts the features, once all registered.
func (list features) start(ctx context.Context) error {
	for _, f := range list {
		if f.start == nil {
			continue
		}
		if err := f.start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// modules are the feature modules whose routes the API serves.
func (list features) modules() []httpapi.Module {
	var modules []httpapi.Module
	for _, f := range list {
		modules = append(modules, f.modules...)
	}
	return modules
}

// close releases the features' engines, the last built first.
func (list features) close() {
	for _, f := range slices.Backward(list) {
		if f.close != nil {
			f.close()
		}
	}
}
