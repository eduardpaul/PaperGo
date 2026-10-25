package script

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"papergo/internal/dal/runs"
	"papergo/sdk"
)

// apiOrigin is the origin the SDK's request adapter puts in front of every
// path (sdk/src/adapter.ts).
const apiOrigin = "http://papergo.workflow"

// host is the state of one execution slice: the run's recorded steps, the
// position the script has reached in them, and the value steps observed
// since the last durable step.
type host struct {
	ctx      context.Context
	run      Run
	effects  Effects
	rt       *goja.Runtime
	watch    *watchdog
	recorded []runs.Step
	next     int
	unsaved  []runs.Step
	// unsavedBytes is the size of the unsaved steps' outputs.
	unsavedBytes int
	// journal is the size of the outputs the run recorded, replayed ones
	// included.
	journal int
	logs    []Log
	// suspended is set when the run waits for operations or sleeps, until
	// until; abort when the slice must stop for another reason. Either
	// interrupts the runtime, which JavaScript cannot catch.
	suspended bool
	until     *time.Time
	abort     error
	// manifest is set while a version's definition is read: the script
	// makes no effects then.
	manifest bool
}

// runtime returns a runtime with the globals scripts may use: ECMAScript,
// the SDK's host functions, TextEncoder and TextDecoder. The SDK adds
// console. There is no require, process, timer or network access.
func (h *host) runtime() *goja.Runtime {
	rt := goja.New()
	h.rt = rt
	rt.SetTimeSource(h.now)
	rt.SetRandSource(h.random)
	rt.SetMaxCallStackSize(maxCallStack)
	h.textCodecs()

	papergo := rt.NewObject()
	_ = papergo.Set("request", h.request)
	_ = papergo.Set("wait", h.wait)
	_ = papergo.Set("sleep", h.sleep)
	_ = papergo.Set("uuid", h.uuid)
	_ = papergo.Set("log", func(level, message string) { h.log(level, message) })
	_ = rt.Set("__papergo", papergo)
	return rt
}

// log keeps entries written after the replay caught up with the journal;
// earlier ones were written by the slice that recorded those steps.
func (h *host) log(level, message string) {
	if h.next < len(h.recorded) {
		return
	}
	switch level {
	case "info", "warn", "error":
	default:
		level = "info"
	}
	h.logs = append(h.logs, Log{Level: level, Message: message})
}

// stop interrupts the runtime: the slice ends without running more
// JavaScript, whatever the script catches.
func (h *host) stop(err error) goja.Value {
	if err == errSuspended {
		h.suspended = true
	} else if h.abort == nil {
		h.abort = err
	}
	h.rt.Interrupt(err)
	return goja.Undefined()
}

