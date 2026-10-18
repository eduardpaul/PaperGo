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

// Tag CI persists this cumulative build input on the release-migrations branch.
// Development keeps the empty placeholder and edits only the complete schema.
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
	if err := b.Validate(); err != nil {
		return b, err
	}
	return b, nil
}

func (b Bundle) Validate() error {
	if b.Format != 1 || len(b.Releases) == 0 {
		return fmt.Errorf("invalid release bundle")
	}
	for i, r := range b.Releases {
		if _, err := CompareVersions(r.Version, r.Version); err != nil {
			return err
		}
		if r.Schema == "" || r.Hash != Hash(r.Schema) {
			return fmt.Errorf("invalid release snapshot %q", r.Version)
		}
		if i == 0 && len(r.Statements) != 0 {
			return fmt.Errorf("first release must initialize from its complete schema")
		}
		if i > 0 {
			order, err := CompareVersions(b.Releases[i-1].Version, r.Version)
			if err != nil {
				return err
			}
			if order >= 0 {
				return fmt.Errorf("release history is not strictly increasing at %q", r.Version)
			}
		}
	}
	return nil
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
