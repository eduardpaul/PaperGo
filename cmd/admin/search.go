package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"papergo/internal/dal/database"
	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/dal/search/zvecstore"
	"papergo/internal/search"
)

func searchCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "copy" {
		return errors.New("usage: papergo-admin search copy --to sqlite|zvec (stop the API first)")
	}
	flags := flag.NewFlagSet("search copy", flag.ContinueOnError)
	flags.SetOutput(out)
	value := func(key, defaultValue string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return defaultValue
	}
	db := flags.String("database", value("DATABASE_PATH", "data/papergo.db"), "Application database; the API must be stopped")
	from := flags.String("from", value("SEARCH_STORE", "sqlite"), "Source store: sqlite or zvec")
	to := flags.String("to", "", "Destination store: sqlite or zvec")
	sqlitePath := flags.String("sqlite", value("SEARCH_DATABASE_PATH", filepath.Join(filepath.Dir(*db), "search.db")), "SQLite search file")
	zvecPath := flags.String("zvec", value("SEARCH_ZVEC_PATH", filepath.Join(filepath.Dir(*db), "search", "zvec")), "zvec search directory")
	languages := flags.String("languages", value("SEARCH_LANGUAGES", "english"), "Comma-separated Snowball languages, or none")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	explicit := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if !explicit["sqlite"] && os.Getenv("SEARCH_DATABASE_PATH") == "" {
		*sqlitePath = filepath.Join(filepath.Dir(*db), "search.db")
	}
	if !explicit["zvec"] && os.Getenv("SEARCH_ZVEC_PATH") == "" {
		*zvecPath = filepath.Join(filepath.Dir(*db), "search", "zvec")
	}
	if flags.NArg() != 0 || (*from != "sqlite" && *from != "zvec") || (*to != "sqlite" && *to != "zvec") || *from == *to {
		return errors.New("copy requires different --from and --to stores (sqlite or zvec)")
	}
	if info, err := os.Stat(*db); err != nil || !info.Mode().IsRegular() {
		return errors.New("--database must name the existing application database")
	}
	lease, err := database.AcquireLease(*db, true)
	if err != nil {
		return err
	}
	defer lease.Close()
	paths := map[string]string{"sqlite": *sqlitePath, "zvec": *zvecPath}
	if _, err = os.Stat(paths[*from]); err != nil {
		return fmt.Errorf("source search store: %w", err)
	}
	if _, err = os.Lstat(paths[*to]); !errors.Is(err, os.ErrNotExist) {
		return errors.New("destination search store already exists or cannot be inspected")
	}
	langs := []string{}
	if *languages != "none" {
		langs = strings.Split(*languages, ",")
	}
	open := func(kind, path string, noRebuild bool) (interface {
		search.Store
		Fresh() bool
	}, error) {
		if kind == "zvec" {
			return zvecstore.Open(ctx, path, zvecstore.Options{Languages: langs, NoRebuild: noRebuild})
		}
		return sqlitestore.Open(ctx, path, sqlitestore.Options{Languages: langs, NoRebuild: noRebuild})
	}
	source, err := open(*from, paths[*from], true)
	if err != nil {
		return err
	}
	defer source.Close()
	if source.Fresh() {
		return errors.New("source search store needs a reindex before it can be copied")
	}
	temporary := paths[*to] + ".copy-" + uuid.NewString()
	defer os.RemoveAll(temporary)
	defer os.Remove(temporary + ".lease")
	target, err := open(*to, temporary, false)
	if err != nil {
		return err
	}
	err = search.Copy(ctx, source, target)
	err = errors.Join(err, target.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(temporary, paths[*to]); err != nil {
		return err
	}
	fmt.Fprintf(out, "search copied from %s to %s: %s\n", *from, *to, paths[*to])
	return nil
}
