// Package paddle is the PaddleOCR engine: PP-OCRv6 small detection and
// recognition with the PP-LCNet text-line orientation classifier, the same
// models and parameters as PaperDotNet. Inference runs on CPU in ONNX Runtime,
// loaded through the shared cgo ONNX SDK. Running
// it needs the ONNX Runtime shared library (1.23 or newer) and glibc.
package paddle

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"papergo/internal/nativeheap"
	"papergo/internal/ocr"
	"papergo/internal/onnx"
)

// The bundled model files, in Config.Models.
const (
	DetectorModel   = "PP-OCRv6_small_det.onnx"
	RecognizerModel = "PP-OCRv6_small_rec.onnx"
	ClassifierModel = "ch_PP-LCNet_x0_25_textline_ori_cls_mobile.onnx"
	Dictionary      = "ppocrv6_small_dict.txt"
)

// Pipeline parameters, as in PaperDotNet.
const (
	// detThreshold binarizes the probability map and unclipRatio expands
	// boxes: the values of the detector's inference configuration.
	detThreshold = 0.2
	unclipRatio  = 1.4
	// recognizeMinScore is the box score Recognize detects with.
	recognizeMinScore = 0.5
	// minLineConfidence drops lines whose characters average less.
	minLineConfidence = 0.5
	// flipConfidence is the classifier score needed to turn a line over.
	flipConfidence = 0.9
	recHeight      = 48
	// maxDetectionPixels bounds the detector's feature map, so a square
	// photo gets a smaller preview than a long receipt.
	maxDetectionPixels = 1 << 20
)

var (
	// The official detector export uses ImageNet normalization.
	detMean = [3]float32{0.485 * 255, 0.456 * 255, 0.406 * 255}
	detStd  = [3]float32{0.229 * 255, 0.224 * 255, 0.225 * 255}
	// Recognition and classification scale pixels to [-1, 1].
	lineMean = [3]float32{127.5, 127.5, 127.5}
	lineStd  = [3]float32{127.5, 127.5, 127.5}
)

// Config configures the engine.
type Config struct {
	// Library is the ONNX Runtime shared library: libonnxruntime.so,
	// libonnxruntime.dylib or onnxruntime.dll, version 1.23 or newer.
	Library string
	// Models is the directory holding the bundled model files.
	Models string
	// Threads is the CPU threads one inference uses, 1 to 32. Default 1.
	Threads int
	// MaxDetectionDimension bounds the longest side of the detector's
	// preview: a multiple of 32 from 256 to 2600. Default 1536.
	MaxDetectionDimension int
}

// gate serializes inference across every engine of the process, so OCR uses
// a bounded amount of CPU and native memory however many runs ask for it.
var gate = make(chan struct{}, 1)

// Engine is the PaddleOCR engine. Models load on first use.
type Engine struct {
	cfg         Config
	info        ocr.Info
	models      *models
	idleTimer   *time.Timer
	idleTimeout time.Duration
}

type models struct {
	det, cls, rec *onnx.Session
	keys          []string
	clsW, clsH    int
	// maxDimension is Config.MaxDetectionDimension.
	maxDimension int
}

// New checks the configuration and the model files. The library and the
// models load on first use.
func New(cfg Config) (*Engine, error) {
	if cfg.Threads == 0 {
		cfg.Threads = 1
	}
	if cfg.MaxDetectionDimension == 0 {
		cfg.MaxDetectionDimension = 1536
	}
	switch {
	case cfg.Library == "":
		return nil, errors.New("the ONNX Runtime library path is required")
	case cfg.Models == "":
		return nil, errors.New("the OCR model directory is required")
	case cfg.Threads < 1 || cfg.Threads > 32:
		return nil, errors.New("OCR threads must be between 1 and 32")
	case cfg.MaxDetectionDimension < 256 || cfg.MaxDetectionDimension > 2600 || cfg.MaxDetectionDimension%32 != 0:
		return nil, errors.New("the maximum detection dimension must be a multiple of 32 between 256 and 2600")
	}
	e := &Engine{cfg: cfg, info: ocr.Info{Engine: "paddleocr"}, idleTimeout: 5 * time.Minute}
	for _, name := range []string{DetectorModel, RecognizerModel, ClassifierModel, Dictionary} {
		f, err := os.Open(filepath.Join(cfg.Models, name))
		if err != nil {
			return nil, fmt.Errorf("PaddleOCR model file: %w", err)
		}
		h := sha256.New()
		_, err = bufio.NewReader(f).WriteTo(h)
		f.Close()
		if err != nil {
			return nil, err
		}
		e.info.Models = append(e.info.Models, ocr.Model{Name: name, SHA256: hex.EncodeToString(h.Sum(nil))})
	}
	return e, nil
}

