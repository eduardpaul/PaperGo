package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Limits of embedding calls.
const (
	// BatchSize is how many texts one request embeds.
	BatchSize = 64
	// maxInputChars bounds a text sent to the model.
	maxInputChars = 30_000
)

// openAI calls an OpenAI-compatible /v1/embeddings endpoint.
type openAI struct {
	config   EmbeddingsConfig
	endpoint endpoint
	dims     int
	measured sync.Mutex
}

func newOpenAI(c EmbeddingsConfig, client *http.Client) *openAI {
	return &openAI{config: c, endpoint: newEndpoint("embedding model", c.Endpoint, "/embeddings", c.APIKey, client), dims: c.Dimensions}
}

// Key is {provider}:{model}:{dims}.
func (e *openAI) Key(ctx context.Context) (string, error) {
	dims, err := e.dimensions(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s:%d", e.config.Provider, e.config.Model, dims), nil
}

// dimensions is the length of the model's vectors: the configured one, or
// measured once with a short text.
func (e *openAI) dimensions(ctx context.Context) (int, error) {
	e.measured.Lock()
	defer e.measured.Unlock()
	if e.dims > 0 {
		return e.dims, nil
	}
	vectors, err := e.request(ctx, []string{"dimensions"})
	if err != nil {
		return 0, err
	}
	e.dims = len(vectors[0])
	return e.dims, nil
}

// Embed embeds texts in batches of BatchSize.
func (e *openAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	dims, err := e.dimensions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += BatchSize {
		vectors, err := e.request(ctx, texts[start:min(len(texts), start+BatchSize)])
		if err != nil {
			return nil, err
		}
		for _, v := range vectors {
			if len(v) != dims {
				return nil, fmt.Errorf("the model returned %d dimensions instead of %d", len(v), dims)
			}
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// embedQuery embeds a search as a passage: OpenAI-compatible endpoints take
// no instruction.
func (e *openAI) embedQuery(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (e *openAI) Close() error { return nil }

// request embeds one batch.
func (e *openAI) request(ctx context.Context, texts []string) ([][]float32, error) {
	input := make([]string, len(texts))
	for i, t := range texts {
		if r := []rune(t); len(r) > maxInputChars {
			t = string(r[:maxInputChars])
		}
		if strings.TrimSpace(t) == "" {
			// Providers refuse empty input.
			t = " "
		}
		input[i] = t
	}
	body := map[string]any{"model": e.config.Model, "input": input, "encoding_format": "float"}
	if e.config.Dimensions > 0 {
		body["dimensions"] = e.config.Dimensions
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var vectors [][]float32
	err = e.endpoint.call(ctx, payload, func(raw []byte) error {
		vectors, err = decodeEmbeddings(raw, len(texts))
		return err
	})
	return vectors, err
}

func decodeEmbeddings(raw []byte, n int) ([][]float32, error) {
	var answer struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, errors.New("the answer is not an embeddings response")
	}
	vectors := make([][]float32, n)
	for _, d := range answer.Data {
		if d.Index < 0 || d.Index >= n || vectors[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, errors.New("the answer's embeddings do not match the input")
		}
		if !Normalize(d.Embedding) {
			return nil, errors.New("the answer has an invalid vector")
		}
		vectors[d.Index] = d.Embedding
	}
	for _, v := range vectors {
		if v == nil {
			return nil, errors.New("the answer has fewer embeddings than the input")
		}
	}
	return vectors, nil
}
