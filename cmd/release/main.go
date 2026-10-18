// release prepares the cumulative embedded database history for publication.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"papergo/internal/releaseplan"
	"papergo/migrations"
)

func main() {
	version := flag.String("version", "", "release tag (vX.Y.Z or vX.Y.Z-rc.N)")
	previous := flag.String("previous", "", "previous published release bundle")
	output := flag.String("output", "migrations/release.json", "generated release bundle")
	flag.Parse()
	if _, err := migrations.CompareVersions(*version, *version); err != nil {
		log.Fatal(err)
	}
	b := migrations.Bundle{Format: 1}
	if *previous != "" {
		raw, err := os.ReadFile(*previous)
		if err != nil {
			log.Fatal(err)
		}
		b, err = migrations.DecodeBundle(raw)
		if err != nil {
			log.Fatal(err)
		}
	}
	if len(b.Releases) > 0 {
		order, err := migrations.CompareVersions(b.Releases[len(b.Releases)-1].Version, *version)
		if err != nil {
			log.Fatal(err)
		}
		if order >= 0 {
			log.Fatal("release must be newer than its published parent")
		}
	}
	r := migrations.Release{Version: *version, Hash: migrations.SchemaHash(), Schema: migrations.SchemaSQL}
	if migrations.Hash(r.Schema) != r.Hash {
		log.Fatal("atlas.sum is stale; run atlas migrate hash --dir file://migrations")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	from := r.Schema
	if len(b.Releases) > 0 {
		from = b.Releases[len(b.Releases)-1].Schema
	}
	var err error
	r.Statements, err = releaseplan.Generate(ctx, from, r.Schema)
	if err != nil {
		log.Fatal(err)
	}
	b.Releases = append(b.Releases, r)
	if err = b.Validate(); err != nil {
		log.Fatal(err)
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(*output, append(raw, '\n'), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Prepared %s: %d published snapshots, %d upgrade statements\n", *version, len(b.Releases), len(r.Statements))
}