// Info names the engine and its model files with their SHA-256.
func (e *Engine) Info() ocr.Info { return e.info }

func acquire(ctx context.Context) error {
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release() { <-gate }

// Close releases the models. The engine loads them again if it is used.
func (e *Engine) Close() error {
	gate <- struct{}{}
	defer release()
	e.stopIdle()
	e.unload()
	nativeheap.Release()
	return nil
}

func (e *Engine) unload() {
	if m := e.models; m != nil {
		m.det.Close()
		m.cls.Close()
		m.rec.Close()
		e.models = nil
	}
}

func (e *Engine) stopIdle() {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
		e.idleTimer = nil
	}
}

func (e *Engine) release() {
	if e.models != nil {
		var timer *time.Timer
		timer = time.AfterFunc(e.idleTimeout, func() {
			gate <- struct{}{}
			defer release()
			if e.idleTimer == timer {
				e.idleTimer = nil
				e.unload()
				nativeheap.Release()
			}
		})
		e.idleTimer = timer
	}
	release()
}

// load opens the sessions; the caller holds the gate.
func (e *Engine) load() (*models, error) {
	if e.models != nil {
		return e.models, nil
	}
	rt, err := onnx.Load(e.cfg.Library)
	if err != nil {
		return nil, err
	}
	open := func(name string) (*onnx.Session, error) {
		data, err := os.ReadFile(filepath.Join(e.cfg.Models, name))
		if err != nil {
			return nil, err
		}
		s, err := rt.NewSession(data, e.cfg.Threads)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", name, err)
		}
		return s, nil
	}
	m := &models{maxDimension: e.cfg.MaxDetectionDimension}
	defer func() {
		if e.models == nil {
			for _, s := range []*onnx.Session{m.det, m.cls, m.rec} {
				if s != nil {
					s.Close()
				}
			}
		}
	}()
	if m.det, err = open(DetectorModel); err != nil {
		return nil, err
	}
	if m.cls, err = open(ClassifierModel); err != nil {
		return nil, err
	}
	if m.rec, err = open(RecognizerModel); err != nil {
		return nil, err
	}
	if m.keys, err = readKeys(filepath.Join(e.cfg.Models, Dictionary)); err != nil {
		return nil, err
	}
	// The recognizer's classes are the CTC blank, the dictionary and a space.
	if s := m.rec.OutputShapes[0]; len(s) != 3 || s[2] != int64(len(m.keys)) {
		return nil, fmt.Errorf("the recognition model's output %v does not match the dictionary's %d characters", s, len(m.keys)-2)
	}
	s := m.cls.InputShapes[0]
	if len(s) != 4 || s[2] <= 0 || s[3] <= 0 {
		return nil, fmt.Errorf("the classifier input %v has no fixed size", s)
	}
	m.clsH, m.clsW = int(s[2]), int(s[3])
	e.models = m
	return m, nil
}

// readKeys reads the dictionary as recognizer classes: the CTC blank, one
// class per line, and a final space.
func readKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys := []string{"#"}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	for _, l := range lines {
		keys = append(keys, strings.TrimSuffix(string(l), "\r"))
	}
	return append(keys, " "), nil
}

// Detect finds text regions.
func (e *Engine) Detect(ctx context.Context, img image.Image, opts ocr.DetectOptions) (ocr.Detection, error) {
	if err := ocr.CheckSize(img.Bounds()); err != nil {
		return ocr.Detection{}, err
	}
	if err := acquire(ctx); err != nil {
		return ocr.Detection{}, err
	}
	e.stopIdle()
	defer e.release()
	m, err := e.load()
	if err != nil {
		return ocr.Detection{}, err
	}
	dimension := 0
	if opts.MaxDimension != 0 {
		if opts.MaxDimension < 256 || opts.MaxDimension > ocr.MaxDetectionDimension {
			return ocr.Detection{}, fmt.Errorf("the detection dimension must be between 256 and %d", ocr.MaxDetectionDimension)
		}
		dimension = opts.MaxDimension
	}
	return m.detect(ctx, img, dimension, opts.MinScore)
}

