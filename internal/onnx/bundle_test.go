package onnx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/klauspost/compress/zstd"
)

// testBundle compresses made-up contents for every bundle file, as
// internal/onnx/stage does.
func testBundle(t *testing.T) (bundle, map[string][]byte) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	data, contents := fstest.MapFS{}, map[string][]byte{}
	var manifest []bundledFile
	for i, name := range bundleFiles {
		content := []byte(strings.Repeat(name, 100*(i+1)))
		sum := sha256.Sum256(content)
		contents[name] = content
		manifest = append(manifest, bundledFile{Name: name, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])})
		data[name+".zst"] = &fstest.MapFile{Data: encoder.EncodeAll(content, nil)}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data[bundleManifest] = &fstest.MapFile{Data: encoded}
	b, err := readBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	return b, contents
}

// recorder opens files as the Windows loader would, without the locks,
// and records what it loads.
type recorder struct {
	loaded []string
	fail   string
}

func (r *recorder) open(path string) (*os.File, error) { return os.Open(path) }

func (r *recorder) load(path string) error {
	if filepath.Base(path) == r.fail {
		return errors.New("not a valid image")
	}
	r.loaded = append(r.loaded, path)
	return nil
}

func installed(t *testing.T, b bundle, cache string, contents map[string][]byte) {
	t.Helper()
	for name, content := range contents {
		path := filepath.Join(cache, b.digest, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(content) {
			t.Fatalf("%s: %v, contents differ", path, err)
		}
		// Windows reports a read-only file as 0444.
		if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o222 != 0 {
			t.Fatalf("%s is not read-only: %v", path, err)
		}
	}
}

func TestBundleInstall(t *testing.T) {
	b, contents := testBundle(t)
	cache := filepath.Join(t.TempDir(), "cache", "onnxruntime")
	l := &recorder{}
	library, err := b.install(cache, l)
	if err != nil {
		t.Fatal(err)
	}
	if library != b.library(cache) || library != filepath.Join(cache, b.digest, "onnxruntime.dll") {
		t.Fatalf("library %s", library)
	}
	installed(t, b, cache, contents)
	// The Visual C++ runtime loads first, in dependency order, by absolute path.
	var want []string
	for _, name := range bundleFiles {
		want = append(want, filepath.Join(cache, b.digest, name))
	}
	if !slices.Equal(l.loaded, want) {
		t.Fatalf("loaded %v, want %v", l.loaded, want)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("cache holds %d entries", len(entries))
	}

	// A second start finds the files intact and keeps them.
	before, err := os.Stat(library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.install(cache, &recorder{}); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(library); err != nil || !os.SameFile(before, after) {
		t.Fatalf("an intact bundle was extracted again: %v", err)
	}
}

func TestBundleReplacesDamagedFiles(t *testing.T) {
	b, contents := testBundle(t)
	for name, damage := range map[string]func(string) error{
		"changed":   func(path string) error { return replace(path, []byte("a planted library")) },
		"truncated": func(path string) error { return replace(path, contents["msvcp140.dll"][:10]) },
		"extended":  func(path string) error { return replace(path, append(slices.Clone(contents["msvcp140.dll"]), 0)) },
		"missing":   os.Remove,
	} {
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			if _, err := b.install(cache, &recorder{}); err != nil {
				t.Fatal(err)
			}
			if err := damage(filepath.Join(cache, b.digest, "msvcp140.dll")); err != nil {
				t.Fatal(err)
			}
			l := &recorder{}
			if _, err := b.install(cache, l); err != nil {
				t.Fatal(err)
			}
			installed(t, b, cache, contents)
			if len(l.loaded) != len(bundleFiles) {
				t.Fatalf("loaded %v", l.loaded)
			}
		})
	}
}

func replace(path string, content []byte) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

func TestBundlePrunesOtherEntries(t *testing.T) {
	b, _ := testBundle(t)
	cache := t.TempDir()
	stale := []string{strings.Repeat("0", 64), ".extract-123"}
	for _, name := range stale {
		if err := os.MkdirAll(filepath.Join(cache, name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, name, "onnxruntime.dll"), []byte("old"), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.install(cache, &recorder{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 || entries[0].Name() != b.digest {
		t.Fatalf("cache holds %v: %v", entries, err)
	}
}

func TestBundleFailures(t *testing.T) {
	b, _ := testBundle(t)
	cache := t.TempDir()
	// A library that fails to load fails the install, and is not pruned.
	if _, err := b.install(cache, &recorder{fail: "msvcp140.dll"}); err == nil || !strings.Contains(err.Error(), "msvcp140.dll") {
		t.Fatalf("load failure: %v", err)
	}
	// Embedded data that does not match its manifest leaves nothing behind.
	data := fstest.MapFS{}
	for name, file := range b.data.(fstest.MapFS) {
		data[name] = file
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	data["onnxruntime.dll.zst"] = &fstest.MapFile{Data: encoder.EncodeAll([]byte("something else"), nil)}
	encoder.Close()
	corrupt := bundle{digest: b.digest, files: b.files, data: data}
	cache = t.TempDir()
	if _, err := corrupt.install(cache, &recorder{}); err == nil {
		t.Fatal("a bundle that does not match its manifest was installed")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("a failed extraction left %v", entries)
	}
}

func TestReadBundleChecksTheManifest(t *testing.T) {
	b, _ := testBundle(t)
	manifest := func(files []bundledFile) fstest.MapFS {
		encoded, err := json.Marshal(files)
		if err != nil {
			t.Fatal(err)
		}
		return fstest.MapFS{bundleManifest: &fstest.MapFile{Data: encoded}}
	}
	reordered := slices.Clone(b.files)
	reordered[0], reordered[2] = reordered[2], reordered[0]
	badHash := slices.Clone(b.files)
	badHash[1].SHA256 = "abc"
	empty := slices.Clone(b.files)
	empty[4].Size = 0
	for name, files := range map[string][]bundledFile{
		"reordered": reordered, "missing": b.files[:4], "extra": append(slices.Clone(b.files), b.files[0]),
		"bad hash": badHash, "empty": empty,
	} {
		if _, err := readBundle(manifest(files)); err == nil {
			t.Errorf("%s manifest accepted", name)
		}
	}
	if _, err := readBundle(fstest.MapFS{bundleManifest: &fstest.MapFile{Data: []byte("{")}}); err == nil {
		t.Error("invalid JSON accepted")
	}
}
