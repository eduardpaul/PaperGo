package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"papergo/internal/auth"
	"papergo/internal/buildinfo"
	"papergo/internal/config"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/integration"
	"papergo/internal/ocr/paddle"
	"papergo/internal/runner"
	"papergo/internal/storage"
	"papergo/internal/webdav"
	"papergo/internal/workflow"
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
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: papergo [--version]; configure the server through environment variables")
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
	}
	if err != nil && !(migrations.Published() && os.IsNotExist(err)) {
		return fmt.Errorf("resolve database path (apply the Atlas schema before startup): %w", err)
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
	}
	if err = db.CheckSchema(startup); err != nil {
		return err
	}
	store, err := storage.NewLocal(c.BlobPath)
	if err != nil {
		return err
	}
	verifier, err := auth.New(startup, c)
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
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
	if err = service.Directory().Initialize(startup, c.AdminSubject, c.AdminGroup); err != nil {
		return fmt.Errorf("initialize organization directory: %w", err)
	}
	if err = service.RegisterPortability(store); err != nil {
		return err
	}
	tools := workflow.Tools{Storage: store, MaxFileBytes: c.MaxUpload}
	if c.OCRLibrary != "" {
		engine, e := paddle.New(paddle.Config{Library: c.OCRLibrary, Models: c.OCRModels, Threads: c.OCRThreads})
		if e != nil {
			return fmt.Errorf("configure OCR: %w", e)
		}
		defer engine.Close()
		tools.OCR = engine
		log.Info("ocr enabled", "engine", engine.Info().Engine, "library", c.OCRLibrary, "models", c.OCRModels)
	}
	workflows := &workflow.Service{DMS: service, Tools: tools}
	service.OnIntegrationCreated(workflows.EnsureIntegrationWorkflow)
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
