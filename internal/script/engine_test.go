package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal/runs"
)

// memory is an in-memory journal with operations. Requests a test does not
// answer start bulk updates and read operations, like the API.
type memory struct {
	mu         sync.Mutex
	steps      map[string][]runs.Step
	requests   []Request
	waits      int
	operations map[string]string
	answer     func(Request) Response
}

func newMemory(answer func(Request) Response) *memory {
	return &memory{steps: map[string][]runs.Step{}, operations: map[string]string{}, answer: answer}
}

func (m *memory) Steps(_ context.Context, run Run) ([]runs.Step, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]runs.Step(nil), m.steps[run.ID]...), nil
}

func (m *memory) record(steps ...runs.Step) {
	for _, s := range steps {
		if s.Seq != len(m.steps[s.RunID]) {
			panic("steps recorded out of order")
		}
		m.steps[s.RunID] = append(m.steps[s.RunID], s)
	}
}

func (m *memory) Request(_ context.Context, _ Run, unsaved []runs.Step, step runs.Step, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	var response Response
	switch {
	case req.Method == "POST" && strings.HasSuffix(req.URL, "/bulk-update"):
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(m.operations)+1)
		m.operations[id] = "pending"
		response = jsonResponse(202, `{"id":"`+id+`","status":"pending"}`)
	case req.Method == "GET" && strings.HasPrefix(req.URL, "/v1/operations/"):
		id := strings.TrimPrefix(req.URL, "/v1/operations/")
		status := m.operations[id]
		body := `{"id":"` + id + `","status":"` + status + `","result":{"updated":2,"ids":["a","b"]}}`
		if status == "failed" {
			body = `{"id":"` + id + `","status":"failed","error":"the list is locked"}`
		}
		response = jsonResponse(200, body)
	default:
		response = m.answer(req)
	}
	step.Output = ResponseOutput(response)
	m.record(append(unsaved, step)...)
	return response, nil
}

func (m *memory) over(step runs.Step) bool {
	for _, id := range step.Operations {
		if m.operations[id] == "pending" {
			return false
		}
	}
	return true
}

func (m *memory) Wait(_ context.Context, _ Run, unsaved []runs.Step, step runs.Step) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range step.Operations {
		if _, ok := m.operations[id]; !ok {
			return false, ErrUnknownOperation
		}
	}
	m.waits++
	done := m.over(step)
	if done {
		step.Status = runs.StepCompleted
	}
	m.record(append(unsaved, step)...)
	return done, nil
}

// finish ends an operation and releases the waits it completes, as the
// operations repository does.
func (m *memory) finish(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operations[id] = status
	for _, steps := range m.steps {
		for i := range steps {
			if steps[i].Kind == runs.StepWait && steps[i].Status == runs.StepPending && m.over(steps[i]) {
				steps[i].Status = runs.StepCompleted
			}
		}
	}
}

