// Package onnx shares a cgo ONNX Runtime environment between local models.
package onnx

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Runtime is the process-wide ONNX Runtime environment.
type Runtime struct{ path string }

var (
	runtimeMu     sync.Mutex
	sharedRuntime *Runtime
)

// Load initializes the environment once. All engines must use the same library.
func Load(path string) (*Runtime, error) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if sharedRuntime != nil {
		if sharedRuntime.path != path {
			return nil, fmt.Errorf("ONNX Runtime is already loaded from %s", sharedRuntime.path)
		}
		return sharedRuntime, nil
	}
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, errors.New("invalid ONNX Runtime library path")
	}
	ort.SetSharedLibraryPath(path)
	if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		return nil, fmt.Errorf("load ONNX Runtime from %s: %w", path, err)
	}
	sharedRuntime = &Runtime{path: path}
	return sharedRuntime, nil
}

// Session owns a model and its metadata. Callers serialize inference and Close.
type Session struct {
	session                   *ort.DynamicAdvancedSession
	Inputs, Outputs           []string
	InputShapes, OutputShapes [][]int64
	InputTypes, OutputTypes   []string
}

// Tensor is a float32, int64 or bool tensor in model input/output order.
type Tensor struct {
	Shape   []int64
	Float32 []float32
	Int64   []int64
	Bool    []bool
}

// NewSession loads a self-contained model from bytes.
func (r *Runtime) NewSession(model []byte, threads int) (*Session, error) {
	if len(model) == 0 {
		return nil, errors.New("the model is empty")
	}
	return newSession(threads, func(opts *ort.SessionOptions) ([]ort.InputOutputInfo, []ort.InputOutputInfo, error) {
		return ort.GetInputOutputInfoWithONNXData(model)
	}, func(inputs, outputs []string, opts *ort.SessionOptions) (*ort.DynamicAdvancedSession, error) {
		return ort.NewDynamicAdvancedSessionWithONNXData(model, inputs, outputs, opts)
	})
}

// OpenSession loads a model by path, resolving external weights beside it.
func (r *Runtime) OpenSession(path string, threads int) (*Session, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, errors.New("invalid model path")
	}
	return newSession(threads, func(opts *ort.SessionOptions) ([]ort.InputOutputInfo, []ort.InputOutputInfo, error) {
		return ort.GetInputOutputInfoWithOptions(path, opts)
	}, func(inputs, outputs []string, opts *ort.SessionOptions) (*ort.DynamicAdvancedSession, error) {
		return ort.NewDynamicAdvancedSession(path, inputs, outputs, opts)
	})
}