func digest(parts ...string) string {
	sum := sha256.New()
	for _, part := range parts {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// replay returns the step recorded at the script's position, or nil past
// the end of the journal. A step recorded for something else is a replay
// that diverged from the run it continues.
func (h *host) replay(kind runs.StepKind, name, hash string) (*runs.Step, error) {
	if h.next >= len(h.recorded) {
		return nil, nil
	}
	s := &h.recorded[h.next]
	if s.Kind != kind || s.Name != name || s.InputHash != hash {
		return nil, fmt.Errorf("%w: step %d was recorded as %s %q, the script now asks for %s %q", ErrNondeterministic, s.Seq, s.Kind, s.Name, kind, name)
	}
	return s, nil
}

func (h *host) newStep(kind runs.StepKind, name, hash string) runs.Step {
	return runs.Step{RunID: h.run.ID, Seq: h.next, Kind: kind, Name: name, InputHash: hash, Status: runs.StepCompleted, StartedAt: time.Now().UTC()}
}

// value replays or records a value step: a clock reading or random value.
func (h *host) value(name string, generate func() json.RawMessage) (json.RawMessage, bool) {
	if h.abort != nil || h.suspended {
		return nil, false
	}
	hash := digest(string(runs.StepValue), name)
	s, err := h.replay(runs.StepValue, name, hash)
	if err != nil {
		h.stop(err)
		return nil, false
	}
	h.next++
	if s != nil {
		return s.Output, true
	}
	step := h.newStep(runs.StepValue, name, hash)
	step.Seq = h.next - 1
	step.Output = generate()
	if !h.grow(step.Output) {
		return nil, false
	}
	h.observe(step)
	return step.Output, true
}

// Unsaved steps are recorded once they hold this many steps or bytes of
// output, so a long run of reads does not keep its journal in memory.
const (
	maxUnsavedSteps = 256
	maxUnsavedBytes = 4 << 20
)

// observe keeps a value or read step, which completed now, until the next
// durable step records it, recording it early when too much is kept.
func (h *host) observe(step runs.Step) {
	completed := time.Now().UTC()
	step.CompletedAt = &completed
	h.unsaved = append(h.unsaved, step)
	h.unsavedBytes += len(step.Output)
	if h.manifest || len(h.unsaved) < maxUnsavedSteps && h.unsavedBytes < maxUnsavedBytes {
		return
	}
	h.watch.pause()
	err := h.effects.Record(h.ctx, h.run, h.unsaved)
	h.watch.resume()
	if err != nil {
		h.stop(err)
		return
	}
	h.saved()
}

// grow counts a new step's output toward the run's journal and stops a run
// whose journal outgrew maxJournal. It reports whether the run goes on.
func (h *host) grow(output []byte) bool {
	h.journal += len(output)
	if h.journal <= maxJournal {
		return true
	}
	h.stop(errJournal)
	return false
}

// saved forgets the unsaved steps once a durable step recorded them.
func (h *host) saved() { h.unsaved, h.unsavedBytes = nil, 0 }

func (h *host) now() time.Time {
	out, ok := h.value("now", func() json.RawMessage {
		return json.RawMessage(strconv.FormatInt(time.Now().UnixNano(), 10))
	})
	if !ok {
		return time.Unix(0, 0)
	}
	nanos, _ := strconv.ParseInt(string(out), 10, 64)
	return time.Unix(0, nanos)
}

func (h *host) random() float64 {
	out, ok := h.value("random", func() json.RawMessage {
		var b [8]byte
		_, _ = rand.Read(b[:])
		v := float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
		return json.RawMessage(strconv.FormatFloat(v, 'g', -1, 64))
	})
	if !ok {
		return 0
	}
	v, _ := strconv.ParseFloat(string(out), 64)
	return v
}

func (h *host) uuid() goja.Value {
	out, ok := h.value("uuid", func() json.RawMessage {
		b, _ := json.Marshal(uuid.NewString())
		return b
	})
	if !ok {
		return goja.Undefined()
	}
	var id string
	_ = json.Unmarshal(out, &id)
	return h.rt.ToValue(id)
}

// hostResponse is a request step's recorded output, as the SDK's adapter
// reads it.
type hostResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Base64  bool              `json:"base64"`
}

// ResponseOutput is the recorded output of a request step that got
// response. Effects store it as the step's output.
func ResponseOutput(response Response) json.RawMessage {
	out := hostResponse{Status: response.Status, Headers: map[string]string{}}
	for name, value := range response.Header {
		out.Headers[strings.ToLower(name)] = value
	}
	if utf8.Valid(response.Body) {
		out.Body = string(response.Body)
	} else {
		out.Body, out.Base64 = base64.StdEncoding.EncodeToString(response.Body), true
	}
	b, _ := json.Marshal(out)
	return b
}

// request runs an API request through the SDK's adapter. A text body is a
// string and a binary one an ArrayBuffer; a response's binary body comes back
// as the ArrayBuffer bytes.
func (h *host) request(method, rawURL string, headers map[string]string, body goja.Value) goja.Value {
	if h.abort != nil || h.suspended {
		return goja.Undefined()
	}
	h.effect()
	method = strings.ToUpper(method)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme+"://"+u.Host != apiOrigin || u.User != nil || u.Fragment != "" {
		panic(h.rt.NewTypeError("workflow requests go to the PaperGo API, not %q", rawURL))
	}
	if sdk.Excluded(u.EscapedPath()) {
		panic(h.rt.NewTypeError("workflow scripts cannot call %s %s", method, u.Path))
	}
	var content []byte
	if body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		if buf, ok := body.Export().(goja.ArrayBuffer); ok {
			content = append([]byte(nil), buf.Bytes()...)
		} else {
			content = []byte(body.String())
		}
	}
	target := u.RequestURI()
	name := method + " " + u.Path
	if len(name) > 300 {
		name = name[:300]
	}
	hash := digest(string(runs.StepRequest), method, target, string(content))
	recorded, err := h.replay(runs.StepRequest, name, hash)
	if err != nil {
		return h.stop(err)
	}
	var output json.RawMessage
	switch {
	case recorded != nil:
		output = recorded.Output
	case method == http.MethodGet || method == http.MethodHead:
		// A read changes nothing, so it needs no transaction of its own:
		// its step is recorded with the next durable one.
		h.watch.pause()
		response, err := h.effects.Read(h.ctx, h.run, Request{Method: method, URL: target, Header: headers})
		h.watch.resume()
		if err != nil {
			return h.stop(err)
		}
		step := h.newStep(runs.StepRequest, name, hash)
		step.Output = ResponseOutput(response)
		output = step.Output
		if !h.grow(output) {
			return goja.Undefined()
		}
		if h.observe(step); h.abort != nil {
			return goja.Undefined()
		}
	default:
		step := h.newStep(runs.StepRequest, name, hash)
		h.watch.pause()
		response, err := h.effects.Request(h.ctx, h.run, h.unsaved, step, Request{Method: method, URL: target, Header: headers, Body: content})
		h.watch.resume()
		if err != nil {
			return h.stop(err)
		}
		h.saved()
		output = ResponseOutput(response)
		// The request is recorded; a journal it outgrew ends the run here.
		if !h.grow(output) {
			return goja.Undefined()
		}
	}
	h.next++
	var out hostResponse
	if err := json.Unmarshal(output, &out); err != nil {
		return h.stop(fmt.Errorf("step %d of %s: %w", h.next-1, h.run.ID, err))
	}
	obj := h.rt.NewObject()
	_ = obj.Set("status", out.Status)
	headerObject := h.rt.NewObject()
	for name, value := range out.Headers {
		_ = headerObject.Set(name, value)
	}
	_ = obj.Set("headers", headerObject)
	if out.Base64 {
		data, err := base64.StdEncoding.DecodeString(out.Body)
		if err != nil {
			return h.stop(fmt.Errorf("step %d of %s: %w", h.next-1, h.run.ID, err))
		}
		_ = obj.Set("body", "")
		_ = obj.Set("bytes", h.rt.NewArrayBuffer(data))
	} else {
		_ = obj.Set("body", out.Body)
	}
	return obj
}