func bundle(t *testing.T, source string) Artifact {
	t.Helper()
	a, err := Bundle("workflow.ts", source)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func execute(t *testing.T, e *Engine, run Run) Outcome {
	t.Helper()
	out, err := e.Execute(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonResponse(status int, body string) Response {
	return Response{Status: status, Header: map[string]string{"Content-Type": "application/json"}, Body: []byte(body)}
}

func TestRequestsReplayWithoutRepeating(t *testing.T) {
	m := newMemory(func(req Request) Response {
		switch {
		case req.Method == "POST" && req.URL == "/v1/resources/0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01/children":
			var body map[string]any
			if err := json.Unmarshal(req.Body, &body); err != nil || body["name"] != "Invoice 7" || body["kind"] != "item" {
				return jsonResponse(422, `{"code":"invalid","detail":"unexpected body"}`)
			}
			return jsonResponse(201, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","kind":"item","name":"Invoice 7"}`)
		case req.Method == "GET" && strings.HasPrefix(req.URL, "/v1/resources/5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11"):
			return jsonResponse(200, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","kind":"item","name":"Invoice 7","version":1}`)
		}
		return jsonResponse(404, `{"code":"not_found","detail":"route not found"}`)
	})
	e := New(m, 0)
	run := Run{ID: "run-1", Input: json.RawMessage(`{"list":"0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01"}`), Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";

export default workflow({
  async run(ctx, input: { list: string }) {
    const created = await ctx.api.v1.resources.byId(input.list).children.post({ kind: "item", name: "Invoice 7" });
    const read = await ctx.api.v1.resources.byId(created!.id!).get();
    ctx.log("created", read?.name);
    return { id: read?.id, name: read?.name };
  },
});
`)}
	first := execute(t, e, run)
	if first.Status != Completed || string(first.Output) != `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","name":"Invoice 7"}` {
		t.Fatalf("first slice: %+v", first)
	}
	if len(first.Logs) != 1 || first.Logs[0].Message != "created Invoice 7" {
		t.Fatalf("logs = %+v", first.Logs)
	}
	again := execute(t, e, run)
	if again.Status != Completed || string(again.Output) != string(first.Output) || len(m.requests) != 2 {
		t.Fatalf("replay: %+v after %d requests", again, len(m.requests))
	}
}

func TestErrorResponsesThrowProblems(t *testing.T) {
	m := newMemory(func(Request) Response {
		return jsonResponse(404, `{"code":"not_found","detail":"no such resource"}`)
	})
	out := execute(t, New(m, 0), Run{ID: "run-2", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    try {
      await ctx.api.v1.resources.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01").get();
      return "found";
    } catch (e: any) {
      return { status: e.responseStatusCode, code: e.code, detail: e.detail };
    }
  },
});
`)})
	if out.Status != Completed || string(out.Output) != `{"status":404,"code":"not_found","detail":"no such resource"}` {
		t.Fatalf("outcome: %+v", out)
	}
}

const listID = "0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01"

func TestWaitsSuspendAndResume(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-3", Input: json.RawMessage(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { workflow, plain } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const started = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "done" } });
    ctx.log("started", started?.status);
    const done = await ctx.wait(started!);
    ctx.log("finished", done.status);
    return { status: done.status, result: plain(done.result) };
  },
});
`)}
	out := execute(t, e, run)
	if out.Status != Suspended || m.waits != 1 || len(m.requests) != 1 {
		t.Fatalf("first slice: %+v, %d waits, %d requests", out, m.waits, len(m.requests))
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != "started pending" {
		t.Fatalf("first slice logs: %+v", out.Logs)
	}
	if again := execute(t, e, run); again.Status != Suspended || m.waits != 1 || len(m.requests) != 1 || len(again.Logs) != 0 {
		t.Fatalf("a pending wait must not repeat anything: %+v", again)
	}
	m.finish("00000000-0000-4000-8000-000000000001", "succeeded")
	out = execute(t, e, run)
	if out.Status != Completed || string(out.Output) != `{"status":"succeeded","result":{"updated":2,"ids":["a","b"]}}` {
		t.Fatalf("last slice: %+v", out)
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != "finished succeeded" || len(m.requests) != 2 {
		t.Fatalf("last slice logs %+v after %d requests", out.Logs, len(m.requests))
	}
}

func TestWaitingForSeveralOperationsSuspendsOnce(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-parallel", Input: json.RawMessage(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const a = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "a" } });
    const b = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "b" } });
    const done = await ctx.wait([a!, b!]);
    return done.map((op) => op.status);
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended || m.waits != 1 {
		t.Fatalf("outcome: %+v", out)
	}
	m.finish("00000000-0000-4000-8000-000000000002", "succeeded")
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("one finished operation must not end the wait: %+v", out)
	}
	m.finish("00000000-0000-4000-8000-000000000001", "succeeded")
	if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `["succeeded","succeeded"]` || m.waits != 1 {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestWaitingForFinishedOperationsContinues(t *testing.T) {
	m := newMemory(nil)
	m.operations["00000000-0000-4000-8000-000000000009"] = "succeeded"
	out := execute(t, New(m, 0), Run{ID: "run-finished", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const op = await ctx.wait({ id: "00000000-0000-4000-8000-000000000009" });
    return op.status;
  },
});
`)})
	if out.Status != Completed || string(out.Output) != `"succeeded"` {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestFailedOperationsThrow(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-5", Input: json.RawMessage(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { workflow, OperationError } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const op = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "done" } });
    try {
      await ctx.wait(op!);
      return "succeeded";
    } catch (e) {
      return e instanceof OperationError ? e.message : "unexpected: " + e;
    }
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("outcome: %+v", out)
	}
	m.finish("00000000-0000-4000-8000-000000000001", "failed")
	if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `"operation 00000000-0000-4000-8000-000000000001 failed: the list is locked"` {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestWaitsNeedKnownOperations(t *testing.T) {
	out := execute(t, New(newMemory(nil), 0), Run{ID: "run-unknown", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    try {
      await ctx.wait({ id: "00000000-0000-4000-8000-000000000404" });
    } catch (e: any) {
      return e.message;
    }
  },
});
`)})
	if out.Status != Completed || !strings.Contains(string(out.Output), "no such operation") {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestSuspensionCannotBeCaught(t *testing.T) {
	m := newMemory(nil)
	run := Run{ID: "run-4", Input: json.RawMessage(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const op = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "done" } });
    try {
      await ctx.wait(op!);
    } catch {
      ctx.log("caught");
      return "caught";
    } finally {
      ctx.log("finally");
    }
    return "done";
  },
});
`)}
	out := execute(t, New(m, 0), run)
	if out.Status != Suspended || len(out.Logs) != 0 {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestValuesReplayDeterministically(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	m.operations["00000000-0000-4000-8000-000000000009"] = "pending"
	run := Run{ID: "run-6", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const before = { now: Date.now(), date: new Date().toISOString(), random: Math.random(), id: crypto.randomUUID() };
    await ctx.wait({ id: "00000000-0000-4000-8000-000000000009" });
    return { before };
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("outcome: %+v", out)
	}
	// The values were recorded with the wait that followed them.
	if steps := m.steps[run.ID]; len(steps) != 5 || steps[0].Name != "now" || steps[3].Name != "uuid" || steps[4].Kind != runs.StepWait {
		t.Fatalf("steps: %+v", steps)
	}
	m.finish("00000000-0000-4000-8000-000000000009", "succeeded")
	first := execute(t, e, run)
	second := execute(t, e, run)
	if first.Status != Completed || string(first.Output) != string(second.Output) {
		t.Fatalf("replays differ:\n%s\n%s", first.Output, second.Output)
	}
	var out struct {
		Before struct {
			Now    int64   `json:"now"`
			Random float64 `json:"random"`
			ID     string  `json:"id"`
		} `json:"before"`
	}
	if err := json.Unmarshal(first.Output, &out); err != nil || out.Before.Now < time.Now().Add(-time.Minute).UnixMilli() || len(out.Before.ID) != 36 || out.Before.Random <= 0 || out.Before.Random >= 1 {
		t.Fatalf("values: %s", first.Output)
	}
}

func TestChangedScriptsFailReplay(t *testing.T) {
	m := newMemory(func(Request) Response { return jsonResponse(200, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11"}`) })
	e := New(m, 0)
	run := Run{ID: "run-7", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    await ctx.api.v1.resources.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01").get();
    await ctx.api.v1.resources.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01").bulkUpdate.post({ values: { status: "done" } });
  },
});
`)}
	if out := execute(t, e, run); out.Status != Completed {
		t.Fatalf("outcome: %+v", out)
	}
	run.Artifact = bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    await ctx.api.v1.resources.byId("6e0d2f3c-1c1e-4bb1-8d0e-0f3a5c7b9d22").get();
  },
});
`)
	out := execute(t, e, run)
	if out.Status != Failed || !strings.Contains(out.Error, ErrNondeterministic.Error()) || len(m.requests) != 2 {
		t.Fatalf("outcome: %+v, requests %d", out, len(m.requests))
	}
}

