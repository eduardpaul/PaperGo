package migrations

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const SchemaFile = "20261007000100_schema.sql"

// Release history is a build input generated in tag CI, never development SQL.
//
//go:embed release.json
var releaseJSON []byte

//go:embed 20261007000100_schema.sql
var SchemaSQL string

type Release struct {
	Version    string   `json:"version"`
	Hash       string   `json:"schema_hash"`
	Schema     string   `json:"schema"`
	Statements []string `json:"upgrade"`
}
type Bundle struct {
	Format   int       `json:"format"`
	Releases []Release `json:"releases"`
}

func Hash(sql string) string {
	sum := sha256.Sum256([]byte(SchemaFile + sql))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func DecodeBundle(data []byte) (Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.Format != 1 || len(b.Releases) == 0 {
		return b, fmt.Errorf("invalid release bundle")
	}
	seen := map[string]bool{}
	for _, r := range b.Releases {
		if r.Version == "" || seen[r.Version] || r.Hash != Hash(r.Schema) {
			return b, fmt.Errorf("invalid release snapshot %q", r.Version)
		}
		seen[r.Version] = true
	}
	return b, nil
}

func Published() bool { return !bytes.Equal(bytes.TrimSpace(releaseJSON), []byte("{}")) }
func EmbeddedBundle() (Bundle, error) {
	b, err := DecodeBundle(releaseJSON)
	if err != nil {
		return b, err
	}
	if b.Releases[len(b.Releases)-1].Hash != SchemaHash() {
		return b, fmt.Errorf("release bundle does not match embedded schema")
	}
	return b, nil
}
