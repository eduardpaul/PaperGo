package standalone

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func testPayload(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	m := manifest{Program: "papergo-core", Files: map[string]fileInfo{}}
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(content))
		mode := uint32(0600)
		if name == m.Program {
			mode = 0700
		}
		m.Files[name] = fileInfo{SHA256: hex.EncodeToString(hash[:]), Size: int64(len(content)), Mode: mode}
	}
	entry, err := archive.Create("runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(entry).Encode(m); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestConcurrentUnpackAndReuse(t *testing.T) {
	files := map[string]string{"papergo-core": "executable", "lib/native.so": "library", "models/paddleocr/model.onnx": "model"}
	payload := testPayload(t, files)
	cache := filepath.Join(t.TempDir(), "papergo", "runtime")
	var wg sync.WaitGroup
	roots := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			root, _, err := unpack(payload, cache)
			if err != nil {
				t.Error(err)
				return
			}
			roots <- root
		})
	}
	wg.Wait()
	close(roots)
	root := ""
	for got := range roots {
		if root != "" && got != root {
			t.Fatal("simultaneous launches used different caches")
		}
		root = got
	}
	if root == "" {
		t.Fatal("no runtime was extracted")
	}
	for name, content := range files {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != content {
			t.Fatal(name, string(got), err)
		}
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary extraction directories were retained", entries, err)
	}
	if _, _, err = unpack(payload, cache); err != nil {
		t.Fatal("cached runtime could not be reused", err)
	}
	if err = os.WriteFile(filepath.Join(root, "papergo-core"), []byte("corruption"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err = unpack(payload, cache); err == nil {
		t.Fatal("corrupted cached executable was accepted")
	}
}

func TestRejectUnsafeArchivePaths(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "a/../outside", "a\\outside", "C:/outside", "."} {
		t.Run(name, func(t *testing.T) {
			payload := testPayload(t, map[string]string{"papergo-core": "app", name: "unsafe"})
			if _, _, err := unpack(payload, filepath.Join(t.TempDir(), "papergo", "runtime")); err == nil {
				t.Fatal("unsafe archive path was accepted")
			}
		})
	}
}

func TestRejectLinkedCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating Windows symlinks requires additional privileges")
	}
	cache := filepath.Join(t.TempDir(), "papergo", "runtime")
	payload := testPayload(t, map[string]string{"papergo-core": "app", "lib/native.so": "library"})
	root, _, err := unpack(payload, cache)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(root, "lib"), filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(root, "other"), filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = unpack(payload, cache); err == nil {
		t.Fatal("a symlink in the cached runtime was accepted")
	}
}

func TestBundledEnvironment(t *testing.T) {
	loader, library := "LD_LIBRARY_PATH", "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		loader, library = "DYLD_LIBRARY_PATH", "libonnxruntime.dylib"
	} else if runtime.GOOS == "windows" {
		loader, library = "PATH", "onnxruntime.dll"
	}
	t.Setenv(loader, "existing-path")
	t.Setenv("ONNXRUNTIME_LIB", "")
	t.Setenv("OCR_MODELS", "")
	t.Setenv("AI_DECISION_MODELS_PATH", "")
	t.Setenv("AI_EMBEDDINGS_MODELS_PATH", "")
	t.Setenv("AI_EMBEDDINGS_MODEL", "")
	root := t.TempDir()
	models := filepath.Join(root, "models", "paddleocr")
	for _, dir := range []string{models, filepath.Join(root, "models", "laya"),
		filepath.Join(root, "models", "embedding", "embeddinggemma-2"), filepath.Join(root, "models", "embedding", "harrier-oss-v1-0.6b")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	values := func() map[string]string {
		result := map[string]string{}
		for _, entry := range environment(root) {
			key, value, _ := strings.Cut(entry, "=")
			result[strings.ToUpper(key)] = value
		}
		return result
	}
	lib := filepath.Join(root, "lib")
	if runtime.GOOS == "windows" {
		lib = root
	}
	got := values()
	if got[loader] != lib+string(os.PathListSeparator)+"existing-path" || got["ONNXRUNTIME_LIB"] != filepath.Join(lib, library) || got["OCR_MODELS"] != models {
		t.Fatal("bundled runtime was not selected", got[loader], got["ONNXRUNTIME_LIB"], got["OCR_MODELS"])
	}
	if got["AI_DECISION_MODELS_PATH"] != filepath.Join(root, "models", "laya") || got["AI_EMBEDDINGS_MODELS_PATH"] != filepath.Join(root, "models", "embedding", "embeddinggemma-2") {
		t.Fatal("bundled decision/embedding models were not selected", got)
	}
	t.Setenv("AI_EMBEDDINGS_MODEL", "harrier-oss-v1-0.6b")
	if got = values(); got["AI_EMBEDDINGS_MODELS_PATH"] != filepath.Join(root, "models", "embedding", "harrier-oss-v1-0.6b") {
		t.Fatal("selected embedding preset was not used", got)
	}
	t.Setenv("AI_DECISION_MODELS_PATH", "custom-decisions")
	t.Setenv("AI_EMBEDDINGS_MODELS_PATH", "custom-embeddings")
	t.Setenv("ONNXRUNTIME_LIB", "custom-runtime")
	t.Setenv("OCR_MODELS", "custom-models")
	got = values()
	if got["ONNXRUNTIME_LIB"] != "custom-runtime" || got["OCR_MODELS"] != "custom-models" || got["AI_DECISION_MODELS_PATH"] != "custom-decisions" || got["AI_EMBEDDINGS_MODELS_PATH"] != "custom-embeddings" {
		t.Fatal("explicit model configuration was overwritten")
	}
}