func TestSandbox(t *testing.T) {
	m := newMemory(nil)
	out := execute(t, New(m, 0), Run{ID: "run-8", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
declare const __papergo: any;
function refused(fn: () => unknown): string {
  try { fn(); return "allowed"; } catch (e: any) { return "refused"; }
}
export default workflow({
  async run() {
    const scope = globalThis as any;
    console.log("logged", { n: 1 });
    return {
      absent: ["require", "process", "Buffer", "URL", "fetch", "setTimeout", "queueMicrotask"].filter((name) => typeof scope[name] !== "undefined"),
      identity: refused(() => __papergo.request("GET", "http://papergo.workflow/v1/identity/me", {}, undefined)),
      elsewhere: refused(() => __papergo.request("GET", "https://example.com/", {}, undefined)),
      text: new TextDecoder().decode(new TextEncoder().encode("ünï")),
    };
  },
});
`)})
	want := `{"absent":[],"identity":"refused","elsewhere":"refused","text":"ünï"}`
	if out.Status != Completed || string(out.Output) != want {
		t.Fatalf("outcome: %+v\nwant %s", out, want)
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != `logged {"n":1}` {
		t.Fatalf("logs: %+v", out.Logs)
	}
}

func TestSandboxRefusesEncodedExcludedPaths(t *testing.T) {
	for _, path := range []string{
		"/v1/%69dentity/me/scopes",
		"/v%31/events",
		"/v1/%24batch",
		"/v1/resources/r%2F1/workflow-roles",
	} {
		t.Run(path, func(t *testing.T) {
			m := newMemory(func(Request) Response { return jsonResponse(200, `{}`) })
			out := execute(t, New(m, 0), Run{ID: "encoded-scope", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
declare const __papergo: any;
export default workflow({
  run() {
    try {
      __papergo.request("GET", "http://papergo.workflow`+path+`", {}, undefined);
      return "allowed";
    } catch (e) {
      return e instanceof TypeError ? "refused" : "unexpected error";
    }
  },
});
`)})
			if out.Status != Completed || string(out.Output) != `"refused"` || len(m.requests) != 0 {
				t.Fatalf("outcome: %+v, requests: %+v", out, m.requests)
			}
		})
	}
}

