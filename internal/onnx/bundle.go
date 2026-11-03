package onnx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/klauspost/compress/zstd"
)

// bundleFiles are the files of an embedded runtime in the order they load.
// Each loads after the ones it imports: the Visual C++ runtime, then ONNX
// Runtime, whose imports then bind to the modules already loaded.
var bundleFiles = []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll", "msvcp140_1.dll", "onnxruntime.dll"}

// bundleLibrary is the bundle's ONNX Runtime library.
const bundleLibrary = "onnxruntime.dll"

// bundleManifest is the name of the manifest in an embedded bundle.
const bundleManifest = "manifest.json"

// bundledFile describes one file of a bundle: its name, its size and the
// SHA-256 of its contents. The bundle stores it zstd-compressed as
// name + ".zst".
type bundledFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// bundle is an embedded ONNX Runtime: its files, compressed in data, and
// its digest, the SHA-256 of its manifest, which names its directory.
type bundle struct {
	digest string
	files  []bundledFile
	data   fs.FS
}

// readBundle reads and checks a bundle's manifest, which lists exactly
// bundleFiles in load order.
func readBundle(data fs.FS) (bundle, error) {
	manifest, err := fs.ReadFile(data, bundleManifest)
	if err != nil {
		return bundle{}, err
	}
	var files []bundledFile
	if err = json.Unmarshal(manifest, &files); err != nil {
		return bundle{}, fmt.Errorf("embedded ONNX Runtime manifest: %w", err)
	}
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
		if sum, err := hex.DecodeString(file.SHA256); err != nil || len(sum) != sha256.Size || file.Size <= 0 {
			return bundle{}, fmt.Errorf("embedded ONNX Runtime manifest: invalid entry for %s", file.Name)
		}
	}
	if !slices.Equal(names, bundleFiles) {
		return bundle{}, fmt.Errorf("embedded ONNX Runtime manifest lists %v, want %v", names, bundleFiles)
	}
	sum := sha256.Sum256(manifest)
	return bundle{digest: hex.EncodeToString(sum[:]), files: files, data: data}, nil
}

// library is where the bundle's ONNX Runtime library is once installed
// in cache.
func (b bundle) library(cache string) string {
	return filepath.Join(cache, b.digest, bundleLibrary)
}

// loader opens and loads a bundle's files.
type loader interface {
	// open opens a file to read, denying writes, renames and deletion
	// until it is closed.
	open(path string) (*os.File, error)
	// load loads a library by its absolute path.
	load(path string) error
}

// install loads the bundle from cache/<digest>, extracting it there first
// when a file is missing or differs from the manifest. It checks every
// file against the manifest, then loads them in order while they cannot be
// changed, and removes everything else from cache: earlier bundles and
// abandoned extractions. It returns the ONNX Runtime library's path.
func (b bundle) install(cache string, l loader) (string, error) {
	directory := filepath.Join(cache, b.digest)
	opened, err := b.verify(directory, l)
	if err != nil {
		if err = os.RemoveAll(directory); err != nil {
			return "", fmt.Errorf("remove the damaged ONNX Runtime in %s: %w", directory, err)
		}
		if err = b.extract(cache, directory); err != nil {
			return "", fmt.Errorf("extract ONNX Runtime to %s: %w", directory, err)
		}
		if opened, err = b.verify(directory, l); err != nil {
			return "", fmt.Errorf("check the extracted ONNX Runtime: %w", err)
		}
	}
	defer func() {
		for _, file := range opened {
			file.Close()
		}
	}()
	for _, file := range b.files {
		path := filepath.Join(directory, file.Name)
		if err = l.load(path); err != nil {
			return "", fmt.Errorf("load %s: %w", path, err)
		}
	}
	b.prune(cache)
	return filepath.Join(directory, bundleLibrary), nil
}

// verify opens every file in directory, so it cannot change, and checks it
// against the manifest. The caller closes the files once they are loaded.
func (b bundle) verify(directory string, l loader) ([]*os.File, error) {
	opened := make([]*os.File, 0, len(b.files))
	for _, file := range b.files {
		f, err := l.open(filepath.Join(directory, file.Name))
		if err == nil {
			opened = append(opened, f)
			err = check(f, file)
		}
		if err != nil {
			for _, f := range opened {
				f.Close()
			}
			return nil, err
		}
	}
	return opened, nil
}

// check reads r to its end and compares its size and SHA-256 with file's.
func check(r io.Reader, file bundledFile) error {
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(r, file.Size+1))
	if err != nil {
		return err
	}
	if n != file.Size || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("%s does not match the embedded ONNX Runtime", file.Name)
	}
	return nil
}

// extract writes the bundle to a new directory beside directory and
// renames it into place, so directory never holds a partial bundle.
func (b bundle) extract(cache, directory string) error {
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(cache, ".extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		return err
	}
	defer decoder.Close()
	for _, file := range b.files {
		if err = b.write(decoder, filepath.Join(temporary, file.Name), file); err != nil {
			return err
		}
	}
	if err = os.Rename(temporary, directory); err != nil {
		// Another process extracted the same bundle first; verify checks it.
		if _, statErr := os.Stat(directory); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// write decompresses one file to path, checking it as it is written.
func (b bundle) write(decoder *zstd.Decoder, path string, file bundledFile) (err error) {
	compressed, err := b.data.Open(file.Name + ".zst")
	if err != nil {
		return err
	}
	defer compressed.Close()
	if err = decoder.Reset(compressed); err != nil {
		return err
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = check(io.TeeReader(decoder, out), file); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	// Read-only: nothing writes an extracted file again.
	return out.Chmod(0o400)
}

// prune removes everything in cache but the bundle's own directory. A
// directory that another process still has loaded stays until a later start.
func (b bundle) prune(cache string) {
	entries, err := os.ReadDir(cache)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() != b.digest {
			_ = os.RemoveAll(filepath.Join(cache, entry.Name()))
		}
	}
}
