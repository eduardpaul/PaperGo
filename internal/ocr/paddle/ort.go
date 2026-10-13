package paddle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// This file is a minimal binding of the ONNX Runtime C API through purego,
// so PaperGo builds without CGO and loads the shared library at run time.
// The C API is a table of function pointers that only grows; ortAPIVersion
// pins the table layout, and each index below is the member's position in
// struct OrtApi of onnxruntime_c_api.h (ONNX Runtime 1.23).
const ortAPIVersion = 23

const (
	fnGetErrorMessage                  = 2
	fnCreateEnv                        = 3
	fnCreateSessionFromArray           = 8
	fnRun                              = 9
	fnCreateSessionOptions             = 10
	fnSetSessionExecutionMode          = 13
	fnDisableMemPattern                = 17
	fnDisableCpuMemArena               = 19
	fnSetSessionGraphOptimizationLevel = 23
	fnSetIntraOpNumThreads             = 24
	fnSetInterOpNumThreads             = 25
	fnSessionGetInputCount             = 30
	fnSessionGetOutputCount            = 31
	fnSessionGetInputTypeInfo          = 33
	fnSessionGetOutputTypeInfo         = 34
	fnSessionGetInputName              = 36
	fnSessionGetOutputName             = 37
	fnCreateRunOptions                 = 39
	fnRunOptionsSetTerminate           = 46
	fnCreateTensorAsOrtValue           = 48
	fnGetTensorMutableData             = 51
	fnCastTypeInfoToTensorInfo         = 55
	fnGetDimensionsCount               = 61
	fnGetDimensions                    = 62
	fnGetTensorTypeAndShape            = 65
	fnAllocatorFree                    = 76
	fnGetAllocatorWithDefaultOptions   = 78
	fnReleaseEnv                       = 92
	fnReleaseStatus                    = 93
	fnReleaseSession                   = 95
	fnReleaseValue                     = 96
	fnReleaseRunOptions                = 97
	fnReleaseTypeInfo                  = 98
	fnReleaseTensorTypeAndShapeInfo    = 99
	fnReleaseSessionOptions            = 100
)

const (
	ortLoggingLevelError = 3  // ORT_LOGGING_LEVEL_ERROR
	ortSequential        = 0  // ORT_SEQUENTIAL
	ortEnableAll         = 99 // ORT_ENABLE_ALL
	ortFloat             = 1  // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
)

// ortRuntime is the loaded library and its environment. ONNX Runtime wants
// one environment per process, so every engine shares it.
type ortRuntime struct {
	path      string
	api       unsafe.Pointer
	env       uintptr
	allocator uintptr
}

var (
	runtimeMu     sync.Mutex
	sharedRuntime *ortRuntime
)

// loadRuntime loads the ONNX Runtime library once per process. The library
// cannot be unloaded, so a second path is an error.
func loadRuntime(path string) (*ortRuntime, error) {
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	if sharedRuntime != nil {
		if sharedRuntime.path != path {
			return nil, fmt.Errorf("ONNX Runtime is already loaded from %s", sharedRuntime.path)
		}
		return sharedRuntime, nil
	}
	handle, err := openLibrary(path)
	if err != nil {
		return nil, fmt.Errorf("load ONNX Runtime from %s: %w", path, err)
	}
	getAPIBase, err := librarySymbol(handle, "OrtGetApiBase")
	if err != nil {
		return nil, fmt.Errorf("%s is not ONNX Runtime: %w", path, err)
	}
	base, _, _ := purego.SyscallN(getAPIBase)
	if base == 0 {
		return nil, errors.New("OrtGetApiBase returned nothing")
	}
	// struct OrtApiBase { GetApi; GetVersionString; }
	baseTable := pointer(base)
	api, _, _ := purego.SyscallN(*(*uintptr)(baseTable), ortAPIVersion)
	version, _, _ := purego.SyscallN(*(*uintptr)(unsafe.Add(baseTable, unsafe.Sizeof(uintptr(0)))))
	if api == 0 {
		return nil, fmt.Errorf("ONNX Runtime %s at %s does not provide API version %d; install 1.23 or newer", cString(version), path, ortAPIVersion)
	}
	r := &ortRuntime{path: path, api: pointer(api)}
	logID := []byte("papergo\x00")
	if err = r.call(fnCreateEnv, ortLoggingLevelError, uintptr(unsafe.Pointer(&logID[0])), uintptr(unsafe.Pointer(&r.env))); err != nil {
		return nil, err
	}
	if err = r.call(fnGetAllocatorWithDefaultOptions, uintptr(unsafe.Pointer(&r.allocator))); err != nil {
		r.release(fnReleaseEnv, r.env)
		return nil, err
	}
	sharedRuntime = r
	return r, nil
}