func TestTextDecoderRejectsInvalidInputs(t *testing.T) {
	for name, input := range map[string]string{
		"plain object":       `{}`,
		"null prototype":     `Object.create(null)`,
		"number":             `42`,
		"string":             `"text"`,
		"array":              `[65]`,
		"fake view":          `{ buffer: new ArrayBuffer(1), byteOffset: -1, byteLength: 1 }`,
		"negative offset":    `Object.defineProperty(new Uint8Array(1), "byteOffset", { value: -1 })`,
		"negative length":    `Object.defineProperty(new Uint8Array(1), "byteLength", { value: -1 })`,
		"offset past buffer": `Object.defineProperty(new Uint8Array(1), "byteOffset", { value: 2 })`,
		"length past buffer": `Object.defineProperty(new Uint8Array(1), "byteLength", { value: 2 })`,
		"overflowing length": `Object.defineProperty(new Uint8Array(1), "byteLength", { value: 1e100 })`,
		"missing buffer":     `Object.defineProperty(new Uint8Array(1), "buffer", { value: undefined })`,
		"invalid buffer":     `Object.defineProperty(new Uint8Array(1), "buffer", { value: {} })`,
		"missing offset":     `Object.defineProperty(new Uint8Array(1), "byteOffset", { value: undefined })`,
		"missing length":     `Object.defineProperty(new Uint8Array(1), "byteLength", { value: undefined })`,
	} {
		t.Run(name, func(t *testing.T) {
			out := execute(t, New(newMemory(nil), 0), Run{ID: "invalid-decode", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  run() {
    try {
      new TextDecoder().decode(`+input+`);
      return "accepted";
    } catch (e) {
      return e instanceof TypeError ? "refused" : "unexpected error";
    }
  },
});
`)})
			if out.Status != Completed || string(out.Output) != `"refused"` {
				t.Fatalf("outcome: %+v", out)
			}
		})
	}
}

func TestTextDecoderReadsBufferViews(t *testing.T) {
	out := execute(t, New(newMemory(nil), 0), Run{ID: "decode-views", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  run() {
    const bytes = new Uint8Array([88, 89, 65, 66, 90]);
    const decode = (v) => new TextDecoder().decode(v);
    // Mutating the global must not let a plain object pass the view check.
    ArrayBuffer.isView = () => true;
    let spoofed = false;
    try { decode({ buffer: bytes.buffer, byteOffset: 0, byteLength: 1 }); }
    catch (e) { spoofed = e instanceof TypeError; }
    return {
      buffer: decode(bytes.buffer),
      subarray: decode(bytes.subarray(2, 4)),
      dataView: decode(new DataView(bytes.buffer, 2, 2)),
      wideView: decode(new Uint16Array(bytes.buffer, 2, 1)),
      empty: decode(bytes.subarray(5)),
      omitted: new TextDecoder().decode(),
      spoofed,
    };
  },
});
`)})
	want := `{"buffer":"XYABZ","subarray":"AB","dataView":"AB","wideView":"AB","empty":"","omitted":"","spoofed":true}`
	if out.Status != Completed || string(out.Output) != want {
		t.Fatalf("outcome: %+v\nwant %s", out, want)
	}
}

