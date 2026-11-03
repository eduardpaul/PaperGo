//go:build windows

package onnx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// cacheVariable hands TestEmbeddedRuntimeProcess its cache directory.
const cacheVariable = "PAPERGO_TEST_EMBEDDED_RUNTIME_CACHE"

// TestEmbeddedRuntime installs and loads the embedded runtime in fresh
// processes, as the first and a later start do: the first extracts it and
// prunes an earlier bundle, the second verifies and reuses it.
func TestEmbeddedRuntime(t *testing.T) {
	if len(embeddedRuntime.files) != len(bundleFiles) || len(embeddedRuntime.digest) != 64 {
		t.Fatalf("embedded runtime %+v", embeddedRuntime)
	}
	cache := filepath.Join(t.TempDir(), "cache", "onnxruntime")
	earlier := filepath.Join(cache, strings.Repeat("0", 64))
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(earlier, "onnxruntime.dll"), []byte("an earlier runtime"), 0o400); err != nil {
		t.Fatal(err)
	}
	var first os.FileInfo
	for start := range 2 {
		command := exec.Command(os.Args[0], "-test.run=^TestEmbeddedRuntimeProcess$", "-test.v", "-test.count=1")
		command.Env = append(os.Environ(), cacheVariable+"="+cache)
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "--- PASS: TestEmbeddedRuntimeProcess") {
			t.Fatalf("start %d: %v\n%s", start+1, err, output)
		}
		entries, err := os.ReadDir(cache)
		if err != nil || len(entries) != 1 || entries[0].Name() != embeddedRuntime.digest {
			t.Fatalf("start %d left %v in the cache: %v", start+1, entries, err)
		}
		info, err := os.Stat(EmbeddedLibrary(cache))
		if err != nil {
			t.Fatal(err)
		}
		if start == 0 {
			first = info
		} else if !os.SameFile(first, info) {
			t.Fatal("a later start extracted the intact runtime again")
		}
	}
}

// TestEmbeddedRuntimeProcess loads the embedded runtime from the cache
// TestEmbeddedRuntime names, runs a model on it, and checks that the
// process uses the extracted Visual C++ runtime, also where Windows has its
// own copy.
func TestEmbeddedRuntimeProcess(t *testing.T) {
	cache := os.Getenv(cacheVariable)
	if cache == "" {
		t.Skip("TestEmbeddedRuntime runs this in its own process")
	}
	r, err := Load(EmbeddedLibrary(cache))
	if err != nil {
		t.Fatal(err)
	}
	probe(t, r)
	for _, name := range bundleFiles {
		var module windows.Handle
		if err = windows.GetModuleHandleEx(0, windows.StringToUTF16Ptr(name), &module); err != nil {
			t.Fatalf("%s is not loaded: %v", name, err)
		}
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		n, err := windows.GetModuleFileName(module, &buffer[0], uint32(len(buffer)))
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := os.Stat(windows.UTF16ToString(buffer[:n]))
		if err != nil {
			t.Fatal(err)
		}
		extracted, err := os.Stat(filepath.Join(cache, embeddedRuntime.digest, name))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(loaded, extracted) {
			t.Fatalf("%s is loaded from %s", name, windows.UTF16ToString(buffer[:n]))
		}
		t.Logf("%s: %s", name, windows.UTF16ToString(buffer[:n]))
	}
}
