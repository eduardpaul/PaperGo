// Command goref gives the parity harness the Go engines' intermediate
// results that the CLIs don't print: tokenizer IDs, Laya encodings, OCR
// fixtures as raw pixels, and detection with options. It lives in its own
// module so the core module never builds it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	tokenizers "github.com/daulet/tokenizers"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"papergo/internal/decision"
	"papergo/internal/ocr"
	"papergo/internal/ocr/paddle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "goref:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: goref tokenize|encode|fixtures|raw|detect ...")
	}
	switch args[0] {
	case "tokenize":
		// tokenize TOKENIZER.json ADD_SPECIAL < ["text", ...] > [[ids], ...]
		return tokenize(args[1], args[2] == "1")
	case "encode":
		// encode LAYA_DIR < {"requests": [...]} > [{"ids","markers","qtype"} | {"error"}]
		return encode(args[1])
	case "fixtures":
		return fixtures(args[1])
	case "raw":
		// raw IMAGE OUT.raw > {"width","height","format"}
		return raw(args[1], args[2])
	case "detect":
		// detect MODELS IMAGE MIN_SCORE MAX_DIMENSION THREADS > ocr.Detection
		return detect(args[1:])
	case "profile":
		// profile < profile.json > memory and latency of the Go engines in process
		return profile()
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func tokenize(path string, special bool) error {
	var texts []string
	if err := json.NewDecoder(os.Stdin).Decode(&texts); err != nil {
		return err
	}
	tk, err := tokenizers.FromFile(path)
	if err != nil {
		return err
	}
	defer tk.Close()
	out := make([][]uint32, len(texts))
	for i, text := range texts {
		enc, err := tk.EncodeWithOptionsErr(text, special)
		if err != nil {
			return fmt.Errorf("text %d: %w", i, err)
		}
		out[i] = enc.IDs
		if out[i] == nil {
			out[i] = []uint32{}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func encode(models string) error {
	var input struct {
		Requests []decision.Request `json:"requests"`
		MaxTokens  int              `json:"max_tokens"`
		HeadTokens int              `json:"head_tokens"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	// The library is never loaded: Encode reads only the tokenizer.
	e, err := decision.New(decision.Config{Models: models, Library: "unused", MaxTokens: input.MaxTokens, HeadTokens: input.HeadTokens})
	if err != nil {
		return err
	}
	defer e.Close()
	type row struct {
		*decision.Encoding
		Error string `json:"error,omitempty"`
	}
	out := make([]row, len(input.Requests))
	for i, r := range input.Requests {
		enc, err := e.Encode(context.Background(), r)
		if err != nil {
			out[i].Error = err.Error()
			continue
		}
		out[i].Encoding = &enc
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

var invoice = []string{
	"Invoice 2026-0042",
	"Coffee beans 1 kg 18.90",
	"Whole milk 2.40",
	"Total amount EUR 21.30",
}

// page renders lines of black text as paddle_test.go does.
func page(lines []string, size float64, transparent bool) (*image.NRGBA, error) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	img := image.NewNRGBA(image.Rect(0, 0, 1000, int(size*2)*len(lines)+int(size*2)))
	if !transparent {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	}
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
	for i, l := range lines {
		d.Dot = fixed.P(int(size*2), int(size*2)*(i+1))
		d.DrawString(l)
	}
	return img, nil
}

// fixtures writes the OCR test images of paddle_test.go as PNG files.
func fixtures(dir string) error {
	write := func(name string, img image.Image) error {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		defer f.Close()
		return png.Encode(f, img)
	}
	img, err := page(invoice, 32, false)
	if err != nil {
		return err
	}
	if err = write("invoice.png", img); err != nil {
		return err
	}
	transparent, err := page(invoice, 32, true)
	if err != nil {
		return err
	}
	if err = write("transparent.png", transparent); err != nil {
		return err
	}
	turned := image.NewNRGBA(transparent.Rect)
	w, h := transparent.Rect.Dx(), transparent.Rect.Dy()
	for y := range h {
		for x := range w {
			turned.Set(w-1-x, h-1-y, transparent.At(x, y))
		}
	}
	if err = write("upside-down-transparent.png", turned); err != nil {
		return err
	}
	lines := make([]string, 0, 60)
	for len(lines) < 60 {
		lines = append(lines, invoice...)
	}
	tall, err := page(lines, 24, false)
	if err != nil {
		return err
	}
	if err = write("tall.png", tall); err != nil {
		return err
	}
	gray := image.NewGray(img.Rect)
	draw.Draw(gray, gray.Rect, img, image.Point{}, draw.Src)
	if err = write("invoice-gray.png", gray); err != nil {
		return err
	}
	blank := image.NewGray(image.Rect(0, 0, 640, 480))
	draw.Draw(blank, blank.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	return write("blank.png", blank)
}

func decode(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// raw writes an image's pixels in the protocol's formats, keeping the type
// the Go engine sees: Gray as gray8, NRGBA as rgba8, opaque RGBA as rgb8.
func raw(in, out string) error {
	img, err := decode(in)
	if err != nil {
		return err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var format string
	var pix []byte
	switch m := img.(type) {
	case *image.Gray:
		format = "gray8"
		for y := range h {
			pix = append(pix, m.Pix[y*m.Stride:y*m.Stride+w]...)
		}
	case *image.NRGBA:
		format = "rgba8"
		for y := range h {
			pix = append(pix, m.Pix[y*m.Stride:y*m.Stride+4*w]...)
		}
	case *image.RGBA:
		if !m.Opaque() {
			return errors.New("translucent premultiplied RGBA is not a protocol format")
		}
		format = "rgb8"
		pix = make([]byte, 0, 3*w*h)
		for y := range h {
			row := m.Pix[y*m.Stride:]
			for x := range w {
				pix = append(pix, row[4*x:4*x+3]...)
			}
		}
	default:
		return fmt.Errorf("unsupported image type %T", img)
	}
	if err = os.WriteFile(out, pix, 0o644); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"width": w, "height": h, "format": format})
}

func detect(args []string) error {
	img, err := decode(args[1])
	if err != nil {
		return err
	}
	minScore, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		return err
	}
	dimension, err := strconv.Atoi(args[3])
	if err != nil {
		return err
	}
	threads, err := strconv.Atoi(args[4])
	if err != nil {
		return err
	}
	e, err := paddle.New(paddle.Config{Library: os.Getenv("ONNXRUNTIME_LIB"), Models: args[0], Threads: threads})
	if err != nil {
		return err
	}
	defer e.Close()
	d, err := e.Detect(context.Background(), img, ocr.DetectOptions{MinScore: minScore, MaxDimension: dimension})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(d)
}