func (r *ortRuntime) fn(index int) uintptr {
	return *(*uintptr)(unsafe.Add(r.api, index*int(unsafe.Sizeof(uintptr(0)))))
}

// call invokes a function that returns an OrtStatus and turns a failure
// into an error. Like purego.SyscallN, it is marked uintptrescapes: a Go
// pointer converted to uintptr in its arguments, such as an out parameter,
// is moved to the heap and kept alive for the call. Without that, the
// goroutine's stack could move while native code writes through it.
//
//go:uintptrescapes
func (r *ortRuntime) call(index int, args ...uintptr) error {
	status, _, _ := purego.SyscallN(r.fn(index), args...)
	if status == 0 {
		return nil
	}
	message, _, _ := purego.SyscallN(r.fn(fnGetErrorMessage), status)
	err := errors.New("onnxruntime: " + cString(message))
	purego.SyscallN(r.fn(fnReleaseStatus), status)
	return err
}

func (r *ortRuntime) release(index int, handle uintptr) {
	if handle != 0 {
		purego.SyscallN(r.fn(index), handle)
	}
}

// session is one loaded model with a single input and a single output.
type session struct {
	rt          *ortRuntime
	ptr         uintptr
	inputName   uintptr // C strings owned by the default allocator, kept for the session's life
	outputName  uintptr
	inputShape  []int64 // -1 for dynamic dimensions
	outputShape []int64
}

// newSession loads a model from its bytes, which works the same on every
// platform (ONNX Runtime takes wide-character paths on Windows). Arena and
// memory-pattern caching are off: image shapes vary between calls, and
// retained buffers would grow a long-lived process.
func (r *ortRuntime) newSession(model []byte, threads int) (s *session, err error) {
	if len(model) == 0 {
		return nil, errors.New("the model is empty")
	}
	var opts uintptr
	if err = r.call(fnCreateSessionOptions, uintptr(unsafe.Pointer(&opts))); err != nil {
		return nil, err
	}
	defer r.release(fnReleaseSessionOptions, opts)
	for _, set := range []struct {
		fn  int
		arg uintptr
	}{{fnSetIntraOpNumThreads, uintptr(threads)}, {fnSetInterOpNumThreads, 1}, {fnSetSessionExecutionMode, ortSequential}, {fnSetSessionGraphOptimizationLevel, ortEnableAll}} {
		if err = r.call(set.fn, opts, set.arg); err != nil {
			return nil, err
		}
	}
	if err = r.call(fnDisableCpuMemArena, opts); err != nil {
		return nil, err
	}
	if err = r.call(fnDisableMemPattern, opts); err != nil {
		return nil, err
	}
	s = &session{rt: r}
	if err = r.call(fnCreateSessionFromArray, r.env, uintptr(unsafe.Pointer(&model[0])), uintptr(len(model)), opts, uintptr(unsafe.Pointer(&s.ptr))); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	var inputs, outputs uintptr
	if err = r.call(fnSessionGetInputCount, s.ptr, uintptr(unsafe.Pointer(&inputs))); err != nil {
		return nil, err
	}
	if err = r.call(fnSessionGetOutputCount, s.ptr, uintptr(unsafe.Pointer(&outputs))); err != nil {
		return nil, err
	}
	if inputs != 1 || outputs != 1 {
		return nil, fmt.Errorf("the model has %d inputs and %d outputs; one of each is expected", inputs, outputs)
	}
	if err = r.call(fnSessionGetInputName, s.ptr, 0, r.allocator, uintptr(unsafe.Pointer(&s.inputName))); err != nil {
		return nil, err
	}
	if err = r.call(fnSessionGetOutputName, s.ptr, 0, r.allocator, uintptr(unsafe.Pointer(&s.outputName))); err != nil {
		return nil, err
	}
	if s.inputShape, err = s.shape(fnSessionGetInputTypeInfo); err != nil {
		return nil, err
	}
	if s.outputShape, err = s.shape(fnSessionGetOutputTypeInfo); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *session) shape(fn int) ([]int64, error) {
	var typeInfo, tensorInfo uintptr
	if err := s.rt.call(fn, s.ptr, 0, uintptr(unsafe.Pointer(&typeInfo))); err != nil {
		return nil, err
	}
	defer s.rt.release(fnReleaseTypeInfo, typeInfo)
	// The tensor info belongs to typeInfo.
	if err := s.rt.call(fnCastTypeInfoToTensorInfo, typeInfo, uintptr(unsafe.Pointer(&tensorInfo))); err != nil {
		return nil, err
	}
	if tensorInfo == 0 {
		return nil, errors.New("the model's input or output is not a tensor")
	}
	return s.rt.dimensions(tensorInfo)
}