// wait replays a recorded wait or records one and returns its operations
// as they ended, as JSON text. A run whose operations have not all finished
// suspends; a wait that failed, such as by timing out, throws an error the
// script can catch.
func (h *host) wait(ids []string) goja.Value {
	if h.abort != nil || h.suspended {
		return goja.Undefined()
	}
	h.effect()
	if len(ids) == 0 || len(ids) > maxWaits {
		panic(h.rt.NewTypeError("ctx.wait takes 1 to %d operations", maxWaits))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 64 || seen[id] {
			panic(h.rt.NewTypeError("ctx.wait takes distinct operation IDs"))
		}
		seen[id] = true
	}
	hash := digest(append([]string{string(runs.StepWait)}, ids...)...)
	recorded, err := h.replay(runs.StepWait, "operations", hash)
	if err != nil {
		return h.stop(err)
	}
	if recorded == nil {
		step := h.newStep(runs.StepWait, "operations", hash)
		step.Status, step.Operations = runs.StepPending, ids
		step.Input, _ = json.Marshal(map[string][]string{"operations": ids})
		h.watch.pause()
		output, done, err := h.effects.Wait(h.ctx, h.run, h.unsaved, step)
		h.watch.resume()
		if errors.Is(err, ErrUnknownOperation) {
			panic(h.rt.NewTypeError("ctx.wait: %v", err))
		}
		if err != nil {
			return h.stop(err)
		}
		h.saved()
		h.next++
		if !done {
			return h.stop(errSuspended)
		}
		if !h.grow(output) {
			return goja.Undefined()
		}
		return h.rt.ToValue(string(output))
	}
	h.next++
	switch recorded.Status {
	case runs.StepCompleted:
		return h.rt.ToValue(string(recorded.Output))
	case runs.StepFailed:
		panic(h.rt.NewGoError(errors.New(recorded.Error)))
	default:
		return h.stop(errSuspended)
	}
}

// effect refuses effects while a definition is read.
func (h *host) effect() {
	if h.manifest {
		panic(h.rt.NewTypeError("a workflow definition is read without running it: requests, waits and sleeps belong in run"))
	}
}