// Binary bodies travel as ArrayBuffers in both directions.
func TestBinaryBodies(t *testing.T) {
	m := newMemory(func(req Request) Response {
		if req.Method == "PUT" {
			return jsonResponse(201, fmt.Sprintf(`{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","size":%d}`, len(req.Body)))
		}
		return Response{Status: 200, Header: map[string]string{"Content-Type": "application/octet-stream"}, Body: []byte{0, 255, 1, 128}}
	})
	e := New(m, 0)
	run := Run{ID: "run-binary", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
declare const __papergo: any;
export default workflow({
  async run(ctx) {
    const blob = await ctx.api.v1.items.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01").content.put(new Uint8Array([0, 255, 254, 1]).buffer);
    const r = __papergo.request("GET", "http://papergo.workflow/v1/items/0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01/content", {}, undefined);
    return { size: blob?.size, got: Array.from(new Uint8Array(r.bytes)) };
  },
});
`)}
	for range 2 {
		if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `{"size":4,"got":[0,255,1,128]}` {
			t.Fatalf("outcome: %+v", out)
		}
	}
	if len(m.requests) != 2 || string(m.requests[0].Body) != "\x00\xff\xfe\x01" {
		t.Fatalf("requests: %+v", m.requests)
	}
}

func TestSliceLimit(t *testing.T) {
	out := execute(t, New(newMemory(nil), 200*time.Millisecond), Run{ID: "run-9", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run() {
    for (;;) {}
  },
});
`)})
	if out.Status != Failed || !strings.Contains(out.Error, errSlice.Error()) {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestErrorsReportScriptLines(t *testing.T) {
	out := execute(t, New(newMemory(nil), 0), Run{ID: "run-10", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";

export default workflow({
  async run() {
    throw new Error("broken");
  },
});
`)})
	if out.Status != Failed || !strings.Contains(out.Error, "broken") || !strings.Contains(out.Error, "workflow.ts:6") {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestBundleRejectsOtherImports(t *testing.T) {
	_, err := Bundle("workflow.ts", `import _ from "lodash"; export default _;`)
	if err == nil || !strings.Contains(err.Error(), `can import only @papergo/workflow, not "lodash"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err = Bundle("workflow.ts", `export default {`); err == nil || !strings.Contains(err.Error(), "workflow.ts:1:") {
		t.Fatalf("err = %v", err)
	}
}
