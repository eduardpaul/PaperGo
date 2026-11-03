// Package admin holds PaperGo's administrative commands, which the papergo
// executable runs as papergo admin <command>.
package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"papergo/internal/backup"
	"papergo/internal/config"
	"papergo/internal/dal/database"
)

const usage = "usage: papergo admin initialize|health|backup|restore|search|workers; each command accepts --help"

// Run runs an administrative command: papergo admin <command>. A command
// asked for --help prints it and returns flag.ErrHelp.
func Run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(out, usage)
		return flag.ErrHelp
	case "health":
		return healthCommand(ctx, args[1:], out)
	case "initialize":
		return initializeCommand(ctx, args[1:], out)
	case "backup", "restore":
		return archiveCommand(ctx, args, out)
	case "search":
		return searchCommand(ctx, args[1:], out)
	case "workers":
		return workersCommand(ctx, args[1:], out)
	}
	return errors.New(usage)
}

// healthCommand probes a running server's readiness check.
func healthCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(out)
	address := flags.String("url", "", "Readiness URL; by default the configured listen address's /health/ready")
	timeout := flags.Duration("timeout", 3*time.Second, "Request timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 || *timeout > time.Minute {
		return errors.New("health requires a timeout greater than zero and at most one minute, and no positional arguments")
	}
	if *address == "" {
		listen, err := config.ListenAddress()
		if err != nil {
			return err
		}
		if *address, err = readinessURL(listen); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *address, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: *timeout}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness status %d", response.StatusCode)
	}
	return nil
}

// initializeCommand creates or upgrades a database without starting the
// server.
func initializeCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("initialize", flag.ContinueOnError)
	flags.SetOutput(out)
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "data/papergo.db"
	}
	flags.StringVar(&path, "database", path, "SQLite database path; DATABASE_URL selects PostgreSQL")
	if err := flags.Parse(args); err != nil {
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

// archiveCommand backs up or restores the SQLite database, its files and
// search store: args[0] is backup or restore.
func archiveCommand(ctx context.Context, args []string, out io.Writer) error {
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

// readinessURL is the readiness check of a server listening on listen. A
// server listening on every address answers on loopback.
func readinessURL(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", listen, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/health/ready"}).String(), nil
}
