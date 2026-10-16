// Package decision runs Laya multilingual in the shared CPU ONNX Runtime. No model,
// tokenizer or library is downloaded during inference.
package decision

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

const Revision = "1720e3e3357cfe1e281542e223f8273b0890ca34"

// Config selects the installed FP32 export. Zero numeric values use defaults.
type Config struct {
	Models, Library                string
	Threads, MaxTokens, HeadTokens int
}

type Request struct {
	State    string   `json:"state"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Type     string   `json:"type,omitempty"`
}

// Encoding is the lossless upstream marker serialization, before batch padding.
type Encoding struct {
	IDs     []int64 `json:"ids"`
	Markers []int64 `json:"markers"`
	QType   int64   `json:"qtype"`
}

// Result retains unrounded softmax probabilities. They are model scores, not
// calibrated certainty. Index always names the highest-logit option.
type Result struct {
	Index         int       `json:"index"`
	ActLogits     []float32 `json:"act_logits"`
	Logits        []float32 `json:"logits"`
	Probabilities []float64 `json:"probabilities"`
	Score         *float64  `json:"score,omitempty"`
	Noul          *float64  `json:"noul,omitempty"`
	Tokens        int       `json:"tokens"`
}

// RowError identifies a failed request without including source text.
type RowError struct {
	Index int
	Err   error
}

func (e *RowError) Error() string { return fmt.Sprintf("question %d: %v", e.Index, e.Err) }
func (e *RowError) Unwrap() error { return e.Err }

// Model is the workflow port; an Engine is its local implementation.
type Model interface {
	ID() string
	Decide(context.Context, []Request) ([]Result, error)
}

// Engine serializes tokenizer/model loading, inference, eviction and closing.
type Engine struct {
	cfg                 Config
	gate                chan struct{}
	tokenizer           *tokenizers.Tokenizer
	session             *onnx.Session
	cls, sep, mask, pad int64
	closed              bool
	verified            bool
	idleTimer           *time.Timer
	idleTimeout         time.Duration
}

func New(c Config) (*Engine, error) {
	if c.Threads == 0 {
		c.Threads = 1
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 1024
	}
	if c.HeadTokens == 0 {
		c.HeadTokens = 256
	}
	if c.Threads < 1 || c.Threads > 32 || c.MaxTokens < 37 || c.MaxTokens > 1024 || c.HeadTokens < 32 || c.HeadTokens > 256 || c.HeadTokens > c.MaxTokens-5 {
		return nil, errors.New("laya requires 1–32 threads, 32–256 head tokens, and head+4 < max_tokens <= 1024")
	}
	for _, p := range []string{c.Models, c.Library} {
		if p == "" || strings.ContainsRune(p, 0) {
			return nil, errors.New("laya model and ONNX library paths are required without NUL")
		}
	}
	return &Engine{cfg: c, gate: make(chan struct{}, 1), idleTimeout: 5 * time.Minute}, nil
}

func (e *Engine) ID() string {
	return fmt.Sprintf("laya:%s:74dc1d3dccd214c105832b195f191f18c1172b799c78301fdd88f3925608d46e:fp32:t%d:h%d:markers-v1:temperature1:batch8-2048-v1:threads%d", Revision, e.cfg.MaxTokens, e.cfg.HeadTokens, e.cfg.Threads)
}

func (e *Engine) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-e.gate
		return err
	}
	if e.closed {
		<-e.gate
		return errors.New("laya engine is closed")
	}
	return nil
}

func (e *Engine) unload() error {
	e.verified = false
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
	if e.tokenizer == nil || e.closed {
		return
	}
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

func (e *Engine) Close() error {
	e.gate <- struct{}{}
	defer func() { <-e.gate }()
	e.closed = true
	e.stopIdle()
	err := e.unload()
	nativeheap.Release()
	return err
}

func (e *Engine) loadTokenizer() error {
	if e.tokenizer != nil {
		return nil
	}
	tk, err := tokenizers.FromFile(filepath.Join(e.cfg.Models, "tokenizer.json"))
	if err != nil {
		return fmt.Errorf("load Laya tokenizer: %w", err)
	}
	for i, text := range []string{"<bos>", "<eos>", "<mask>", "<pad>"} {
		ids, _, err := tk.EncodeErr(text, false)
		if err != nil || len(ids) != 1 {
			tk.Close()
			return fmt.Errorf("laya tokenizer lacks %s", text)
		}
		switch i {
		case 0:
			e.cls = int64(ids[0])
		case 1:
			e.sep = int64(ids[0])
		case 2:
			e.mask = int64(ids[0])
		case 3:
			e.pad = int64(ids[0])
		}
	}
	e.tokenizer = tk
	return nil
}

func (e *Engine) loadModel() error {
	if e.session != nil {
		return nil
	}
	rt, err := onnx.Load(e.cfg.Library)
	if err != nil {
		return err
	}
	s, err := rt.OpenSession(filepath.Join(e.cfg.Models, "model.onnx"), e.cfg.Threads)
	if err != nil {
		return fmt.Errorf("load Laya model: %w", err)
	}
	if !slices.Equal(s.InputTypes, []string{"int64", "int64", "int64", "bool", "int64"}) || !slices.Equal(s.OutputTypes, []string{"float32", "float32"}) || !slices.Equal(s.Inputs, []string{"input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"}) || !slices.Equal(s.Outputs, []string{"logits", "act_logits"}) {
		s.Close()
		return errors.New("unexpected Laya ONNX graph; install the pinned export")
	}
	e.session = s
	return nil
}

func validate(r Request) error {
	if len(r.Options) < 2 || len(r.Options) > 20 {
		return errors.New("laya needs 2–20 options")
	}
	if r.Type != "" && r.Type != "choice" && r.Type != "score" && r.Type != "noul" {
		return errors.New("laya type must be choice, score or noul")
	}
	if r.Type == "noul" && len(r.Options) != 2 {
		return errors.New("noul needs false then true options")
	}
	if strings.TrimSpace(r.State) == "" || strings.TrimSpace(r.Question) == "" {
		return errors.New("state and question must not be empty")
	}
	for _, text := range append([]string{r.State, r.Question}, r.Options...) {
		if !utf8.ValidString(text) || len(text) > 128*1024 || strings.ContainsRune(text, 0) || strings.Contains(text, "<mask>") || strings.Contains(text, "<bos>") || strings.Contains(text, "<eos>") || strings.Contains(text, "<pad>") {
			return errors.New("laya text must be UTF-8, at most 128 KiB, without NUL or reserved markers")
		}
	}
	for _, option := range r.Options {
		if strings.TrimSpace(option) == "" {
			return errors.New("laya options must not be empty")
		}
	}
	return nil
}

// encode implements the verified markers-v1 lossless serialization; see NOTICE.
func (e *Engine) encode(r Request) (Encoding, error) {
	kind := r.Type
	if kind == "" {
		kind = "choice"
	}
	encode := func(s string) ([]int64, error) {
		v, _, err := e.tokenizer.EncodeErr(s, false)
		ids := make([]int64, len(v))
		for i, id := range v {
			ids[i] = int64(id)
		}
		return ids, err
	}
	head, err := encode(kind + " question: " + r.Question)
	if err != nil {
		return Encoding{}, err
	}
	options := make([][]int64, len(r.Options))
	budget := e.cfg.HeadTokens
	for i, option := range r.Options {
		v, err := encode(" " + option)
		if err != nil {
			return Encoding{}, err
		}
		if len(v) > 48 {
			return Encoding{}, errors.New("laya option exceeds 48 tokens")
		}
		options[i] = append([]int64{e.mask}, v...)
		budget -= len(options[i])
	}
	// Upstream truncates options when the remaining head budget falls below 16.
	// Strict encoding rejects any such loss rather than altering the request.
	if budget < 16 {
		perOption := max(4, (e.cfg.HeadTokens-16)/len(options))
		for _, option := range options {
			if len(option) > perOption {
				return Encoding{}, errors.New("laya options exceed lossless head budget")
			}
		}
	}
	if len(head) > budget {
		return Encoding{}, errors.New("laya question/options exceed lossless head budget")
	}
	enc := Encoding{IDs: append([]int64{e.cls}, head...)}
	enc.IDs = append(enc.IDs, e.sep)
	for _, option := range options {
		enc.Markers = append(enc.Markers, int64(len(enc.IDs)))
		enc.IDs = append(enc.IDs, option...)
	}
	enc.IDs = append(enc.IDs, e.sep)
	state, err := encode(r.State)
	if err != nil {
		return Encoding{}, err
	}
	room := e.cfg.MaxTokens - len(enc.IDs) - 1
	if room < 1 || len(state) > room {
		return Encoding{}, errors.New("laya state exceeds lossless context budget; split the document")
	}
	enc.IDs = append(enc.IDs, state...)
	enc.IDs = append(enc.IDs, e.sep)
	if kind == "score" {
		enc.QType = 1
	}
	if kind == "noul" {
		enc.QType = 2
	}
	return enc, nil
}

// Encode exposes exact token IDs for reference checks, loading only the tokenizer.
func (e *Engine) Encode(ctx context.Context, r Request) (Encoding, error) {
	if err := validate(r); err != nil {
		return Encoding{}, err
	}
	if err := e.acquire(ctx); err != nil {
		return Encoding{}, err
	}
	defer func() { <-e.gate }()
	e.stopIdle()
	defer e.armIdle()
	if err := e.verifyBundle(); err != nil {
		return Encoding{}, err
	}
	if err := e.loadTokenizer(); err != nil {
		return Encoding{}, err
	}
	return e.encode(r)
}

// Decide validates all rows, then runs deterministic bounded batches. Each question
// includes its own state; multiple tags do not share an encoder forward pass.
func (e *Engine) Decide(ctx context.Context, rows []Request) ([]Result, error) {
	if len(rows) < 1 || len(rows) > 50 {
		return nil, errors.New("1–50 Laya questions per call")
	}
	for i, r := range rows {
		if err := validate(r); err != nil {
			return nil, &RowError{Index: i, Err: err}
		}
	}
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.gate }()
	results := make([]Result, 0, len(rows))
	e.stopIdle()
	defer e.armIdle()
	if err := e.verifyBundle(); err != nil {
		return nil, err
	}
	if err := e.loadTokenizer(); err != nil {
		return nil, err
	}
	encoded := make([]Encoding, len(rows))
	for i, r := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		encoded[i], err = e.encode(r)
		if err != nil {
			return nil, &RowError{Index: i, Err: err}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := e.loadModel(); err != nil {
		_ = e.unload()
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		_ = e.unload()
		return nil, err
	}
	for start := 0; start < len(encoded); {
		end, length, k := start, 0, 0
		for end < len(encoded) && end-start < 8 {
			nextLength := min(e.cfg.MaxTokens, (max(length, len(encoded[end].IDs))+7)/8*8)
			if end > start && (end-start+1)*nextLength > 2048 {
				break
			}
			length, k = nextLength, max(k, len(encoded[end].Markers))
			end++
		}
		n := end - start
		ids, attention := make([]int64, n*length), make([]int64, n*length)
		markers, mask, qtype := make([]int64, n*k), make([]bool, n*k), make([]int64, n)
		for i := range ids {
			ids[i] = e.pad
		}
		for i, enc := range encoded[start:end] {
			copy(ids[i*length:], enc.IDs)
			copy(markers[i*k:], enc.Markers)
			for j := range enc.IDs {
				attention[i*length+j] = 1
			}
			for j := range enc.Markers {
				mask[i*k+j] = true
			}
			qtype[i] = enc.QType
		}
		out, err := e.session.Run(ctx, []onnx.Tensor{
			{Shape: []int64{int64(n), int64(length)}, Int64: ids},
			{Shape: []int64{int64(n), int64(length)}, Int64: attention},
			{Shape: []int64{int64(n), int64(k)}, Int64: markers},
			{Shape: []int64{int64(n), int64(k)}, Bool: mask},
			{Shape: []int64{int64(n)}, Int64: qtype},
		})
		if err != nil {
			return nil, fmt.Errorf("laya rows %d–%d: %w", start, end, err)
		}
		if len(out) != 2 || !slices.Equal(out[0].Shape, []int64{int64(n), int64(k)}) || !slices.Equal(out[1].Shape, []int64{int64(n), 2}) || len(out[0].Float32) != n*k || len(out[1].Float32) != n*2 {
			return nil, errors.New("unexpected Laya output shapes")
		}
		for head, values := range [][]float32{out[0].Float32, out[1].Float32} {
			for _, v := range values {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					return nil, fmt.Errorf("nonfinite laya output head %d", head)
				}
			}
		}
		for i, enc := range encoded[start:end] {
			result, err := probabilities(out[0].Float32[i*k:i*k+len(enc.Markers)], rows[start+i].Type)
			if err != nil {
				return nil, err
			}
			result.ActLogits = slices.Clone(out[1].Float32[i*2 : i*2+2])
			result.Tokens = len(enc.IDs)
			results = append(results, result)
		}
		start = end
	}
	return results, nil
}

func probabilities(logits []float32, kind string) (Result, error) {
	if len(logits) < 2 {
		return Result{}, errors.New("laya returned too few logits")
	}
	r := Result{Logits: logits, Probabilities: make([]float64, len(logits))}
	peak := math.Inf(-1)
	for i, value := range logits {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Result{}, errors.New("laya returned nonfinite logits")
		}
		if v > peak {
			peak = v
			r.Index = i
		}
	}
	total := 0.0
	for i, v := range logits {
		r.Probabilities[i] = math.Exp(float64(v) - peak)
		total += r.Probabilities[i]
	}
	for i := range r.Probabilities {
		r.Probabilities[i] /= total
	}
	if kind == "noul" {
		v := r.Probabilities[1]
		r.Noul = &v
	}
	if kind == "score" {
		v := 0.0
		for i, p := range r.Probabilities {
			v += float64(i) * p
		}
		r.Score = &v
	}
	return r, nil
}
