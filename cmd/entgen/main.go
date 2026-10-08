// entgen writes to a fresh directory before replacing generated files. This
// avoids truncating files that Windows tools have memory-mapped for reading.
package main

import (
	"bytes"
	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"fmt"
	"os"
	"path/filepath"
)

func generate() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, ".ent-generate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // Only the fresh, owned directory under root.
	if err := entc.Generate("./schema", &gen.Config{Target: stage, Package: "papergo/ent", Features: []gen.Feature{gen.FeatureExecQuery, gen.FeatureVersionedMigration}}); err != nil {
		return err
	}
	return filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(root, rel)
		generated, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		existing, err := os.ReadFile(dest)
		if err == nil && bytes.Equal(generated, existing) {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		return os.Rename(path, dest)
	})
}

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
