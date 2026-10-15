// Package embedding runs local text embeddings in the same ONNX Runtime as OCR.
// Constructing an Engine does no I/O. Libraries, tokenizer and weights load on
// the first nonempty embedding request; inference never downloads anything.
package embedding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tokenizers "github.com/daulet/tokenizers"
	"papergo/internal/nativeheap"
	"papergo/internal/onnx"
)

const (
	Gemma2  = "embeddinggemma-2"
	Harrier = "harrier-oss-v1-0.6b"
)

// Config selects a pinned Q4 text export installed by fetch-embedding-models.py.
// Library is the same ONNXRUNTIME_LIB used for OCR. The tokenizer is linked
// into the binary; inference never downloads libraries or models.
type Config struct {
	Model, Models, Library         string
	Threads, Dimensions, MaxTokens int
}

// ErrTooManyTokens is returned for an input longer than the token limit.
var ErrTooManyTokens = errors.New("too many tokens")

// Document is one passage with an optional title.
type Document struct{ Title, Text string }

// Engine owns a tokenizer and a session. Its gate serializes loading, inference
// and Close and allows a waiting request to be cancelled.
type Engine struct {
	cfg         Config
	gate        chan struct{}
	tokenizer   *tokenizers.Tokenizer
	session     *onnx.Session
	closed      bool
	idleTimer   *time.Timer
	idleTimeout time.Duration
}

func New(cfg Config) (*Engine, error) {
	if cfg.Model == "" {
		cfg.Model = Gemma2
	}
	if cfg.Threads == 0 {
		cfg.Threads = 1
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 512
	}
	limit, dim := 8192, 768
	if cfg.Model == Harrier {
		limit, dim = 32768, 1024
	} else if cfg.Model != Gemma2 {
		return nil, fmt.Errorf("unknown embedding model %q", cfg.Model)
	}
	if cfg.Dimensions == 0 {
		cfg.Dimensions = dim
	}
	if cfg.Model == Gemma2 && !slices.Contains([]int{128, 256, 512, 768}, cfg.Dimensions) || cfg.Model == Harrier && cfg.Dimensions != 1024 {
		return nil, errors.New("invalid embedding dimensions for the model")
	}
	if cfg.Threads < 1 || cfg.Threads > 32 {
		return nil, errors.New("embedding threads must be between 1 and 32")
	}
	if cfg.MaxTokens < 2 || cfg.MaxTokens > limit {
		return nil, fmt.Errorf("embedding token limit must be between 2 and %d", limit)
	}
	for _, p := range []string{cfg.Models, cfg.Library} {
		if p == "" || strings.ContainsRune(p, 0) {
			return nil, errors.New("embedding model and library paths are required and cannot contain NUL")
		}
	}
	return &Engine{cfg: cfg, gate: make(chan struct{}, 1), idleTimeout: 5 * time.Minute}, nil
}

// ID identifies the vector space, including quantization, dimension and input
// limit; store vectors under this key rather than mixing model outputs.
func (e *Engine) ID() string {
	rev := "daa72c51243991dfcaf9f9137d2c573d8f7790c0"
	if e.cfg.Model == Harrier {
		rev = "e4daffa011e666dcd2ff2a3c6c05084090ac314d"
	}
	return fmt.Sprintf("%s:%s:q4:d%d:t%d", e.cfg.Model, rev, e.cfg.Dimensions, e.cfg.MaxTokens)
}

func (e *Engine) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close releases native model and tokenizer memory and prevents further use.
func (e *Engine) Close() error {
	e.gate <- struct{}{}
	defer func() { <-e.gate }()
	e.closed = true
	e.stopIdle()
	err := e.unload()
	nativeheap.Release()
	return err
}

// unload runs with the gate held; idle eviction keeps the engine reusable.
func (e *Engine) unload() error {
	if e.session != nil {
		e.session.Close()
		e.session = nil
	}
	if e.tokenizer != nil {
		err := e.tokenizer.Close()
		e.tokenizer = nil
		return err
	}
	return nil
}

func (e *Engine) stopIdle() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
}

func (e *Engine) armIdle() {
	if e.session != nil && !e.closed {
		var timer *time.Timer
		timer = time.AfterFunc(e.idleTimeout, func() {
			e.gate <- struct{}{}
			defer func() { <-e.gate }()
			if e.idleTimer == timer {
				e.idleTimer = nil
				_ = e.unload()
				nativeheap.Release()
			}
		})
		e.idleTimer = timer
	}
}