func newSession(threads int,
	metadata func(*ort.SessionOptions) ([]ort.InputOutputInfo, []ort.InputOutputInfo, error),
	create func([]string, []string, *ort.SessionOptions) (*ort.DynamicAdvancedSession, error),
) (*Session, error) {
	if threads < 1 || threads > 32 {
		return nil, errors.New("ONNX threads must be between 1 and 32")
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	for _, configure := range []func() error{
		func() error { return opts.SetIntraOpNumThreads(threads) },
		func() error { return opts.SetInterOpNumThreads(1) },
		func() error { return opts.SetExecutionMode(ort.ExecutionModeSequential) },
		func() error { return opts.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll) },
		func() error { return opts.SetCpuMemArena(false) },
		func() error { return opts.SetMemPattern(false) },
	} {
		if err = configure(); err != nil {
			return nil, err
		}
	}
	inputs, outputs, err := metadata(opts)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 || len(inputs) > 64 || len(outputs) == 0 || len(outputs) > 64 {
		return nil, errors.New("unsupported number of model inputs or outputs")
	}
	s := &Session{}
	for _, input := range inputs {
		s.Inputs = append(s.Inputs, input.Name)
		s.InputTypes = append(s.InputTypes, tensorType(input.DataType))
		s.InputShapes = append(s.InputShapes, slices.Clone(input.Dimensions))
	}
	for _, output := range outputs {
		s.Outputs = append(s.Outputs, output.Name)
		s.OutputTypes = append(s.OutputTypes, tensorType(output.DataType))
		s.OutputShapes = append(s.OutputShapes, slices.Clone(output.Dimensions))
	}
	s.session, err = create(s.Inputs, s.Outputs, opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func elements(shape []int64) (int, error) {
	count := 1
	for _, d := range shape {
		if d < 0 || (d > 0 && int64(count) > int64(int(^uint(0)>>1))/d) {
			return 0, fmt.Errorf("invalid tensor shape %v", shape)
		}
		count *= int(d)
	}
	return count, nil
}

// RunFloat executes the single float input/output OCR models.
func (s *Session) RunFloat(ctx context.Context, input []float32, shape []int64) ([]float32, []int64, error) {
	if len(s.Inputs) != 1 || len(s.Outputs) != 1 {
		return nil, nil, errors.New("expected one model input and output")
	}
	out, err := s.Run(ctx, []Tensor{{Shape: shape, Float32: input}})
	if err != nil {
		return nil, nil, err
	}
	return out[0].Float32, out[0].Shape, nil
}

// Run executes all model inputs. Cancellation terminates this run only.
func (s *Session) Run(ctx context.Context, inputs []Tensor) ([]Tensor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.session == nil {
		return nil, errors.New("ONNX session is closed")
	}
	if len(inputs) != len(s.Inputs) {
		return nil, errors.New("incorrect model input count")
	}
	// The SDK keeps input slices as tensor storage across cgo calls. Pin them
	// until every native tensor has been destroyed.
	var pinned runtime.Pinner
	defer pinned.Unpin()
	values := make([]ort.Value, len(inputs))
	outputs := make([]ort.Value, len(s.Outputs))
	defer func() {
		for _, group := range [][]ort.Value{values, outputs} {
			for _, value := range group {
				if value != nil {
					_ = value.Destroy()
				}
			}
		}
	}()
	for i, input := range inputs {
		n, err := elements(input.Shape)
		if err != nil {
			return nil, err
		}
		size, types := len(input.Float32), 0
		if input.Float32 != nil {
			types++
		}
		if input.Int64 != nil {
			size = len(input.Int64)
			types++
		}
		if input.Bool != nil {
			size = len(input.Bool)
			types++
		}
		if types > 1 {
			return nil, errors.New("tensor has multiple data types")
		}
		if n != size {
			return nil, fmt.Errorf("tensor shape %v does not hold %d values", input.Shape, size)
		}
		if input.Int64 != nil {
			if n > 0 {
				pinned.Pin(&input.Int64[0])
			}
			values[i], err = ort.NewTensor(ort.NewShape(input.Shape...), input.Int64)
		} else if input.Bool != nil {
			if n > 0 {
				pinned.Pin(&input.Bool[0])
			}
			values[i], err = ort.NewTensor(ort.NewShape(input.Shape...), input.Bool)
		} else {
			if n > 0 {
				pinned.Pin(&input.Float32[0])
			}
			values[i], err = ort.NewTensor(ort.NewShape(input.Shape...), input.Float32)
		}
		if err != nil {
			return nil, err
		}
	}
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Go(func() {
		select {
		case <-ctx.Done():
			_ = options.Terminate()
		case <-done:
		}
	})
	err = s.session.RunWithOptions(values, outputs, options)
	close(done)
	watcher.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	result := make([]Tensor, len(outputs))
	for i, output := range outputs {
		tensor, ok := output.(*ort.Tensor[float32])
		if !ok {
			return nil, fmt.Errorf("unsupported output tensor type %T", output)
		}
		result[i] = Tensor{Shape: slices.Clone([]int64(tensor.GetShape())), Float32: slices.Clone(tensor.GetData())}
	}
	return result, nil
}

// Close frees model memory. It is safe to call repeatedly.
func (s *Session) Close() {
	if s.session != nil {
		_ = s.session.Destroy()
		s.session = nil
	}
}

func tensorType(t ort.TensorElementDataType) string {
	switch t {
	case ort.TensorElementDataTypeInt64:
		return "int64"
	case ort.TensorElementDataTypeBool:
		return "bool"
	case ort.TensorElementDataTypeFloat:
		return "float32"
	default:
		return t.String()
	}
}
