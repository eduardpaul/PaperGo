// Command decide runs the verified local Laya engine on diagnostic JSON rows.
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
	"papergo/internal/decision"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	f := flag.NewFlagSet("decide", flag.ContinueOnError)
	models := f.String("models", os.Getenv("AI_DECISION_MODELS_PATH"), "verified Laya bundle directory")
	library := f.String("library", os.Getenv("ONNXRUNTIME_LIB"), "shared ONNX Runtime library")
	threads := f.Int("threads", 1, "CPU threads")
	tokens := f.Int("max-tokens", 1024, "strict context budget")
	head := f.Int("head-tokens", 256, "strict question/option budget")
	if err := f.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("decide accepts one JSON request on stdin")
	}
	var input struct {
		Requests []decision.Request `json:"requests"`
	}
	d := json.NewDecoder(io.LimitReader(in, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON request within 64 KiB")
	}
	e, err := decision.New(decision.Config{Models: *models, Library: *library, Threads: *threads, MaxTokens: *tokens, HeadTokens: *head})
	if err != nil {
		return err
	}
	defer e.Close()
	results, err := e.Decide(ctx, input.Requests)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Model   string            `json:"model"`
		Results []decision.Result `json:"results"`
	}{e.ID(), results})
}
