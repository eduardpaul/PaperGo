// Package ai gives PaperGo access to AI models: embedding models for
// semantic search, either local (EmbeddingGemma 2 or Harrier in the same
// ONNX Runtime as OCR, internal/embedding) or behind any OpenAI-compatible
// /v1/embeddings endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp). It is
// off unless configured.
package ai

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"papergo/internal/embedding"
)

// Embedding providers (AI_EMBEDDINGS_PROVIDER).
const (
	ProviderNone   = "none"
	ProviderLocal  = "local"
	ProviderOpenAI = "openai"
)

// DefaultEndpoint is OpenAI's API.
const DefaultEndpoint = "https://api.openai.com/v1"

// EmbeddingsConfig configures the embedding model.
type EmbeddingsConfig struct {
	// Provider is none (semantic search off), local or openai.
	Provider string
	// Model names the model: for local, embeddinggemma-2 (default) or
	// harrier-oss-v1-0.6b.
	Model string
	// Dimensions asks for vectors of this length; 0 takes the model's own
	// (measured once for openai).
	Dimensions int
	// Endpoint (openai) is the API's base URL (…/v1), or its /embeddings
	// URL; APIKey is optional for local servers.
	Endpoint string
	APIKey   string
	// Models (local) is the model's directory, as installed by
	// scripts/fetch-embedding-models.py; Library is ONNX Runtime (shared
	// with OCR). The tokenizer is statically linked into the binary.
	Models    string
	Library   string
	Threads   int
	MaxTokens int
}

// Enabled reports whether an embedding model is configured.
func (c EmbeddingsConfig) Enabled() bool { return c.Provider != "" && c.Provider != ProviderNone }

// Validate checks a configuration without loading anything.
func (c EmbeddingsConfig) Validate() error {
	if c.Dimensions < 0 || c.Dimensions > MaxDimensions {
		return fmt.Errorf("dimensions must be between 1 and %d", MaxDimensions)
	}
	switch c.Provider {
	case "", ProviderNone:
		return nil
	case ProviderLocal:
		_, err := embedding.New(c.local())
		return err
	case ProviderOpenAI:
	default:
		return errors.New("the provider must be none, local or openai")
	}
	if strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 {
		return errors.New("an embedding model needs a name of at most 200 characters")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the endpoint must be an http or https URL without credentials or a query")
	}
	return nil
}

func (c EmbeddingsConfig) local() embedding.Config {
	return embedding.Config{Model: c.Model, Models: c.Models, Library: c.Library, Threads: c.Threads, Dimensions: c.Dimensions, MaxTokens: c.MaxTokens}
}

// MaxDimensions bounds the length of vectors.
const MaxDimensions = 8192

// Embedder is an embedding model.
type Embedder interface {
	// Key identifies the model and its vector space: vectors are stored and
	// searched under it, so another model or length embeds again.
	Key(ctx context.Context) (string, error)
	// Embed returns the normalized vectors of passages, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery returns the normalized vector of a search, cached for 30
	// minutes.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	Close() error
}

// NewEmbedder returns the configured model, or nil when none is. It loads
// nothing: a local model loads on its first use.
func NewEmbedder(c EmbeddingsConfig, client *http.Client) (Embedder, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var p provider
	if c.Provider == ProviderLocal {
		engine, err := embedding.New(c.local())
		if err != nil {
			return nil, err
		}
		p = &local{engine: engine}
	} else {
		p = newOpenAI(c, client)
	}
	return &cached{provider: p, queries: newLRU(queryCacheSize), now: time.Now}, nil
}

// provider is an Embedder without the query cache.
type provider interface {
	Key(ctx context.Context) (string, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	embedQuery(ctx context.Context, text string) ([]float32, error)
	Close() error
}

// Query embedding cache bounds.
const (
	queryCacheSize = 1000
	queryCacheTTL  = 30 * time.Minute
)

// cached keeps the vectors of recent searches.
type cached struct {
	provider
	queries *lru
	now     func() time.Time
}

func (c *cached) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	key, err := c.Key(ctx)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(key + "\x00" + text))
	if v, ok := c.queries.get(hash, c.now()); ok {
		return v, nil
	}
	v, err := c.embedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	c.queries.put(hash, v, c.now().Add(queryCacheTTL))
	return v, nil
}

// Normalize scales a vector to length 1, and reports false for a vector
// that has no direction or is not finite.
func Normalize(v []float32) bool {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return false
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return true
}

// lru is a small cache of query vectors with an expiry per entry.
type lru struct {
	mu    sync.Mutex
	size  int
	order *list.List
	items map[[32]byte]*list.Element
}

type entry struct {
	key     [32]byte
	vector  []float32
	expires time.Time
}

func newLRU(size int) *lru {
	return &lru{size: size, order: list.New(), items: map[[32]byte]*list.Element{}}
}

func (c *lru) get(key [32]byte, now time.Time) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if now.After(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return nil, false
	}
	c.order.MoveToFront(el)
	return e.vector, true
}

func (c *lru) put(key [32]byte, vector []float32, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value = &entry{key, vector, expires}
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&entry{key, vector, expires})
	for c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry).key)
	}
}