// sleep replays or records a sleep until a time in milliseconds since the
// epoch. A sleep whose time has not come suspends the run until then.
func (h *host) sleep(untilMillis int64) goja.Value {
	if h.abort != nil || h.suspended {
		return goja.Undefined()
	}
	h.effect()
	until := time.UnixMilli(untilMillis).UTC()
	stamp := until.Format(time.RFC3339Nano)
	hash := digest(string(runs.StepWait), "sleep", stamp)
	recorded, err := h.replay(runs.StepWait, "sleep", hash)
	if err != nil {
		return h.stop(err)
	}
	h.watch.pause()
	defer h.watch.resume()
	due := !time.Now().Before(until)
	if recorded == nil {
		step := h.newStep(runs.StepWait, "sleep", hash)
		step.Input, _ = json.Marshal(map[string]string{"until": stamp})
		if !due {
			step.Status = runs.StepPending
		}
		if err = h.effects.Sleep(h.ctx, h.run, h.unsaved, step); err != nil {
			return h.stop(err)
		}
		h.saved()
	} else if recorded.Status == runs.StepPending && due {
		if err = h.effects.Wake(h.ctx, h.run, recorded.Seq); err != nil {
			return h.stop(err)
		}
	}
	h.next++
	if due || recorded != nil && recorded.Status == runs.StepCompleted {
		return goja.Undefined()
	}
	h.until = &until
	return h.stop(errSuspended)
}

// textCodecs defines UTF-8 TextEncoder and TextDecoder.
func (h *host) textCodecs() {
	rt := h.rt
	uint8Array, _ := goja.AssertConstructor(rt.Get("Uint8Array"))
	isView, _ := goja.AssertFunction(rt.Get("ArrayBuffer").ToObject(rt).Get("isView"))
	_ = rt.Set("TextEncoder", func(call goja.ConstructorCall) *goja.Object {
		_ = call.This.Set("encoding", "utf-8")
		_ = call.This.Set("encode", func(s goja.Value) goja.Value {
			text := ""
			if s != nil && !goja.IsUndefined(s) {
				text = s.String()
			}
			view, err := uint8Array(nil, rt.ToValue(rt.NewArrayBuffer([]byte(text))))
			if err != nil {
				panic(err)
			}
			return view
		})
		return nil
	})
	_ = rt.Set("TextDecoder", func(call goja.ConstructorCall) *goja.Object {
		_ = call.This.Set("encoding", "utf-8")
		_ = call.This.Set("decode", func(v goja.Value) string {
			b := bytesOf(rt, v, isView)
			return strings.ToValidUTF8(string(b), "�")
		})
		return nil
	})
}

// bytesOf returns the bytes of an ArrayBuffer or of a view of one.
func bytesOf(rt *goja.Runtime, v goja.Value, isView goja.Callable) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	if v.ExportType() == reflect.TypeFor[goja.ArrayBuffer]() {
		return v.Export().(goja.ArrayBuffer).Bytes()
	}
	view, err := isView(goja.Undefined(), v)
	if err != nil {
		panic(err)
	}
	if !view.ToBoolean() {
		panic(rt.NewTypeError("TextDecoder.decode needs an ArrayBuffer or a view of one"))
	}
	obj := v.ToObject(rt)
	buffer := obj.Get("buffer")
	if buffer == nil || buffer.ExportType() != reflect.TypeFor[goja.ArrayBuffer]() {
		panic(rt.NewTypeError("TextDecoder.decode needs a view with an ArrayBuffer"))
	}
	buf := buffer.Export().(goja.ArrayBuffer)
	offsetValue, lengthValue := obj.Get("byteOffset"), obj.Get("byteLength")
	if offsetValue == nil || lengthValue == nil || goja.IsUndefined(offsetValue) || goja.IsUndefined(lengthValue) {
		panic(rt.NewTypeError("TextDecoder.decode needs a view with byteOffset and byteLength"))
	}
	offset, length := offsetValue.ToInteger(), lengthValue.ToInteger()
	data := buf.Bytes()
	if offset < 0 || length < 0 || offset > int64(len(data)) || length > int64(len(data))-offset {
		panic(rt.NewTypeError("TextDecoder.decode view is outside its ArrayBuffer"))
	}
	return data[offset : offset+length]
}
