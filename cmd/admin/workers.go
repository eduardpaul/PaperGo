package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"papergo/internal/dal/database"
	"papergo/internal/worker"
)

// workersCommand reads and controls the operation workers of running API
// processes, which apply a control within their control poll.
func workersCommand(ctx context.Context, args []string, out io.Writer) error {
	usage := errors.New("usage: papergo-admin workers status | pause|resume|drain|flush <worker>")
	if len(args) == 0 {
		return usage
	}
	flags := flag.NewFlagSet("workers "+args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "data/papergo.db"
	}
	flags.StringVar(&path, "database", path, "SQLite database path; DATABASE_URL selects PostgreSQL")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	command, names := args[0], flags.Args()
	switch {
	case command == "status" && len(names) == 0:
	case slices.Contains([]string{"pause", "resume", "drain", "flush"}, command) && len(names) == 1:
	default:
		return usage
	}
	db, closeDB, err := openDatabase(ctx, path)
	if err != nil {
		return err
	}
	defer closeDB()
	statuses, err := worker.Statuses(ctx, db.SQL)
	if err != nil {
		return err
	}
	if command == "status" {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "WORKER\tSTATE\tPENDING\tACTIVE\tREPORTED")
		for _, s := range statuses {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", s.Worker, s.State, s.Pending, s.Active, s.UpdatedAt.Format(time.RFC3339))
		}
		return w.Flush()
	}
	name := names[0]
	if !slices.ContainsFunc(statuses, func(s worker.Status) bool { return s.Worker == name }) {
		return fmt.Errorf("no worker named %s has reported; papergo-admin workers status lists them", name)
	}
	switch command {
	case "pause":
		err = worker.SetControl(ctx, db.SQL, name, worker.Paused)
	case "resume":
		err = worker.SetControl(ctx, db.SQL, name, worker.Running)
	case "drain":
		err = worker.SetControl(ctx, db.SQL, name, worker.Draining)
	case "flush":
		err = worker.RequestFlush(ctx, db.SQL, name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "worker %s: %s requested\n", name, command)
	return nil
}

// openDatabase opens the database a running API serves: PostgreSQL when
// DATABASE_URL is set, otherwise the SQLite file under a shared lease.
func openDatabase(ctx context.Context, path string) (*database.Database, func(), error) {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err := database.OpenPostgres(ctx, dsn)
		if err != nil {
			return nil, nil, err
		}
		return db, func() { db.Close() }, nil
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("--database must name the existing application database")
	}
	lease, err := database.AcquireLease(path, false)
	if err != nil {
		return nil, nil, err
	}
	db, err := database.Open(ctx, path)
	if err != nil {
		lease.Close()
		return nil, nil, err
	}
	return db, func() { db.Close(); lease.Close() }, nil
}
