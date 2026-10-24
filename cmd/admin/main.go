package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"papergo/internal/backup"
	"papergo/internal/buildinfo"
	"papergo/internal/dal/database"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(out, buildinfo.String())
		return nil
	}
	if len(args) > 0 && args[0] == "search" {
		return searchCommand(ctx, args[1:], out)
	}
	if len(args) > 0 && args[0] == "workers" {
		return workersCommand(ctx, args[1:], out)
	}
	if len(args) > 0 && args[0] == "health" {
		flags := flag.NewFlagSet("health", flag.ContinueOnError)
		flags.SetOutput(out)
		address := flags.String("url", "http://127.0.0.1:8080/health/ready", "Readiness URL")
		timeout := flags.Duration("timeout", 3*time.Second, "Request timeout")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *timeout <= 0 || *timeout > time.Minute {
			return errors.New("health requires a timeout greater than zero and at most one minute, and no positional arguments")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", *address, nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: *timeout}).Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("readiness status %d", response.StatusCode)
		}
		return nil
	}
	if len(args) > 0 && args[0] == "initialize" {
		flags := flag.NewFlagSet("initialize", flag.ContinueOnError)
		flags.SetOutput(out)
		path := os.Getenv("DATABASE_PATH")
		if path == "" {
			path = "data/papergo.db"
		}
		flags.StringVar(&path, "database", path, "SQLite database path; DATABASE_URL selects PostgreSQL")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("initialize accepts no positional arguments")
		}
		if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
			return database.InitializePostgres(ctx, dsn)
		}
		return database.Initialize(ctx, path)
	}
	if len(args) == 0 || args[0] != "backup" && args[0] != "restore" {
		return errors.New("usage: papergo-admin initialize|health|backup|restore|search|workers; each command accepts --help")
	}
	if os.Getenv("DATABASE_URL") != "" {
		return errors.New(args[0] + " archives the SQLite database; DATABASE_URL selects PostgreSQL, which its own tools back up")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	defaultPath := func(key, fallback string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return fallback
	}
	var in backup.Options
	flags.StringVar(&in.DatabasePath, "database", defaultPath("DATABASE_PATH", "data/papergo.db"), "SQLite database path")
	flags.StringVar(&in.BlobPath, "blobs", defaultPath("BLOB_PATH", "data/blobs"), "Local blob directory")
	flags.StringVar(&in.ArchivePath, "file", "", "Backup archive path")
	flags.StringVar(&in.SearchStore, "search-store", defaultPath("SEARCH_STORE", "sqlite"), "Search store: sqlite or zvec (zvec backups require the API stopped)")
	searchPath := defaultPath("SEARCH_DATABASE_PATH", filepath.Join(filepath.Dir(in.DatabasePath), "search.db"))
	if in.SearchStore == "zvec" {
		searchPath = defaultPath("SEARCH_ZVEC_PATH", filepath.Join(filepath.Dir(in.DatabasePath), "search", "zvec"))
	}
	flags.StringVar(&in.SearchPath, "search", searchPath, "Search file/directory; zvec snapshots are retained, SQLite indexes are rebuilt")
	if args[0] == "restore" {
		flags.BoolVar(&in.Force, "force", false, "Replace existing data after validating the complete archive")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	searchExplicit := false
	flags.Visit(func(f *flag.Flag) { searchExplicit = searchExplicit || f.Name == "search" })
	if !searchExplicit {
		if in.SearchStore == "zvec" {
			in.SearchPath = defaultPath("SEARCH_ZVEC_PATH", filepath.Join(filepath.Dir(in.DatabasePath), "search", "zvec"))
		} else {
			in.SearchPath = defaultPath("SEARCH_DATABASE_PATH", filepath.Join(filepath.Dir(in.DatabasePath), "search.db"))
		}
	}
	if flags.NArg() != 0 || in.ArchivePath == "" {
		return errors.New("--file is required; positional arguments are not supported")
	}
	var err error
	if args[0] == "backup" {
		err = backup.Create(ctx, in)
	} else {
		err = backup.Restore(ctx, in)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s completed: %s\n", args[0], in.ArchivePath)
	return nil
}
