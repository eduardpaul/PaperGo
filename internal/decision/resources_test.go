package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"papergo/internal/embedding"
	"papergo/internal/ocr/paddle"
)

func processMemory() map[string]int64 {
	out := map[string]int64{}
	raw, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(raw), "\n") {
		var key string
		var n int64
		if _, err := fmt.Sscan(line, &key, &n); err == nil && (key == "VmRSS:" || key == "VmHWM:") {
			out[strings.TrimSuffix(key, ":")+"_kib"] = n
		}
	}
	return out
}
func TestNativeResourceAcceptance(t *testing.T) {
	e := nativeEngine(t)
	ctx := context.Background()
	base := Request{State: "Invoice for coffee beans. Total EUR 21.30.", Question: "Which category best describes this document?", Options: []string{"Other document", "Invoices and payment requests"}}
	timings := []map[string]any{}
	for _, n := range []int{1, 8, 50} {
		rows := make([]Request, n)
		for i := range rows {
			rows[i] = base
		}
		start := time.Now()
		r, err := e.Decide(ctx, rows)
		if err != nil {
			t.Fatal(err)
		}
		if err = CheckResults(rows, r); err != nil {
			t.Fatal(err)
		}
		timings = append(timings, map[string]any{"rows": n, "ms": time.Since(start).Milliseconds(), "row_tokens": r[0].Tokens, "memory": processMemory()})
	}
	long := base
	last := ""
	for words := 1; words <= 1100; words++ {
		long.State = strings.Repeat("a ", words)
		enc, err := e.Encode(ctx, long)
		if err != nil {
			if len(last) == 0 {
				t.Fatal(err)
			}
			break
		}
		last = long.State
		if len(enc.IDs) > 1024 {
			t.Fatal("context overflow")
		}
	}
	if _, err := e.Decide(ctx, []Request{base, long}); err == nil {
		t.Fatal("did not reject overflow before inference")
	}
	long.State = last
	start := time.Now()
	r, err := e.Decide(ctx, []Request{long})
	if err != nil {
		t.Fatal(err)
	}
	if r[0].Tokens != 1024 {
		t.Fatal("max-length boundary", r[0].Tokens)
	}
	timings = append(timings, map[string]any{"rows": 1, "tokens": r[0].Tokens, "ms": time.Since(start).Milliseconds(), "memory": processMemory()})
	// Terminate an active native execution, then prove session recovery.
	canceled, stop := context.WithTimeout(ctx, 5*time.Millisecond)
	defer stop()
	if _, err = e.Decide(canceled, []Request{long}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("active cancellation", err)
	}
	if _, err = e.Decide(ctx, []Request{base}); err != nil {
		t.Fatal("recovery", err)
	}
	// Keep all three engines resident and exercise their common ONNX environment.
	embedRoot := os.Getenv("EMBEDDING_MODELS_ROOT")
	if embedRoot == "" {
		if os.Getenv("PAPERGO_REQUIRE_EMBEDDING") != "" {
			t.Fatal("EMBEDDING_MODELS_ROOT required")
		}
		t.Log("combined embedding workload not configured")
	} else {
		emb, err := embedding.New(embedding.Config{Models: filepath.Join(embedRoot, embedding.Gemma2), Library: os.Getenv("ONNXRUNTIME_LIB"), Threads: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer emb.Close()
		if _, err = emb.EmbedQuery(ctx, "Invoice for coffee"); err != nil {
			t.Fatal(err)
		}
		ocr, err := paddle.New(paddle.Config{Library: os.Getenv("ONNXRUNTIME_LIB"), Models: "../../models/paddleocr", Threads: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer ocr.Close()
		if _, err = ocr.Recognize(ctx, image.NewNRGBA(image.Rect(0, 0, 256, 256))); err != nil {
			t.Fatal(err)
		}
		if _, err = e.Decide(ctx, []Request{base}); err != nil {
			t.Fatal(err)
		}
		timings = append(timings, map[string]any{"combined_residency": true, "memory": processMemory()})
	}
	// Eviction releases native state and the next request verifies/reloads it.
	e.gate <- struct{}{}
	e.stopIdle()
	e.idleTimeout = 20 * time.Millisecond
	e.armIdle()
	<-e.gate
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.gate <- struct{}{}
		unloaded := e.session == nil && e.tokenizer == nil
		<-e.gate
		if unloaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle unload timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	timings = append(timings, map[string]any{"after_laya_idle": true, "memory": processMemory()})
	if _, err = e.Decide(ctx, []Request{base}); err != nil {
		t.Fatal("idle reload", err)
	}
	cpuInfo, _ := os.ReadFile("/proc/cpuinfo")
	cpu := ""
	for _, line := range strings.Split(string(cpuInfo), "\n") {
		if strings.HasPrefix(line, "model name") {
			_, cpu, _ = strings.Cut(line, ":")
			cpu = strings.TrimSpace(cpu)
			break
		}
	}
	load, _ := os.ReadFile("/proc/loadavg")
	raw, _ := json.Marshal(map[string]any{"cpu": cpu, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "load_average_end": strings.TrimSpace(string(load)), "threads": 1, "batches": "at most 8 rows / 2048 padded tokens", "measurements": timings})
	t.Log(string(raw))
	if path := os.Getenv("DECISION_RESOURCE_REPORT"); path != "" {
		if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
func TestBundleFailures(t *testing.T) {
	dir := t.TempDir()
	e, err := New(Config{Models: dir, Library: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = e.verifyBundle(); err == nil {
		t.Fatal("missing manifest")
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.verifyBundle(); err == nil {
		t.Fatal("unsupported manifest")
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), Manifest(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.verifyBundle(); err == nil {
		t.Fatal("missing artifacts")
	}
	var m struct {
		Files map[string]string `json:"files"`
	}
	_ = json.Unmarshal(Manifest(), &m)
	for name := range m.Files {
		if err = os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.verifyBundle(); err == nil {
		t.Fatal("corrupt artifacts")
	}
}
