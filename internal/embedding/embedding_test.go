package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/ocr/paddle"
)

func TestLazyLifecycle(t *testing.T) {
	e, err := New(Config{Models: "absent", Library: "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if e.session != nil || e.tokenizer != nil {
		t.Fatal("New loaded native state")
	}
	if got, err := e.EmbedDocuments(context.Background(), nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.EmbedQuery(ctx, "invoice"); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	if e.session != nil || e.tokenizer != nil {
		t.Fatal("empty or cancelled call loaded native state")
	}
	e.gate <- struct{}{}
	waiting, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	if _, err = e.EmbedQuery(waiting, "invoice"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting cancellation: %v", err)
	}
	<-e.gate

	if _, err = e.EmbedQuery(context.Background(), "invoice"); err == nil {
		t.Fatal("missing assets succeeded")
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedQuery(context.Background(), "invoice"); err == nil {
		t.Fatal("closed engine succeeded")
	}
}

func TestValidation(t *testing.T) {
	base := Config{Models: "m", Library: "l"}
	for _, modify := range []func(*Config){func(c *Config) { c.Model = "qwen" }, func(c *Config) { c.Threads = -1 }, func(c *Config) { c.MaxTokens = 9000 }, func(c *Config) { c.Dimensions = 1024 }, func(c *Config) { c.Model = Harrier; c.Dimensions = 256 }, func(c *Config) { c.Library = "" }} {
		cfg := base
		modify(&cfg)
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	for _, text := range []string{"", "   ", "x\x00y", string([]byte{255}), strings.Repeat("x", 128*1024+1)} {
		if err := validateText(text); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
	for _, vector := range [][]float32{nil, {0, 0}, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if err := normalize(vector); err == nil {
			t.Fatal("invalid vector accepted")
		}
	}
	vector := []float32{3, 4}
	if err := normalize(vector); err != nil || math.Abs(float64(vector[0])-.6) > 1e-6 {
		t.Fatalf("normalize %v %v", vector, err)
	}
}

func nativeConfig(t *testing.T, model string) Config {
	t.Helper()
	lib, root := os.Getenv("ONNXRUNTIME_LIB"), os.Getenv("EMBEDDING_MODELS_ROOT")
	if lib == "" || root == "" {
		if os.Getenv("PAPERGO_REQUIRE_EMBEDDING") != "" {
			t.Fatal("native embedding test paths are required")
		}
		t.Skip("set ONNXRUNTIME_LIB and EMBEDDING_MODELS_ROOT for native tests")
	}
	return Config{Model: model, Models: filepath.Join(root, model), Library: lib}
}

func TestNativeIdleUnloadAndReload(t *testing.T) {
	e, err := New(nativeConfig(t, Gemma2))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.idleTimeout = 50 * time.Millisecond
	want, err := e.EmbedQuery(context.Background(), "invoice")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		// Empty batches perform no inference and must not postpone eviction.
		if _, err := e.EmbedDocuments(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		e.gate <- struct{}{}
		unloaded := e.session == nil && e.tokenizer == nil && e.idleTimer == nil
		<-e.gate
		if unloaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle model and tokenizer were not unloaded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, err := e.EmbedQuery(context.Background(), "invoice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal("reload changed embedding dimensions")
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-5 {
			t.Fatal("reload changed the embedding")
		}
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedQuery(context.Background(), "invoice"); err == nil {
		t.Fatal("idle eviction made a closed engine reusable")
	}
}

type reference struct {
	Model, Query string
	Document     Document
	Prompts      []string
	IDs          [][]uint32
	Vectors      [][]float32
}

func TestNativeReference(t *testing.T) {
	data, err := os.ReadFile("testdata/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var refs []reference
	if err = json.Unmarshal(data, &refs); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		t.Run(ref.Model, func(t *testing.T) {
			e, err := New(nativeConfig(t, ref.Model))
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			query, err := e.EmbedQuery(context.Background(), ref.Query)
			if err != nil {
				t.Fatal(err)
			}
			docs, err := e.EmbedDocuments(context.Background(), []Document{ref.Document})
			if err != nil {
				t.Fatal(err)
			}
			for i, vector := range [][]float32{query, docs[0]} {
				encoded, err := e.tokenizer.EncodeWithOptionsErr(ref.Prompts[i], true)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(encoded.IDs, ref.IDs[i]) {
					t.Errorf("token IDs differ from Python reference: %v != %v", encoded.IDs, ref.IDs[i])
				}
				checkVector(t, vector, ref.Vectors[i])
			}
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					v, err := e.EmbedQuery(context.Background(), ref.Query)
					if err != nil {
						t.Error(err)
						return
					}
					checkVector(t, v, query)
				})
			}
			wg.Wait()
			if _, err := e.EmbedQuery(context.Background(), strings.Repeat("invoice ", 600)); err == nil {
				t.Fatal("overlong input was silently truncated")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			if _, err := e.EmbedQuery(ctx, strings.Repeat("invoice ", 300)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("inference cancellation: %v", err)
			}
			// A cancelled inference must not poison the next run.
			if _, err := e.EmbedQuery(context.Background(), ref.Query); err != nil {
				t.Fatal(err)
			}
			if ref.Model == Gemma2 {
				cfg := nativeConfig(t, Gemma2)
				cfg.Dimensions = 256
				small, err := New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer small.Close()
				v, err := small.EmbedQuery(context.Background(), ref.Query)
				if err != nil {
					t.Fatal(err)
				}
				expected := slices.Clone(query[:256])
				if err = normalize(expected); err != nil {
					t.Fatal(err)
				}
				checkVector(t, v, expected)
			}
		})
	}
}

func checkVector(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("dimensions %d, expected %d", len(got), len(want))
		return
	}
	norm := 0.0
	for i, v := range got {
		norm += float64(v) * float64(v)
		if math.Abs(float64(v-want[i])) > 1e-5 {
			t.Errorf("vector %d: got %g expected %g", i, v, want[i])
			return
		}
	}
	if math.Abs(norm-1) > 1e-5 {
		t.Errorf("vector norm squared %g", norm)
	}
}

func TestNativeRetryAndSharedOCR(t *testing.T) {
	cfg := nativeConfig(t, Gemma2)
	source := cfg.Models
	cfg.Models = t.TempDir()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err = os.Symlink(filepath.Join(source, "tokenizer.json"), filepath.Join(cfg.Models, "tokenizer.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = e.EmbedQuery(context.Background(), "invoice"); err == nil {
		t.Fatal("missing assets succeeded")
	}
	for _, name := range []string{"onnx"} {
		if err = os.Symlink(filepath.Join(source, name), filepath.Join(cfg.Models, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.EmbedQuery(context.Background(), "invoice"); err != nil {
		t.Fatal("retry", err)
	}
	ocr, err := paddle.New(paddle.Config{Library: cfg.Library, Models: "../../models/paddleocr"})
	if err != nil {
		t.Fatal(err)
	}
	defer ocr.Close()
	if _, err = ocr.Recognize(context.Background(), image.NewRGBA(image.Rect(0, 0, 256, 256))); err != nil {
		t.Fatal("shared OCR runtime", err)
	}
}
