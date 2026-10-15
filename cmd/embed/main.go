// Command embed runs the local embedding engine on JSON from stdin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"papergo/internal/embedding"
)

type request struct {
	Queries   []string             `json:"queries"`
	Documents []embedding.Document `json:"documents"`
}
type response struct {
	Model       string      `json:"model"`
	ConfigureMS float64     `json:"configure_ms"`
	QueryMS     []float64   `json:"query_ms"`
	DocumentMS  float64     `json:"document_ms"`
	Queries     [][]float32 `json:"queries"`
	Documents   [][]float32 `json:"documents"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("embed", flag.ContinueOnError)
	model := flags.String("model", embedding.Gemma2, "embeddinggemma-2 or harrier-oss-v1-0.6b")
	models := flags.String("models", "", "directory installed by fetch-embedding-models.py")
	library := flags.String("library", os.Getenv("ONNXRUNTIME_LIB"), "ONNX Runtime shared library (same as OCR)")
	threads := flags.Int("threads", 1, "CPU threads per inference")
	dims := flags.Int("dimensions", 0, "vector dimensions (0 selects model default)")
	tokens := flags.Int("max-tokens", 512, "maximum tokens per input; longer passages are rejected")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("embed accepts JSON from stdin, no positional arguments")
	}
	var input request
	decoder := json.NewDecoder(io.LimitReader(in, 8*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON request")
	}
	if len(input.Queries)+len(input.Documents) > 2000 {
		return errors.New("at most 2000 inputs per request")
	}
	start := time.Now()
	engine, err := embedding.New(embedding.Config{Model: *model, Models: *models, Library: *library, Threads: *threads, Dimensions: *dims, MaxTokens: *tokens})
	if err != nil {
		return err
	}
	defer engine.Close()
	result := response{Model: engine.ID(), ConfigureMS: ms(start), Queries: make([][]float32, 0, len(input.Queries)), QueryMS: make([]float64, 0, len(input.Queries))}
	for _, query := range input.Queries {
		start = time.Now()
		vector, err := engine.EmbedQuery(ctx, query)
		if err != nil {
			return err
		}
		result.QueryMS = append(result.QueryMS, ms(start))
		result.Queries = append(result.Queries, vector)
	}
	start = time.Now()
	result.Documents, err = engine.EmbedDocuments(ctx, input.Documents)
	if err != nil {
		return err
	}
	result.DocumentMS = ms(start)
	return json.NewEncoder(out).Encode(result)
}
func ms(start time.Time) float64 { return float64(time.Since(start)) / float64(time.Millisecond) }