func (r *ortRuntime) dimensions(info uintptr) ([]int64, error) {
	var n uintptr
	if err := r.call(fnGetDimensionsCount, info, uintptr(unsafe.Pointer(&n))); err != nil {
		return nil, err
	}
	dims := make([]int64, n)
	if n == 0 {
		return dims, nil
	}
	if err := r.call(fnGetDimensions, info, uintptr(unsafe.Pointer(&dims[0])), n); err != nil {
		return nil, err
	}
	return dims, nil
}

// run executes the model on one float tensor and returns the output tensor
// and its shape. A cancelled context terminates the inference.
func (s *session) run(ctx context.Context, input []float32, shape []int64) ([]float32, []int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	r := s.rt
	elements := int64(1)
	for _, d := range shape {
		elements *= d
	}
	if elements != int64(len(input)) || elements == 0 {
		return nil, nil, fmt.Errorf("tensor shape %v does not hold %d values", shape, len(input))
	}
	// The input lives in ONNX Runtime's memory, so native code never keeps
	// a pointer into the Go heap.
	var value uintptr
	if err := r.call(fnCreateTensorAsOrtValue, r.allocator, uintptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), ortFloat, uintptr(unsafe.Pointer(&value))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseValue, value)
	var data uintptr
	if err := r.call(fnGetTensorMutableData, value, uintptr(unsafe.Pointer(&data))); err != nil {
		return nil, nil, err
	}
	copy(unsafe.Slice((*float32)(pointer(data)), len(input)), input)

	var options uintptr
	if err := r.call(fnCreateRunOptions, uintptr(unsafe.Pointer(&options))); err != nil {
		return nil, nil, err
	}
	defer r.release(fnReleaseRunOptions, options)
	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Go(func() {
		select {
		case <-ctx.Done():
			purego.SyscallN(r.fn(fnRunOptionsSetTerminate), options)
		case <-done:
		}
	})
	var output uintptr
	err := r.call(fnRun, s.ptr, options, uintptr(unsafe.Pointer(&s.inputName)), uintptr(unsafe.Pointer(&value)), 1,
		uintptr(unsafe.Pointer(&s.outputName)), 1, uintptr(unsafe.Pointer(&output)))
	close(done)
	watcher.Wait()
	defer r.release(fnReleaseValue, output)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, err
	}
	var info uintptr
	if err = r.call(fnGetTensorTypeAndShape, output, uintptr(unsafe.Pointer(&info))); err != nil {
		return nil, nil, err
	}
	dims, err := r.dimensions(info)
	r.release(fnReleaseTensorTypeAndShapeInfo, info)
	if err != nil {
		return nil, nil, err
	}
	count := int64(1)
	for _, d := range dims {
		count *= d
	}
	out := make([]float32, count)
	if count > 0 {
		if err = r.call(fnGetTensorMutableData, output, uintptr(unsafe.Pointer(&data))); err != nil {
			return nil, nil, err
		}
		copy(out, unsafe.Slice((*float32)(pointer(data)), count))
	}
	return out, dims, nil
}

func (s *session) close() {
	for _, name := range []uintptr{s.inputName, s.outputName} {
		if name != 0 {
			_ = s.rt.call(fnAllocatorFree, s.rt.allocator, name)
		}
	}
	s.inputName, s.outputName = 0, 0
	s.rt.release(fnReleaseSession, s.ptr)
	s.ptr = 0
}

// pointer turns an address returned by native code into a pointer without
// a uintptr-to-pointer conversion that vet would flag.
func pointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

// cString copies a NUL-terminated C string.
func cString(address uintptr) string {
	if address == 0 {
		return ""
	}
	p := pointer(address)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
