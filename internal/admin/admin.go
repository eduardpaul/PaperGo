// Package admin holds PaperGo's administrative commands, which the papergo
// executable runs as papergo admin <command>.
package admin

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"papergo/internal/backup"
	"papergo/internal/config"
	"papergo/internal/dal/database"
)

const usage = "usage: papergo admin initialize|health|backup|restore|workers; each command accepts --help"

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
		c, err := configured()
		if err != nil {
			return err
		}
		if *address, err = readinessURL(c.Address); err != nil {
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
	path := flags.String("database", "", "SQLite database path (default: the configured one); DATABASE_URL selects PostgreSQL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("initialize accepts no positional arguments")
	}
	c, err := configured()
	if err != nil {
		return err
	}
	if c.DatabaseURL != "" {
		return database.InitializePostgres(ctx, c.DatabaseURL)
	}
	return database.Initialize(ctx, cmp.Or(*path, c.DatabasePath))
}

// archiveCommand backs up or restores the SQLite database, its files and
// search store: args[0] is backup or restore.
func archiveCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	var in backup.Options
	flags.StringVar(&in.DatabasePath, "database", "", "SQLite database path (default: the configured one)")
	flags.StringVar(&in.BlobPath, "blobs", "", "Local blob directory (default: the configured one)")
	flags.StringVar(&in.ArchivePath, "file", "", "Backup archive path")
	flags.StringVar(&in.SearchPath, "search", "", "SQLite search file (default: the configured one, else beside the database); restore removes it and a reindex rebuilds it")
	if args[0] == "restore" {
		flags.BoolVar(&in.Force, "force", false, "Replace existing data after validating the complete archive")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || in.ArchivePath == "" {
		return errors.New("--file is required; positional arguments are not supported")
	}
	c, err := configured()
	if err != nil {
		return err
	}
	if c.DatabaseURL != "" {
		return errors.New(args[0] + " archives the SQLite database; DATABASE_URL selects PostgreSQL, which its own tools back up")
	}
	// Unless configured, the search file follows the database chosen.
	switch {
	case in.SearchPath != "":
	case c.SearchPathSet || in.DatabasePath == "":
		in.SearchPath = c.SearchPath
	default:
		in.SearchPath = filepath.Join(filepath.Dir(in.DatabasePath), "search.db")
	}
	in.DatabasePath, in.BlobPath = cmp.Or(in.DatabasePath, c.DatabasePath), cmp.Or(in.BlobPath, c.BlobPath)
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

// configured is the server's configuration, as the server reads it: the
// admin commands act on what it names.
func configured() (config.Config, error) {
	c, err := config.Load()
	if err != nil {
		return c, fmt.Errorf("read the configuration: %w", err)
	}
	return c, nil
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
