package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"papergo/internal/ai"
	"papergo/internal/auth"
	"papergo/internal/buildinfo"
	"papergo/internal/config"
	"papergo/internal/database"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/doctext/pdfium"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/integration"
	"papergo/internal/readiness"
	"papergo/internal/resultcache"
	"papergo/internal/runner"
	"papergo/internal/search"
	"papergo/internal/search/sqlitestore"
	"papergo/internal/search/zvecstore"
	"papergo/internal/storage"
	"papergo/internal/webdav"
	"papergo/internal/workflow"
	"papergo/internal/workflowai"
	"papergo/migrations"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.String())
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "--health" {
		if err := readiness.Check(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: papergo [--version|--health [url]]; configuration is read from config.yaml and environment variables")
		os.Exit(1)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	timeout := 20 * time.Second
	if migrations.Published() {
		timeout = database.ReleaseStartupTimeout
	}
	startup, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Every SQLite handle, including the runner, uses the same canonical name.
	resolved, err := filepath.EvalSymlinks(c.DatabasePath)
	if err == nil {
		c.DatabasePath = resolved
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resolve database path: %w", err)
	}
	dataLease, err := database.AcquireLease(c.DatabasePath, migrations.Published())
	if err != nil {
		return err
	}
	defer dataLease.Close()
	blobLease, err := database.AcquireLease(c.BlobPath, false)
	if err != nil {
		return err
	}
	defer blobLease.Close()
	db, err := database.Open(startup, c.DatabasePath)
	if err != nil {
		return err
	}
	defer func() { db.Close() }()
	if migrations.Published() {
		bundle, e := migrations.EmbeddedBundle()
		if e != nil {
			return e
		}
		snapshot, e := db.ApplyRelease(startup, c.DatabasePath, bundle)
		if e != nil {
			return e
		}
		if snapshot != "" {
			log.Info("database upgraded", "snapshot", snapshot, "version", buildinfo.Version)
		}
		// Windows cannot downgrade a byte-range lease atomically. Close all
		// SQLite handles before sharing the lease, then reopen under that lease.
		if e = db.Close(); e != nil {
			return e
		}
		if e = database.ShareLease(dataLease); e != nil {
			return e
		}
		reopened, e := database.Open(startup, c.DatabasePath)
		if e != nil {
			return e
		}
		db = reopened
	} else if err = db.EnsureSchema(startup); err != nil {
		return err
	}
	if err = db.CheckSchema(startup); err != nil {
		return err
	}
	if c.AuthMode == "local" && c.AdminPassword == "" {
		ready, e := identity.HasAdministrator(startup, db.SQL)
		if e != nil {
			return e
		}
		if !ready {
			// Human input has no startup deadline. Give the remaining setup a
			// fresh context once the password has been entered.
			cancel()
			c.AdminPassword, err = config.PromptAdminPassword(os.Stdin, os.Stderr)
			if err != nil {
				return err
			}
			startup, cancel = context.WithTimeout(context.Background(), timeout)
			defer cancel()
		}
	}
	store, err := storage.NewLocal(c.BlobPath)
	if err != nil {
		return err
	}
	var verifier auth.Authenticator
	if c.AuthMode == "local" {
		verifier = auth.Local{DB: db.SQL}
	} else {
		verifier, err = auth.New(startup, c)
		if err != nil {
			return fmt.Errorf("initialize authentication: %w", err)
		}
	}
	service := dms.NewService(db.SQL)
	configuredIntegrations, err := service.Client.ListIntegration.Query().Exist(startup)
	if err != nil {
		return err
	}
	secrets, err := integration.LoadSecrets(c.DatabasePath+".integrations.key", !configuredIntegrations)
	if err != nil {
		return fmt.Errorf("configure list integration encryption: %w", err)
	}
	service.ConfigureIntegrations(secrets)

	var index interface {
		search.Store
		Fresh() bool
	}
	if c.SearchStore == "zvec" {
		index, err = zvecstore.Open(startup, c.ZvecPath, zvecstore.Options{Languages: c.SearchLanguages, KeywordCandidates: c.ZvecKeywordCandidates})
	} else {
		index, err = sqlitestore.Open(startup, c.SearchPath, sqlitestore.Options{Languages: c.SearchLanguages})
	}
	if err != nil {
		return fmt.Errorf("open the search store: %w", err)
	}
	defer index.Close()
	service.SetSearch(index)
	embeddings, err := ai.NewEmbedder(c.Embeddings, nil)
	if err != nil {
		return fmt.Errorf("configure embeddings: %w", err)
	}
	if embeddings != nil {
		defer embeddings.Close()
		service.SetEmbeddings(embeddings)
		log.Info("semantic search enabled", "provider", c.Embeddings.Provider, "model", c.Embeddings.Model)
	}
	if index.Fresh() {
		// A new or recreated store: rebuild it from the database.
		err = service.RebuildSearch(startup)
	} else {
		err = service.ResumeSearchRebuild(startup)
	}
	if err != nil {
		return fmt.Errorf("start the search index: %w", err)
	}
	if err = service.Directory().Initialize(startup, c.AdminSubject, c.AdminGroup); err != nil {
		return fmt.Errorf("initialize organization directory: %w", err)
	}
	if c.AuthMode == "local" {
		hash := ""
		if c.AdminPassword != "" {
			hash, err = auth.HashPassword(c.AdminPassword)
			if err != nil {
				return err
			}
		}
		if err = service.Directory().SetInitialPasswordHash(startup, c.AdminSubject, hash); err != nil {
			return fmt.Errorf("initialize local administrator: %w", err)
		}
	}
	if err = service.RegisterPortability(store); err != nil {
		return err
	}
	chat, err := ai.NewChat(c.Chat, nil)
	if err != nil {
		return fmt.Errorf("configure the chat model: %w", err)
	}
	if chat != nil {
		log.Info("ai steps enabled", "model", chat.Key())
	}
	batch, err := ai.NewBatch(c.Batch, nil)
	if err != nil {
		return fmt.Errorf("configure the batch model: %w", err)
	}
	if batch != nil {
		log.Info("ai batch enabled", "model", batch.Key())
	}
	var decisions *decision.Engine
	if c.Decisions.Models != "" {
		decisions, err = decision.New(c.Decisions)
		if err != nil {
			return fmt.Errorf("configure decisions: %w", err)
		}
		defer decisions.Close()
		log.Info("local decisions enabled", "model", decisions.ID())
	}
	tools := workflow.Tools{Storage: store, MaxFileBytes: c.MaxUpload, Search: index, Embeddings: embeddings, Results: resultcache.New(db.SQL), ResultTTL: c.RunRetention, Languages: c.SearchLanguages}
	workflowai.Configure(&tools, workflowai.Runtime{Chat: chat, Batch: batch, DefaultExecution: c.AIExecution, BatchDeadline: c.BatchDeadline})
	if decisions != nil {
		tools.Decisions = decisions
	}
	tools.PDF = pdfium.New()
	log.Info("pdf reading enabled", "reader", tools.PDF.Name())
	if c.OCRLibrary != "" {
		engine, e := openOCR(c)
		if e != nil {
			return e
		}
		if engine == nil {
			log.Info("ocr disabled", "models", c.OCRModels)
		} else {
			defer engine.Close()
			tools.OCR = engine
			log.Info("ocr enabled", "engine", engine.Info().Engine, "library", c.OCRLibrary, "models", c.OCRModels)
		}
	}
	workflows := &workflow.Service{DMS: service, Tools: tools}
	service.OnIntegrationCreated(workflows.EnsureIntegrationWorkflow)
	if c.BatchWorkspace != "" && batch != nil {
		subject := c.AdminSubject
		if subject == "" {
			subject = c.DevSubject
		}
		if err = workflowai.EnsureCoordinator(startup, workflows, subject, c.BatchWorkspace); err != nil {
			return fmt.Errorf("enable the AI batch coordinator: %w", err)
		}
		log.Info("ai batch coordinator enabled", "workspace", c.BatchWorkspace, "queue", "shared")
	}
	run, err := runner.New(runner.Config{DatabasePath: c.DatabasePath, NodeID: c.NodeID, Retention: c.RunRetention, Logger: log}, db.SQL, service, workflows)
	if err != nil {
		return err
	}
	if err = run.Launch(startup); err != nil {
		return err
	}
	// The runner stops after the server, so requests in flight can still start runs.
	defer func() {
		if err := run.Shutdown(10 * time.Second); err != nil {
			log.Error("runner shutdown", "error", err)
		}
	}()
	a := &httpapi.API{DMS: service, Workflows: workflows, Runner: run, Auth: verifier, Storage: store, Logger: log, MaxUpload: c.MaxUpload, MaxInFlight: c.MaxInFlight, RequestTimeout: c.RequestTimeout, Ready: func(ctx context.Context) error {
		if err := db.SQL.PingContext(ctx); err != nil {
			return err
		}
		if err := run.Ready(ctx); err != nil {
			return err
		}
		return db.CheckSchema(ctx)
	}, WebDAV: webdav.New(service, verifier, store, log, c.MaxUpload), PublicURL: c.PublicURL}
	if c.ClientID != "" {
		a.BrowserSignIn = &httpapi.BrowserSignIn{Issuer: c.Issuer, ClientID: c.ClientID, Scope: c.ClientScopes}
	}
	if c.PublicURL != nil {
		a.WebDAV.PublicHost = c.PublicURL.Host
	}
	// Content uploads/downloads extend these deadlines while data keeps flowing.
	server := &http.Server{Addr: c.Address, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Background operations (index builds) resume from their persisted progress.
	go service.RunOperations(ctx, log)
	go service.RunBlobCleanup(ctx, store, log)
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	log.Info("api listening", "address", c.Address, "environment", c.Env)
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	if err = server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	return nil
}
