package ai_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
)

func model(t *testing.T, endpoint string, dims int) ai.Embedder {
	t.Helper()
	e, err := ai.NewEmbedder(ai.EmbeddingsConfig{Provider: ai.ProviderOpenAI, Endpoint: endpoint, Model: "test-embed", Dimensions: dims}, nil)
	if err != nil || e == nil {
		t.Fatal(err)
	}
	return e
}

func TestEmbeddingsBatchesNormalizesAndCaches(t *testing.T) {
	server := aitest.New(t)
	e := model(t, server.Endpoint(), 0)
	ctx := context.Background()
	key, err := e.Key(ctx)
	if err != nil || key != fmt.Sprintf("openai:test-embed:%d", aitest.Dimensions) {
		t.Fatal(key, err)
	}
	texts := make([]string, ai.BatchSize*2+3)
	for i := range texts {
		texts[i] = fmt.Sprintf("text number %d", i)
	}
	before := server.Requests.Load()
	vectors, err := e.Embed(ctx, texts)
	if err != nil || len(vectors) != len(texts) {
		t.Fatal(len(vectors), err)
	}
	if n := server.Requests.Load() - before; n != 3 {
		t.Fatalf("%d requests for %d texts", n, len(texts))
	}
	for i, v := range vectors {
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if math.Abs(norm-1) > 1e-5 {
			t.Fatalf("vector %d has norm %f", i, norm)
		}
	}
	// The answer came out of order; each vector is its own text's.
	want := aitest.Vector(texts[5])
	ai.Normalize(want)
	for i := range want {
		if math.Abs(float64(want[i]-vectors[5][i])) > 1e-6 {
			t.Fatal("vectors are not in input order")
		}
	}
	before = server.Requests.Load()
	for range 3 {
		if _, err = e.EmbedQuery(ctx, "invoice"); err != nil {
			t.Fatal(err)
		}
	}
	if n := server.Requests.Load() - before; n != 1 {
		t.Fatalf("query embeddings are not cached: %d requests", n)
	}
}

// A busy or failing provider is tried once: the error says to try again
// and when, so the operation that called defers itself.
func TestEmbeddingsReportBusyProviders(t *testing.T) {
	server := aitest.New(t)
	e := model(t, server.Endpoint()+"/embeddings", 64)
	ctx := context.Background()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		server.Fail(1, status)
		before := server.Requests.Load()
		_, err := e.Embed(ctx, []string{"a"})
		var failure *ai.Error
		if !errors.As(err, &failure) || failure.Status != status || failure.Message != "unavailable" || server.Requests.Load()-before != 1 {
			t.Fatal(status, err)
		}
		if after, retry := ai.RetryAfter(err); !retry || after != 30*time.Second {
			t.Fatal(status, after, retry)
		}
	}
	server.Fail(1, http.StatusBadRequest)
	_, err := e.Embed(ctx, []string{"a"})
	if _, retry := ai.RetryAfter(err); err == nil || retry {
		t.Fatal("a refused request is worth trying again", err)
	}
	if _, err = e.Embed(ctx, []string{"a"}); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddingsConfig(t *testing.T) {
	if e, err := ai.NewEmbedder(ai.EmbeddingsConfig{Provider: ai.ProviderNone}, nil); e != nil || err != nil {
		t.Fatal(e, err)
	}
	for _, c := range []ai.EmbeddingsConfig{
		{Provider: "azure", Endpoint: ai.DefaultEndpoint, Model: "m"},
		{Provider: ai.ProviderOpenAI, Endpoint: ai.DefaultEndpoint},
		{Provider: ai.ProviderOpenAI, Endpoint: "ftp://x", Model: "m"},
		{Provider: ai.ProviderOpenAI, Endpoint: "https://u:p@x/v1", Model: "m"},
		{Provider: ai.ProviderOpenAI, Endpoint: ai.DefaultEndpoint, Model: "m", Dimensions: ai.MaxDimensions + 1},
	} {
		if c.Validate() == nil {
			t.Fatalf("%+v is valid", c)
		}
	}
	// A local model validates and names its vector space without loading.
	local, err := ai.NewEmbedder(ai.EmbeddingsConfig{Provider: ai.ProviderLocal, Models: "/missing/model", Library: "/missing/ort.so", Dimensions: 256}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key, err := local.Key(context.Background()); err != nil || !strings.HasPrefix(key, "local:embeddinggemma-2:") || !strings.Contains(key, ":d256:") {
		t.Fatal(key, err)
	}
	if _, err = local.Embed(context.Background(), []string{"text"}); err == nil {
		t.Fatal("a missing model embeds")
	}
	if (ai.EmbeddingsConfig{Provider: ai.ProviderLocal, Models: "/m", Library: "/l", Dimensions: 300}).Validate() == nil {
		t.Fatal("Gemma accepts 300 dimensions")
	}
	if ai.Normalize([]float32{0, 0}) || ai.Normalize([]float32{float32(math.NaN())}) {
		t.Fatal("degenerate vectors normalize")
	}
}
