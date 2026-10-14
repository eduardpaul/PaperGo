// Command ocr runs the PaddleOCR engine on image files and prints the result
// as JSON, to check an OCR setup or tune detection. It reads JPEG, PNG and
// WebP; EXIF orientation is not applied.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	_ "golang.org/x/image/webp"

	"papergo/internal/ocr"
	"papergo/internal/ocr/paddle"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultLibrary() string {
	if v := os.Getenv("ONNXRUNTIME_LIB"); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so"
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("ocr", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: ocr [flags] image...")
		flags.PrintDefaults()
	}
	library := flags.String("library", defaultLibrary(), "ONNX Runtime shared library (ONNXRUNTIME_LIB)")
	models := flags.String("models", "models/paddleocr", "directory of the PaddleOCR model files")
	threads := flags.Int("threads", 1, "CPU threads per inference")
	detect := flags.Bool("detect", false, "only detect text regions")
	minScore := flags.Float64("min-score", 0.5, "minimum region score for -detect, 0 to 1")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return fmt.Errorf("no image given")
	}
	engine, err := paddle.New(paddle.Config{Library: *library, Models: *models, Threads: *threads})
	if err != nil {
		return err
	}
	defer engine.Close()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	for _, path := range flags.Args() {
		img, err := decode(path)
		if err != nil {
			return err
		}
		start := time.Now()
		result := map[string]any{"file": path, "engine": engine.Info()}
		if *detect {
			result["detection"], err = engine.Detect(ctx, img, ocr.DetectOptions{MinScore: *minScore})
		} else {
			var page ocr.Page
			page, err = engine.Recognize(ctx, img)
			result["page"], result["text"] = page, page.Text()
		}
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		result["milliseconds"] = time.Since(start).Milliseconds()
		if err = encoder.Encode(result); err != nil {
			return err
		}
	}
	return nil
}

func decode(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return img, nil
}
