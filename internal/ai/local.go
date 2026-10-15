package ai

import (
	"context"
	"errors"

	"papergo/internal/embedding"
)

// local runs an embedding model in the process (internal/embedding).
type local struct{ engine *embedding.Engine }

// Key is local: and the engine's ID: model, revision, quantization,
// dimensions and token limit.
func (l *local) Key(context.Context) (string, error) { return ProviderLocal + ":" + l.engine.ID(), nil }

// shortenings bounds how often a passage over the token limit is cut by a
// quarter and tried again.
const shortenings = 8

// Embed embeds passages one at a time. A passage over the model's token
// limit is embedded by its beginning, so one long chunk never holds back
// the others.
func (l *local) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		for try := 0; ; try++ {
			vectors, err := l.engine.EmbedDocuments(ctx, []embedding.Document{{Text: text}})
			if err == nil {
				out[i] = vectors[0]
				break
			}
			runes := []rune(text)
			if !errors.Is(err, embedding.ErrTooManyTokens) || try == shortenings || len(runes) < 8 {
				return nil, err
			}
			text = string(runes[:len(runes)*3/4])
		}
	}
	return out, nil
}

func (l *local) embedQuery(ctx context.Context, text string) ([]float32, error) {
	return l.engine.EmbedQuery(ctx, text)
}

func (l *local) Close() error { return l.engine.Close() }
