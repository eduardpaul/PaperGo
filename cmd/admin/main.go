package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"papergo/internal/backup"
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
	if len(args) == 0 || args[0] != "backup" && args[0] != "restore" {
		return errors.New("usage: papergo-admin backup|restore --file archive.tar.gz [--database path] [--blobs path] [--force (restore)]")
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