// Recognize reads the image's text lines.
func (e *Engine) Recognize(ctx context.Context, img image.Image) (ocr.Page, error) {
	if err := ocr.CheckSize(img.Bounds()); err != nil {
		return ocr.Page{}, err
	}
	if err := acquire(ctx); err != nil {
		return ocr.Page{}, err
	}
	e.stopIdle()
	defer e.release()
	m, err := e.load()
	if err != nil {
		return ocr.Page{}, err
	}
	src := opaque(img)
	page := ocr.Page{Width: src.Rect.Dx(), Height: src.Rect.Dy(), Lines: []ocr.Line{}}
	det, err := m.detect(ctx, src, 0, recognizeMinScore)
	if err != nil {
		return ocr.Page{}, err
	}
	for _, region := range det.Regions {
		line, quad, err := crop(src, region.Quad)
		if err != nil {
			return ocr.Page{}, err
		}
		flip, err := m.upsideDown(ctx, line)
		if err != nil {
			return ocr.Page{}, err
		}
		if flip {
			line, quad = rotate180(line), ocr.Quad{quad[2], quad[3], quad[0], quad[1]}
		}
		chars, scores, columns, count, err := m.read(ctx, line)
		if err != nil {
			return ocr.Page{}, err
		}
		if len(scores) == 0 {
			continue
		}
		var sum float64
		for _, s := range scores {
			sum += float64(s)
		}
		confidence := sum / float64(len(scores))
		text := strings.Join(chars, "")
		if confidence < minLineConfidence || strings.TrimSpace(text) == "" {
			continue
		}
		page.Lines = append(page.Lines, ocr.Line{Text: text, Confidence: confidence, Quad: quad, Words: words(chars, columns, count, quad)})
	}
	return page, nil
}

// detectionDimension lowers the preview's longest side for squarer images,
// so its area stays near maxDetectionPixels.
func detectionDimension(w, h, maximum int) int {
	long, short := float64(max(w, h)), float64(min(w, h))
	return min(maximum, max(32, int(math.Sqrt(maxDetectionPixels*long/short))/32*32))
}

// networkDimension rounds to the nearest multiple of 32 the detector needs.
func networkDimension(pixels int) int {
	return max(32, int(math.RoundToEven(float64(pixels)/32))*32)
}

