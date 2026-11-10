package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"papergo/internal/admin"
	"papergo/internal/auth"
	"papergo/internal/buildinfo"
	"papergo/internal/config"
	"papergo/internal/dal/database"
	"papergo/internal/dal/integrations"
	"papergo/internal/dms"
	"papergo/internal/doctext/pdfium"
	"papergo/internal/httpapi"
	"papergo/internal/identity"
	"papergo/internal/integration"
	"papergo/internal/officefiles"
	"papergo/internal/scriptrun"
	"papergo/internal/solutions"
	"papergo/internal/storage"
	"papergo/internal/textfiles"
	"papergo/internal/webdav"
	"papergo/internal/workflow"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.String())
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "admin" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := admin.Run(ctx, os.Args[2:], os.Stdout)
		stop()
		// A command asked for --help has printed it.
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "solution" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := solutions.Run(ctx, os.Args[2:], os.Stdout)
		stop()
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: papergo [--version | admin <command> | solution <command>]; the server reads its configuration from config.yaml and environment variables")
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
	// Startup may upgrade the database, which copies a SQLite file first and
	// may rebuild large tables.
	timeout := 10 * time.Minute
	startup, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	blobLease, err := database.AcquireLease(c.BlobPath, false)
	if err != nil {
		return err
	}
	defer blobLease.Close()
	var db *database.Database
	if c.DatabaseURL != "" {
		if db, err = database.OpenPostgres(startup, c.DatabaseURL); err != nil {
			return err
		}
		defer func() { db.Close() }()
		if err = upgradeSchema(startup, log, db); err != nil {
			return err
		}
	} else {
		var lease *os.File
		db, lease, err = openSQLite(startup, log, &c)
		if err != nil {
			return err
		}
		defer lease.Close()
		defer func() { db.Close() }()
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
		verifier = auth.Local{DAL: db.DAL}
	} else {
		verifier, err = auth.New(startup, c)
		if err != nil {
			return fmt.Errorf("initialize authentication: %w", err)
		}
	}
	service := dms.NewService(db.SQL)
	service.SetOperationQuotas(dms.OperationQuotas(c.OperationQuotas))
	configuredIntegrations, err := db.DAL.Integrations().Exists(startup, integrations.Filter{})
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
	workflows := &workflow.Service{DMS: service}
	// Markdown, text, CSV and Office files have their text read.
	textfiles.Register()
	officefiles.Register()
	list, err := enabled(startup, environment{config: c, db: db, service: service, store: store, workflows: workflows, log: log})
	if err != nil {
		return err
	}
	defer list.close()
	if err = list.register(service); err != nil {
		return err
	}
	if err = list.start(startup); err != nil {
		return err
	}
	if err = workflows.SyncBuiltIns(startup); err != nil {
		return fmt.Errorf("update built-in workflows: %w", err)
	}
	a := &httpapi.API{DMS: service, Workflows: workflows, Modules: list.modules(), Auth: verifier, Storage: store, Logger: log, MaxUpload: c.MaxUpload, MaxInFlight: c.MaxInFlight, RequestTimeout: c.RequestTimeout, Ready: func(ctx context.Context) error {
		if err := db.SQL.PingContext(ctx); err != nil {
			return err
		}
		return db.CheckSchema(ctx)
	}, WebDAV: webdav.New(service, verifier, store, log, c.MaxUpload), PublicURL: c.PublicURL, FileEditMax: c.FileEditMax}
	if c.ClientID != "" {
		a.BrowserSignIn = &httpapi.BrowserSignIn{Issuer: c.Issuer, ClientID: c.ClientID, Scope: c.ClientScopes}
	}
	if c.PublicURL != nil {
		a.WebDAV.PublicHost = c.PublicURL.Host
	}
	// Runs call the API in-process, so the runner registers once it exists.
	runs := &scriptrun.Runner{DMS: service, API: a, Log: log, Retention: c.RunRetention, SystemRetention: c.SystemRunRetention}
	if err = runs.Register(service); err != nil {
		return err
	}
	// Runs waiting on an operation continue as soon as it ends; waits whose
	// release a stopped process missed are reconciled at startup and by
	// maintenance.
	service.OnOperationsFinished(scriptrun.ReleaseWaits(service, log))
	if err = scriptrun.ReconcileWaits(startup, service); err != nil {
		return err
	}
	// Content uploads/downloads extend these deadlines while data keeps flowing.
	if err = a.Validate(); err != nil {
		return err
	}
	server := &http.Server{Addr: c.Address, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	runtime := workers(service, store, list, runs, log)
	if err = configure(runtime, c.Workers); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Operations resume from their persisted progress; the runtime stops
	// after the server, and the engines and database close after it.
	working, stopWorking := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { _ = runtime.Run(working); close(stopped) }()
	defer func() { stopWorking(); <-stopped }()
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

// warmPDF prepares PDFium in the background: the first start compiles it
// into the cache, so later PDFs load it from there in a fraction of a
// second, and nothing stays loaded until a PDF is read. PDFs read
// meanwhile wait for the same start. Nothing waits for it at shutdown.
func warmPDF(log *slog.Logger, pdf *pdfium.Reader) {
	go func() {
		start := time.Now()
		cache, err := pdf.Warm()
		if err != nil {
			log.Error("pdf reader cannot start", "error", err)
			return
		}
		log.Info("pdf reader ready", "took", time.Since(start).Round(time.Millisecond).String(), "cache", cache)
	}()
}

// upgradeSchema creates the database's schema or applies its pending
// migrations, before anything else uses it.
func upgradeSchema(ctx context.Context, log *slog.Logger, db *database.Database) error {
	result, err := db.Upgrade(ctx)
	if err != nil {
		return err
	}
	switch {
	case result.Created:
		log.Info("database created", "schema_version", result.To)
	case result.From < result.To:
		log.Info("database upgraded", "from", result.From, "to", result.To, "snapshot", result.Snapshot, "version", buildinfo.Version)
	}
	return nil
}

// openSQLite opens the SQLite file under its lease and brings its schema to
// this build while no other process uses the file. It returns the lease,
// which the caller closes after the database.
func openSQLite(startup context.Context, log *slog.Logger, c *config.Config) (*database.Database, *os.File, error) {
	// Every SQLite handle, including the runner, uses the same canonical name.
	resolved, err := filepath.EvalSymlinks(c.DatabasePath)
	if err == nil {
		c.DatabasePath = resolved
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("resolve database path: %w", err)
	}
	dataLease, err := database.AcquireLease(c.DatabasePath, true)
	if err != nil {
		return nil, nil, err
	}
	db, err := database.Open(startup, c.DatabasePath)
	if err != nil {
		dataLease.Close()
		return nil, nil, err
	}
	err = upgradeSchema(startup, log, db)
	// Windows cannot downgrade a byte-range lease atomically. Close all
	// SQLite handles before sharing the lease, then reopen under that lease.
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = database.ShareLease(dataLease)
	}
	if err == nil {
		db, err = database.Open(startup, c.DatabasePath)
	}
	if err != nil {
		dataLease.Close()
		return nil, nil, err
	}
	return db, dataLease, nil
}
