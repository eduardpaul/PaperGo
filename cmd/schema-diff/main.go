// schema-diff generates reviewed Atlas migrations from Ent against a disposable
// SQLite database. Apply existing migrations to that dev DB before later diffs.
package main

import (
	"ariga.io/atlas/sql/migrate"
	"context"
	"entgo.io/ent/dialect/sql/schema"
	"flag"
	"log"
	"os"
	"papergo/internal/database"
)

func main() {
	name := flag.String("name", "changes", "migration name")
	path := flag.String("dev-db", "data/schema-dev.db", "disposable dev database")
	dirPath := flag.String("dir", "migrations", "migration directory")
	flag.Parse()
	ctx := context.Background()
	db, err := database.Open(ctx, *path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err = os.MkdirAll(*dirPath, 0755); err != nil {
		log.Fatal(err)
	}
	dir, err := migrate.NewLocalDir(*dirPath)
	if err != nil {
		log.Fatal(err)
	}
	if err = db.Client.Schema.NamedDiff(ctx, *name, schema.WithDir(dir), schema.WithFormatter(migrate.DefaultFormatter)); err != nil {
		log.Fatal(err)
	}
}
