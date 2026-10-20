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
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"
	nodeurl "github.com/dop251/goja_nodejs/url"
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
	logs     []Log
	// suspended is set when the run waits for an action; abort when the
	// slice must stop for another reason. Either interrupts the runtime,
	// which JavaScript cannot catch.
	suspended bool
	abort     error
}

// runtime returns a runtime with the globals scripts may use: ECMAScript,
// the SDK's host functions, Buffer, URL, TextEncoder, TextDecoder, console
// and an empty process. require() resolves only the SDK; it reads no files.
func (h *host) runtime() *goja.Runtime {
	rt := goja.New()
	h.rt = rt
	rt.SetTimeSource(h.now)
	rt.SetRandSource(h.random)
	rt.SetMaxCallStackSize(maxCallStack)

	registry := require.NewRegistry(require.WithLoader(func(string) ([]byte, error) {
		return nil, require.ModuleFileDoesNotExistError
	}))
	registry.RegisterNativeModule(SDKModule, func(rt *goja.Runtime, module *goja.Object) {
		_ = module.Set("exports", rt.Get("__papergoSdk"))
	})
	// goja_nodejs's own process module exposes the server's environment.
	registry.RegisterNativeModule("process", func(rt *goja.Runtime, module *goja.Object) {
		process := rt.NewObject()
		_ = process.Set("env", rt.NewObject())
		_ = module.Set("exports", process)
	})
	registry.RegisterNativeModule(console.ModuleName, console.RequireWithPrinter(printer{h}))
	registry.Enable(rt)
	buffer.Enable(rt)
	nodeurl.Enable(rt)
	console.Enable(rt)
	_ = rt.Set("process", require.Require(rt, "process"))
	h.textCodecs()

	papergo := rt.NewObject()
	_ = papergo.Set("request", h.request)
	_ = papergo.Set("action", h.action)
	_ = papergo.Set("uuid", h.uuid)
	_ = papergo.Set("log", func(level, message string) { h.log(level, message) })
	_ = rt.Set("__papergo", papergo)
	return rt
}

type printer struct{ h *host }

func (p printer) Log(s string)   { p.h.log("info", s) }
func (p printer) Warn(s string)  { p.h.log("warn", s) }
func (p printer) Error(s string) { p.h.log("error", s) }

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
	return runs.Step{RunID: h.run.ID, Seq: h.next, Kind: kind, Name: name, InputHash: hash, Status: runs.StepCompleted}
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
	h.unsaved = append(h.unsaved, step)
	return step.Output, true
}

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

// request runs an API request through the SDK's adapter.
func (h *host) request(method, rawURL string, headers map[string]string, body goja.Value, encoded bool) goja.Value {
	if h.abort != nil || h.suspended {
		return goja.Undefined()
	}
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
		if encoded {
			if content, err = base64.StdEncoding.DecodeString(body.String()); err != nil {
				panic(h.rt.NewTypeError("the request body is not base64"))
			}
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
	if recorded != nil {
		output = recorded.Output
	} else {
		step := h.newStep(runs.StepRequest, name, hash)
		h.watch.pause()
		response, err := h.effects.Request(h.ctx, h.unsaved, step, Request{Method: method, URL: target, Header: headers, Body: content})
		h.watch.resume()
		if err != nil {
			return h.stop(err)
		}
		h.unsaved = nil
		output = ResponseOutput(response)
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
	_ = obj.Set("body", out.Body)
	_ = obj.Set("base64", out.Base64)
	return obj
}

// action replays a platform action's recorded result or starts it. A run
// whose action has not completed suspends; a failed action throws an error
// the script can catch.
func (h *host) action(name, input string) goja.Value {
	if h.abort != nil || h.suspended {
		return goja.Undefined()
	}
	if name == "" || len(name) > 100 {
		panic(h.rt.NewTypeError("an action name has 1 to 100 characters"))
	}
	if len(input) > maxActionInput || !json.Valid([]byte(input)) {
		panic(h.rt.NewTypeError("action input must be JSON of at most %d bytes", maxActionInput))
	}
	hash := digest(string(runs.StepAction), name, input)
	recorded, err := h.replay(runs.StepAction, name, hash)
	if err != nil {
		return h.stop(err)
	}
	if recorded == nil {
		step := h.newStep(runs.StepAction, name, hash)
		step.Status = runs.StepPending
		step.Input = json.RawMessage(input)
		h.watch.pause()
		err := h.effects.Start(h.ctx, h.unsaved, step)
		h.watch.resume()
		if err != nil {
			return h.stop(err)
		}
		h.unsaved = nil
		h.next++
		return h.stop(errSuspended)
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

// textCodecs defines UTF-8 TextEncoder and TextDecoder.
func (h *host) textCodecs() {
	rt := h.rt
	uint8Array, _ := goja.AssertConstructor(rt.Get("Uint8Array"))
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
			b := bytesOf(rt, v)
			return strings.ToValidUTF8(string(b), "�")
		})
		return nil
	})
}

// bytesOf returns the bytes of an ArrayBuffer or of a view of one.
func bytesOf(rt *goja.Runtime, v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	if buf, ok := v.Export().(goja.ArrayBuffer); ok {
		return buf.Bytes()
	}
	obj := v.ToObject(rt)
	buf, ok := obj.Get("buffer").Export().(goja.ArrayBuffer)
	if !ok {
		panic(rt.NewTypeError("TextDecoder.decode needs an ArrayBuffer or a view of one"))
	}
	offset, length := obj.Get("byteOffset").ToInteger(), obj.Get("byteLength").ToInteger()
	return buf.Bytes()[offset : offset+length]
}
