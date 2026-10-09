// Command server runs the REST API for the SDK end-to-end tests on a fresh,
// migrated SQLite database with development authentication. It prints its
// base URL on the first line of stdout and serves until it is interrupted.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"papergo/internal/auth"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"path/filepath"
	"syscall"
	"time"

	"ariga.io/atlas/sql/migrate"
)

func main() {
	migrations := flag.String("migrations", "migrations", "directory of the versioned migrations")
	token := flag.String("token", "", "development bearer token")
	flag.Parse()
	if err := run(*migrations, *token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(migrations, token string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, err := os.MkdirTemp("", "papergo-sdk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := database.Open(ctx, filepath.Join(dir, "api.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	local, err := migrate.NewLocalDir(migrations)
	if err != nil {
		return err
	}
	files, err := local.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, err = db.SQL.ExecContext(ctx, string(f.Bytes())); err != nil {
			return fmt.Errorf("apply %s: %w", f.Name(), err)
		}
	}
	store, err := storage.NewLocal(filepath.Join(dir, "blobs"))
	if err != nil {
		return err
	}
	a := &httpapi.API{DMS: dms.NewService(db.Client), Auth: auth.Development{Token: token, Subject: "sdk"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), MaxUpload: 1 << 20, MaxInFlight: 32, RequestTimeout: 30 * time.Second, Ready: db.SQL.PingContext}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	fmt.Printf("http://%s\n", listener.Addr())
	select {
	case err = <-result:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdown); errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