// detect finds text regions on a preview of img whose longest side is at
// most dimension, or the engine's area-bounded default when it is 0.
func (m *models) detect(ctx context.Context, img image.Image, dimension int, minScore float64) (ocr.Detection, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if dimension == 0 {
		dimension = detectionDimension(w, h, m.maxDimension)
	}
	k := math.Min(1, float64(dimension)/float64(max(w, h)))
	pw, ph := max(1, int(math.RoundToEven(float64(w)*k))), max(1, int(math.RoundToEven(float64(h)*k)))
	// The cubic filter is not interpolating: an image already small enough
	// is analyzed as it is.
	preview := opaque(img)
	if pw != w || ph != h {
		preview = resize(img, pw, ph, color.White)
	}
	nw, nh := networkDimension(pw), networkDimension(ph)
	input, shape := tensor(resize(preview, nw, nh, color.White), detMean, detStd)
	out, dims, err := m.det.RunFloat(ctx, input, shape)
	if err != nil {
		return ocr.Detection{}, err
	}
	if len(dims) != 4 || dims[2] != int64(nh) || dims[3] != int64(nw) {
		return ocr.Detection{}, fmt.Errorf("unexpected detector output %v", dims)
	}
	// Boxes go from the network size to the preview, then to the source.
	sx, sy := float32(nw)/float32(pw), float32(nh)/float32(ph)
	type box struct {
		pts   [4]image.Point
		score float64
	}
	var boxes []box
	for _, d := range dbBoxes(out, nh, nw, minScore, detThreshold, unclipRatio) {
		var bx box
		bx.score = d.score
		for i, p := range d.pts {
			bx.pts[i] = image.Point{
				X: max(0, min(int(float32(p.X)/sx), pw)),
				Y: max(0, min(int(float32(p.Y)/sy), ph)),
			}
		}
		boxes = append(boxes, bx)
	}
	// Reading order, as PP-OCR sorts boxes: by top, boxes whose tops are
	// within 10 preview pixels of the previous one share a line, and each
	// line goes left to right.
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].pts[0].Y < boxes[j].pts[0].Y })
	lines := make([]int, len(boxes))
	for i := 1; i < len(boxes); i++ {
		lines[i] = lines[i-1]
		if boxes[i].pts[0].Y-boxes[i-1].pts[0].Y >= 10 {
			lines[i]++
		}
	}
	order := make([]int, len(boxes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, c int) bool {
		i, j := order[a], order[c]
		if lines[i] != lines[j] {
			return lines[i] < lines[j]
		}
		return boxes[i].pts[0].X < boxes[j].pts[0].X
	})
	det := ocr.Detection{Width: pw, Height: ph, Regions: make([]ocr.Region, 0, len(boxes))}
	for _, i := range order {
		var q ocr.Quad
		for c, p := range boxes[i].pts {
			q[c] = image.Point{
				X: max(0, min(int(math.RoundToEven(float64(p.X)*float64(w)/float64(pw))), w-1)),
				Y: max(0, min(int(math.RoundToEven(float64(p.Y)*float64(h)/float64(ph))), h-1)),
			}
		}
		det.Regions = append(det.Regions, ocr.Region{Quad: q, Score: boxes[i].score})
	}
	return det, nil
}

// upsideDown classifies a text line's orientation. The line keeps its
// aspect ratio on a mid-gray canvas of the classifier's input size.
func (m *models) upsideDown(ctx context.Context, line *image.RGBA) (bool, error) {
	w := min(m.clsW, max(1, int(math.Ceil(float64(m.clsH)*float64(line.Rect.Dx())/float64(line.Rect.Dy())))))
	canvas := filled(m.clsW, m.clsH, color.Gray{Y: 128})
	mitchell.Scale(canvas, image.Rect(0, 0, w, m.clsH), line, line.Rect, xdraw.Src, nil)
	input, shape := tensor(canvas, lineMean, lineStd)
	out, _, err := m.cls.RunFloat(ctx, input, shape)
	if err != nil {
		return false, err
	}
	if len(out) < 2 {
		return false, fmt.Errorf("unexpected classifier output of %d values", len(out))
	}
	return out[1] > out[0] && float64(out[1]) >= flipConfidence, nil
}

// read recognizes a text line: its characters, their confidences, the CTC
// column of each and the number of columns.
func (m *models) read(ctx context.Context, line *image.RGBA) ([]string, []float32, []int, int, error) {
	scale := float32(recHeight) / float32(line.Rect.Dy())
	w := max(1, int(float32(line.Rect.Dx())*scale))
	input, shape := tensor(resize(line, w, recHeight, color.White), lineMean, lineStd)
	out, dims, err := m.rec.RunFloat(ctx, input, shape)
	if err != nil {
		return nil, nil, nil, 0, err
	}
	if len(dims) != 3 || dims[2] != int64(len(m.keys)) {
		return nil, nil, nil, 0, fmt.Errorf("unexpected recognizer output %v", dims)
	}
	chars, scores, columns := ctcDecode(out, int(dims[1]), m.keys)
	return chars, scores, columns, int(dims[1]), nil
}

// ctcDecode is greedy CTC decoding: the best class of each column, without
// blanks (class 0) and without repeats of the previous column's class.
func ctcDecode(probs []float32, columns int, keys []string) ([]string, []float32, []int) {
	classes := len(keys)
	var chars []string
	var scores []float32
	var cols []int
	last := 0
	for t := range columns {
		row := probs[t*classes : (t+1)*classes]
		best, value := 0, float32(math.Inf(-1))
		for c, p := range row {
			if p > value {
				best, value = c, p
			}
		}
		if best > 0 && !(t > 0 && best == last) {
			chars = append(chars, keys[best])
			scores = append(scores, value)
			cols = append(cols, t)
		}
		last = best
	}
	return chars, scores, cols
}
