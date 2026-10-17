// Package standalone starts an application from its bundled native runtime.
package standalone

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"papergo/internal/buildinfo"
	"papergo/internal/readiness"
)

type fileInfo struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type manifest struct {
	Program string              `json:"program"`
	Files   map[string]fileInfo `json:"files"`
}

// Main is the entry point compiled with the embedded payload. Lightweight
// version and server health probes need neither extraction nor native code.
func Main(payload []byte, server bool) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.String())
		return
	}
	if server && len(os.Args) >= 2 && os.Args[1] == "--health" {
		if err := readiness.Check(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	code, err := Run(payload, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// Run verifies and unpacks the bundled runtime into the current user's cache,
// then starts the native application with the original arguments and terminal.
func Run(payload []byte, args []string) (int, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return 1, fmt.Errorf("find runtime cache: %w", err)
	}
	root, m, err := unpack(payload, filepath.Join(cache, "papergo", "runtime"))
	if err != nil {
		return 1, err
	}
	return runProcess(filepath.Join(root, m.Program), args, environment(root))
}

func unpack(payload []byte, cache string) (string, manifest, error) {
	var m manifest
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", m, fmt.Errorf("read bundled runtime: %w", err)
	}
	entries := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		if !validPath(entry.Name) || !entry.Mode().IsRegular() || entries[entry.Name] != nil {
			return "", m, fmt.Errorf("invalid bundled runtime entry %q", entry.Name)
		}
		entries[entry.Name] = entry
	}
	entry := entries["runtime.json"]
	if entry == nil {
		return "", m, errors.New("bundled runtime manifest is missing")
	}
	reader, err := entry.Open()
	if err != nil {
		return "", m, err
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&m)
	reader.Close()
	if err != nil || !validPath(m.Program) || len(m.Files) != len(entries)-1 {
		return "", m, errors.New("invalid bundled runtime manifest")
	}
	if info, ok := m.Files[m.Program]; !ok || info.Mode != 0700 {
		return "", m, errors.New("bundled application is missing")
	}
	for name, info := range m.Files {
		hash, e := hex.DecodeString(info.SHA256)
		if !validPath(name) || name == "runtime.json" || entries[name] == nil || e != nil || len(hash) != sha256.Size || info.Size < 0 || (info.Mode != 0600 && info.Mode != 0700) {
			return "", m, fmt.Errorf("invalid runtime file %q", name)
		}
	}
	// A private parent prevents other local users from replacing cached code.
	for _, dir := range []string{filepath.Dir(cache), cache} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", m, err
		}
		if err = privateDirectory(dir); err != nil {
			return "", m, err
		}
	}
	hash := sha256.Sum256(payload)
	root := filepath.Join(cache, hex.EncodeToString(hash[:]))
	if _, err = os.Lstat(root); err == nil {
		return root, m, verify(root, m)
	} else if !os.IsNotExist(err) {
		return "", m, err
	}
	staging, err := os.MkdirTemp(cache, ".unpack-")
	if err != nil {
		return "", m, err
	}
	defer os.RemoveAll(staging)
	for name, info := range m.Files {
		path := filepath.Join(staging, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", m, err
		}
		source, e := entries[name].Open()
		if e != nil {
			return "", m, e
		}
		target, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(info.Mode))
		if e != nil {
			source.Close()
			return "", m, e
		}
		_, copyErr := io.Copy(target, source)
		err = errors.Join(copyErr, target.Close(), source.Close())
		if err != nil {
			return "", m, err
		}
	}
	if err = verify(staging, m); err != nil {
		return "", m, err
	}
	if err = os.Rename(staging, root); err != nil {
		// Another simultaneous first launch may have installed the same bundle.
		if verifyErr := verify(root, m); verifyErr != nil {
			return "", m, fmt.Errorf("install runtime cache: %w", err)
		}
	}
	return root, m, nil
}

func validPath(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, "\\:\x00") && filepath.IsLocal(filepath.FromSlash(name)) && filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) == name
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("runtime cache must be a private directory: %s", path)
	}
	return nil
}

func verify(root string, m manifest) error {
	if err := privateDirectory(root); err != nil {
		return err
	}
	for name, expected := range m.Files {
		path := filepath.Join(root, filepath.FromSlash(name))
		// Refuse symlinks in every component before reading executable code.
		for parent := filepath.Dir(path); parent != root; parent = filepath.Dir(parent) {
			if err := privateDirectory(parent); err != nil {
				return err
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != expected.Size || (runtime.GOOS != "windows" && uint32(info.Mode().Perm()) != expected.Mode) {
			return fmt.Errorf("bundled runtime file changed: %s; remove %s and restart", path, root)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("bundled runtime checksum differs: %s; remove %s and restart", path, root)
		}
	}
	return nil
}

func environment(root string) []string {
	env := os.Environ()
	set := func(key, value string) {
		for i, entry := range env {
			if name, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, key) {
				env[i] = key + "=" + value
				return
			}
		}
		env = append(env, key+"="+value)
	}
	loader, library := "LD_LIBRARY_PATH", "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		loader, library = "DYLD_LIBRARY_PATH", "libonnxruntime.dylib"
	} else if runtime.GOOS == "windows" {
		loader, library = "PATH", "onnxruntime.dll"
	}
	lib := filepath.Join(root, "lib")
	if runtime.GOOS == "windows" {
		// Windows searches the executable directory before PATH. Keep DLLs
		// beside the native executable so the working directory cannot win.
		lib = root
	}
	search := lib
	if previous := os.Getenv(loader); previous != "" {
		search += string(os.PathListSeparator) + previous
	}
	set(loader, search)
	if os.Getenv("ONNXRUNTIME_LIB") == "" {
		set("ONNXRUNTIME_LIB", filepath.Join(lib, library))
	}
	if os.Getenv("OCR_MODELS") == "" {
		if _, err := os.Stat(filepath.Join(root, "models", "paddleocr")); err == nil {
			set("OCR_MODELS", filepath.Join(root, "models", "paddleocr"))
		}
	}
	if os.Getenv("AI_DECISION_MODELS_PATH") == "" {
		if models := filepath.Join(root, "models", "laya"); isDirectory(models) {
			set("AI_DECISION_MODELS_PATH", models)
		}
	}
	if os.Getenv("AI_EMBEDDINGS_MODELS_PATH") == "" {
		model := os.Getenv("AI_EMBEDDINGS_MODEL")
		if model == "" {
			model = "embeddinggemma-2"
		}
		// Only catalog identifiers may select a bundled path. The application
		// validates provider/model combinations and explicit operator paths.
		if model == "embeddinggemma-2" || model == "harrier-oss-v1-0.6b" {
			if models := filepath.Join(root, "models", "embedding", model); isDirectory(models) {
				set("AI_EMBEDDINGS_MODELS_PATH", models)
			}
		}
	}
	return env
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