func (e *Engine) load() error {
	if e.closed {
		return errors.New("embedding engine is closed")
	}
	if e.session != nil {
		return nil
	}
	tk, err := tokenizers.FromFile(filepath.Join(e.cfg.Models, "tokenizer.json"))
	if err != nil {
		return fmt.Errorf("load embedding tokenizer: %w", err)
	}
	rt, err := onnx.Load(e.cfg.Library)
	if err != nil {
		tk.Close()
		return err
	}
	session, err := rt.OpenSession(filepath.Join(e.cfg.Models, "onnx", "model_q4.onnx"), e.cfg.Threads)
	if err != nil {
		tk.Close()
		return fmt.Errorf("load embedding model: %w", err)
	}
	inputs := []string{"input_ids", "attention_mask", "image_features", "video_features", "audio_features"}
	if e.cfg.Model == Harrier {
		inputs = []string{"input_ids", "attention_mask"}
	}
	if !slices.Equal(session.Inputs, inputs) || !slices.Contains(session.Outputs, "sentence_embedding") {
		session.Close()
		tk.Close()
		return errors.New("unexpected embedding ONNX graph; use the pinned export")
	}
	e.tokenizer, e.session = tk, session
	return nil
}

// EmbedQuery applies the model's retrieval instruction to a search query.
func (e *Engine) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := validateText(text); err != nil {
		return nil, err
	}
	prompt := "task: search result | query: " + text
	if e.cfg.Model == Harrier {
		prompt = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: " + text
	}
	result, err := e.embed(ctx, []string{prompt})
	if err != nil {
		return nil, err
	}
	return result[0], nil
}

// EmbedDocuments embeds passages in order, one at a time to bound native
// activation memory. Oversized passages return an error; callers must chunk.
func (e *Engine) EmbedDocuments(ctx context.Context, docs []Document) ([][]float32, error) {
	texts := make([]string, len(docs))
	for i, d := range docs {
		if err := validateText(d.Text); err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		if !utf8.ValidString(d.Title) || strings.ContainsRune(d.Title, 0) || len(d.Title) > 4096 {
			return nil, fmt.Errorf("document %d: invalid title", i)
		}
		title := strings.TrimSpace(d.Title)
		if e.cfg.Model == Gemma2 {
			if title == "" {
				title = "none"
			}
			texts[i] = "title: " + title + " | text: " + d.Text
		} else {
			texts[i] = d.Text
			if title != "" {
				texts[i] = title + "\n" + d.Text
			}
		}
	}
	return e.embed(ctx, texts)
}

func validateText(text string) error {
	if strings.TrimSpace(text) == "" || len(text) > 128*1024 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("embedding text must be nonempty UTF-8, at most 128 KiB, without NUL")
	}
	return nil
}

func (e *Engine) embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.gate }()
	if e.closed {
		return nil, errors.New("embedding engine is closed")
	}
	result := make([][]float32, len(texts))
	if len(texts) == 0 {
		return result, nil
	}
	e.stopIdle()
	defer e.armIdle()
	if err := e.load(); err != nil {
		return nil, err
	}
	for i, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		encoded, err := e.tokenizer.EncodeWithOptionsErr(text, true)
		if err != nil {
			return nil, err
		}
		if len(encoded.IDs) > e.cfg.MaxTokens {
			return nil, fmt.Errorf("input %d has %d tokens; limit is %d: chunk the passage: %w", i, len(encoded.IDs), e.cfg.MaxTokens, ErrTooManyTokens)
		}
		ids := make([]int64, len(encoded.IDs))
		mask := make([]int64, len(ids))
		for j, id := range encoded.IDs {
			ids[j] = int64(id)
			mask[j] = 1
		}
		shape := []int64{1, int64(len(ids))}
		inputs := []onnx.Tensor{{Shape: shape, Int64: ids}, {Shape: shape, Int64: mask}}
		if e.cfg.Model == Gemma2 {
			for range 3 {
				inputs = append(inputs, onnx.Tensor{Shape: []int64{0, 512}, Float32: []float32{}})
			}
		}
		outputs, err := e.session.Run(ctx, inputs)
		if err != nil {
			return nil, err
		}
		output := outputs[slices.Index(e.session.Outputs, "sentence_embedding")]
		native := 768
		if e.cfg.Model == Harrier {
			native = 1024
		}
		if !slices.Equal(output.Shape, []int64{1, int64(native)}) || len(output.Float32) != native {
			return nil, errors.New("unexpected embedding output shape")
		}
		vector := slices.Clone(output.Float32[:e.cfg.Dimensions])
		if err = normalize(vector); err != nil {
			return nil, err
		}
		result[i] = vector
	}
	return result, nil
}

func normalize(vector []float32) error {
	sum := float64(0)
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errors.New("embedding contains non-finite values")
		}
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return errors.New("embedding has zero norm")
	}
	norm := math.Sqrt(sum)
	for i, v := range vector {
		vector[i] = float32(float64(v) / norm)
	}
	return nil
}
