package script

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal/runs"
)

// memory is an in-memory journal whose requests a test answers.
type memory struct {
	mu       sync.Mutex
	steps    map[string][]runs.Step
	requests []Request
	started  []runs.Step
	answer   func(Request) Response
}

func newMemory(answer func(Request) Response) *memory {
	return &memory{steps: map[string][]runs.Step{}, answer: answer}
}

func (m *memory) Steps(_ context.Context, runID string) ([]runs.Step, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]runs.Step(nil), m.steps[runID]...), nil
}

func (m *memory) record(steps ...runs.Step) {
	for _, s := range steps {
		if s.Seq != len(m.steps[s.RunID]) {
			panic("steps recorded out of order")
		}
		m.steps[s.RunID] = append(m.steps[s.RunID], s)
	}
}

func (m *memory) Request(_ context.Context, unsaved []runs.Step, step runs.Step, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	response := m.answer(req)
	step.Output = ResponseOutput(response)
	m.record(append(unsaved, step)...)
	return response, nil
}

func (m *memory) Start(_ context.Context, unsaved []runs.Step, step runs.Step) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = append(m.started, step)
	m.record(append(unsaved, step)...)
	return nil
}

func (m *memory) finish(t *testing.T, runID string, seq int, output, failure string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &m.steps[runID][seq]
	if s.Status != runs.StepPending {
		t.Fatalf("step %d is %s", seq, s.Status)
	}
	if failure != "" {
		s.Status, s.Error = runs.StepFailed, failure
	} else {
		s.Status, s.Output = runs.StepCompleted, json.RawMessage(output)
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

func TestActionsSuspendAndResume(t *testing.T) {
	m := newMemory(func(Request) Response { return jsonResponse(500, `{}`) })
	e := New(m, 0)
	run := Run{ID: "run-3", Input: json.RawMessage(`{"doc":"d1"}`), Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { doc: string }) {
    ctx.log("reading", input.doc);
    const text = await ctx.action<{ text: string }>("ocr", { doc: input.doc });
    ctx.log("read", text.text);
    const vector = await ctx.action<number[]>("embeddings", { text: text.text });
    return { text: text.text, size: vector.length };
  },
});
`)}
	out := execute(t, e, run)
	if out.Status != Suspended || len(m.started) != 1 || m.started[0].Name != "ocr" || string(m.started[0].Input) != `{"doc":"d1"}` {
		t.Fatalf("first slice: %+v, started %+v", out, m.started)
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != "reading d1" {
		t.Fatalf("first slice logs: %+v", out.Logs)
	}
	if again := execute(t, e, run); again.Status != Suspended || len(m.started) != 1 || len(again.Logs) != 0 {
		t.Fatalf("a pending action must not start again: %+v, started %d", again, len(m.started))
	}
	m.finish(t, run.ID, 0, `{"text":"hello"}`, "")
	out = execute(t, e, run)
	if out.Status != Suspended || len(m.started) != 2 || m.started[1].Name != "embeddings" || string(m.started[1].Input) != `{"text":"hello"}` {
		t.Fatalf("second slice: %+v", out)
	}
	if len(out.Logs) != 1 || out.Logs[0].Message != "read hello" {
		t.Fatalf("second slice logs: %+v", out.Logs)
	}
	m.finish(t, run.ID, 1, `[0.1,0.2,0.3]`, "")
	out = execute(t, e, run)
	if out.Status != Completed || string(out.Output) != `{"text":"hello","size":3}` || len(m.requests) != 0 {
		t.Fatalf("last slice: %+v", out)
	}
}

func TestSuspensionCannotBeCaught(t *testing.T) {
	m := newMemory(nil)
	run := Run{ID: "run-4", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    try {
      await ctx.action("approval", {});
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

func TestFailedActionsThrow(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-5", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    try {
      await ctx.action("ocr", { doc: "d" });
      return "read";
    } catch (e: any) {
      return "failed: " + e.message;
    }
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("outcome: %+v", out)
	}
	m.finish(t, run.ID, 0, "", "the file has no pages")
	if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `"failed: the file has no pages"` {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestValuesReplayDeterministically(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-6", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const before = { now: Date.now(), date: new Date().toISOString(), random: Math.random(), id: crypto.randomUUID() };
    await ctx.action("wait", {});
    return { before };
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("outcome: %+v", out)
	}
	// The values were recorded with the action that followed them.
	if steps := m.steps[run.ID]; len(steps) != 5 || steps[0].Name != "now" || steps[3].Name != "uuid" || steps[4].Kind != runs.StepAction {
		t.Fatalf("steps: %+v", steps)
	}
	m.finish(t, run.ID, 4, "null", "")
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
    await ctx.action("ocr", {});
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended {
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
	if out.Status != Failed || !strings.Contains(out.Error, ErrNondeterministic.Error()) || len(m.requests) != 1 {
		t.Fatalf("outcome: %+v, requests %d", out, len(m.requests))
	}
}

func TestSandbox(t *testing.T) {
	m := newMemory(nil)
	out := execute(t, New(m, 0), Run{ID: "run-8", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
// A require() call esbuild can see is an import; reach the runtime's own.
const require = (globalThis as any).require as (name: string) => any;
declare const process: any;
declare const __papergo: any;
function refused(fn: () => unknown): string {
  try { fn(); return "allowed"; } catch (e: any) { return "refused"; }
}
export default workflow({
  async run() {
    return {
      env: Object.keys(process.env).length + Object.keys(require("process").env).length,
      fs: refused(() => require("fs")),
      file: refused(() => require("/etc/hostname")),
      relative: refused(() => require("./other.js")),
      fetch: typeof (globalThis as any).fetch,
      setTimeout: typeof (globalThis as any).setTimeout,
      identity: refused(() => __papergo.request("GET", "http://papergo.workflow/v1/identity/me", {}, undefined, false)),
      elsewhere: refused(() => __papergo.request("GET", "https://example.com/", {}, undefined, false)),
      url: new URL("https://a.example/x?y=1").searchParams.get("y"),
      buffer: Buffer.from("aGk=", "base64").toString("utf8"),
      text: new TextDecoder().decode(new TextEncoder().encode("ünï")),
    };
  },
});
`)})
	want := `{"env":0,"fs":"refused","file":"refused","relative":"refused","fetch":"undefined","setTimeout":"undefined","identity":"refused","elsewhere":"refused","url":"1","buffer":"hi","text":"ünï"}`
	if out.Status != Completed || string(out.Output) != want {
		t.Fatalf("outcome: %+v\nwant %s", out, want)
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
