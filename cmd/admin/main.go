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
	"syscall"
	"time"

	"papergo/internal/backup"
	"papergo/internal/buildinfo"
	"papergo/internal/database"
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
	if len(args) > 0 && args[0] == "health" {
		flags := flag.NewFlagSet("health", flag.ContinueOnError)
		flags.SetOutput(out)
		address := flags.String("url", "http://127.0.0.1:8080/health/ready", "Readiness URL")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", *address, nil)
		if err != nil {
			return err
		}
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
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
		flags.StringVar(&path, "database", path, "SQLite database path")
		schema := flags.String("schema", "migrations", "Current Atlas schema directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("initialize accepts no positional arguments")
		}
		return database.Initialize(ctx, path, *schema)
	}
	if len(args) == 0 || args[0] != "backup" && args[0] != "restore" {
		return errors.New("usage: papergo-admin initialize|health|backup|restore; each command accepts --help")
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
	if args[0] == "restore" {
		flags.BoolVar(&in.Force, "force", false, "Replace existing data after validating the complete archive")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
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
