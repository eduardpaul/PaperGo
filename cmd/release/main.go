// release prepares the ephemeral build input and published database artifact.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"papergo/internal/releaseplan"
	"papergo/migrations"
)

func main() {
	version := flag.String("version", "", "release tag (vX.Y.Z or vX.Y.Z-rc.N)")
	previous := flag.String("previous", "", "previous published release bundle")
	output := flag.String("output", "migrations/release.json", "generated release bundle")
	flag.Parse()
	if !regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.(0|[1-9][0-9]*))?$`).MatchString(*version) {
		log.Fatal("invalid version tag")
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
	for _, r := range b.Releases {
		if r.Version == *version {
			log.Fatal("release tag already exists in the published history")
		}
	}
	r := migrations.Release{Version: *version, Hash: migrations.SchemaHash(), Schema: migrations.SchemaSQL}
	if migrations.Hash(r.Schema) != r.Hash {
		log.Fatal("atlas.sum is stale; run atlas migrate hash --dir file://migrations")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if len(b.Releases) > 0 {
		var err error
		r.Statements, err = releaseplan.Generate(ctx, b.Releases[len(b.Releases)-1].Schema, r.Schema)
		if err != nil {
			log.Fatal(err)
		}
	}
	b.Releases = append(b.Releases, r)
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err = os.WriteFile(*output, append(raw, '\n'), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Prepared %s: %d published snapshots, %d upgrade statements\n", *version, len(b.Releases), len(r.Statements))
}
