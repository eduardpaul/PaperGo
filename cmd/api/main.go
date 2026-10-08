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
	"papergo/internal/config"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/webdav"
	"syscall"
	"time"
)

func main() {
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
	startup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := database.Open(startup, c.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
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
	service := dms.NewService(db.Client)
	a := &httpapi.API{DMS: service, Auth: verifier, Storage: store, Logger: log, MaxUpload: c.MaxUpload, MaxInFlight: c.MaxInFlight, RequestTimeout: c.RequestTimeout, Ready: func(ctx context.Context) error {
		if err := db.SQL.PingContext(ctx); err != nil {
			return err
		}
		return db.CheckSchema(ctx)
	}, WebDAV: webdav.New(service, verifier, store, log, c.MaxUpload)}
	// Content uploads/downloads extend these deadlines while data keeps flowing.
	server := &http.Server{Addr: c.Address, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
