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
	// results are the results of operations that do not end as bulk
	// updates do, such as tasks.
	results map[string]string
	answer  func(Request) Response
}

func newMemory(answer func(Request) Response) *memory {
	return &memory{steps: map[string][]runs.Step{}, operations: map[string]string{}, results: map[string]string{}, answer: answer}
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
	response := m.respond(req)
	step.Output = ResponseOutput(response)
	m.record(append(unsaved, step)...)
	return response, nil
}

func (m *memory) Read(_ context.Context, _ Run, req Request) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.respond(req), nil
}

func (m *memory) Record(_ context.Context, _ Run, unsaved []runs.Step) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(unsaved...)
	return nil
}

// respond answers a request; the caller holds m.mu.
func (m *memory) respond(req Request) Response {
	m.requests = append(m.requests, req)
	var response Response
	switch {
	case req.Method == "POST" && strings.HasSuffix(req.URL, "/bulk-update"):
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(m.operations)+1)
		m.operations[id] = "pending"
		response = jsonResponse(202, `{"id":"`+id+`","status":"pending"}`)
	case req.Method == "GET" && strings.HasPrefix(req.URL, "/v1/operations/"):
		response = jsonResponse(200, m.operation(strings.TrimPrefix(req.URL, "/v1/operations/")))
	default:
		response = m.answer(req)
	}
	return response
}

// operation is an operation as the API shows it; the caller holds m.mu.
func (m *memory) operation(id string) string {
	status := m.operations[id]
	if status == "failed" {
		return `{"id":"` + id + `","status":"failed","error":"the list is locked"}`
	}
	if result, ok := m.results[id]; ok {
		return `{"id":"` + id + `","status":"` + status + `","result":` + result + `}`
	}
	return `{"id":"` + id + `","status":"` + status + `","result":{"updated":2,"ids":["a","b"]}}`
}

// ended is the output of a wait for step's operations, which have ended.
func (m *memory) ended(step runs.Step) json.RawMessage {
	ops := make([]string, len(step.Operations))
	for i, id := range step.Operations {
		ops[i] = m.operation(id)
	}
	return json.RawMessage("[" + strings.Join(ops, ",") + "]")
}

func (m *memory) over(step runs.Step) bool {
	for _, id := range step.Operations {
		if m.operations[id] == "pending" {
			return false
		}
	}
	return true
}

func (m *memory) Wait(_ context.Context, _ Run, unsaved []runs.Step, step runs.Step) (json.RawMessage, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range step.Operations {
		if _, ok := m.operations[id]; !ok {
			return nil, false, ErrUnknownOperation
		}
	}
	m.waits++
	done := m.over(step)
	if done {
		step.Status, step.Output = runs.StepCompleted, m.ended(step)
	}
	m.record(append(unsaved, step)...)
	return step.Output, done, nil
}

// finish ends an operation and releases the waits it completes, as the
// operations repository does.
func (m *memory) finish(id, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operations[id] = status
	for _, steps := range m.steps {
		for i := range steps {
			if steps[i].Kind == runs.StepWait && steps[i].Status == runs.StepPending && len(steps[i].Operations) > 0 && m.over(steps[i]) {
				steps[i].Status, steps[i].Output = runs.StepCompleted, m.ended(steps[i])
			}
		}
	}
}

func (m *memory) Sleep(_ context.Context, _ Run, unsaved []runs.Step, step runs.Step) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(append(unsaved, step)...)
	return nil
}

func (m *memory) Wake(_ context.Context, run Run, seq int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps[run.ID][seq].Status = runs.StepCompleted
	return nil
}

// withInput is a run context with input, given as JSON.
func withInput(input string) map[string]any {
	var v any
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		panic(err)
	}
	return map[string]any{"input": v}
}

func bundle(t *testing.T, source string) Artifact {
	t.Helper()
	a, err := Bundle("workflow.ts", source)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// execute runs a slice and, as the runner does, records the steps a
// finished run left unsaved.
func execute(t testing.TB, e *Engine, run Run) Outcome {
	t.Helper()
	out, err := e.Execute(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.effects.Record(context.Background(), run, out.Unsaved); err != nil {
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
	run := Run{ID: "run-1", Context: withInput(`{"list":"0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01"}`), Artifact: bundle(t, `
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

// The API links a page to the next with the origin it was asked on, which
// is none in-process; all() asks the SDK's origin with the link's query.
func TestPagedListsReadEveryPage(t *testing.T) {
	m := newMemory(func(req Request) Response {
		children := "/v1/resources/" + listID + "/children"
		query := "/v1/resources/" + listID + "/query"
		switch {
		case req.Method == "GET" && req.URL == children+"?%24top=2":
			return jsonResponse(200, `{"value":[{"name":"A"},{"name":"B"}],"@odata.nextLink":"http:///v1/resources/`+listID+`/children?%24skiptoken=t1&%24top=2"}`)
		case req.Method == "GET" && req.URL == children+"?%24skiptoken=t1&%24top=2":
			return jsonResponse(200, `{"value":[{"name":"C"}],"@odata.nextLink":null}`)
		case req.Method == "POST" && req.URL == query && string(req.Body) == `{"surface":"head"}`:
			return jsonResponse(200, `{"value":[{"name":"D"}],"@odata.nextLink":"https://papergo.example/v1/resources/`+listID+`/query?%24skiptoken=t2"}`)
		case req.Method == "POST" && req.URL == query+"?%24skiptoken=t2" && string(req.Body) == `{"surface":"head"}`:
			return jsonResponse(200, `{"value":[{"name":"E"}]}`)
		}
		return jsonResponse(404, `{"code":"not_found","detail":"route not found"}`)
	})
	out := execute(t, New(m, 0), Run{ID: "run-pages", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const list = ctx.api.v1.resources.byId("`+listID+`");
    const children = await list.children.all({ queryParameters: { top: 2 } });
    const rows = await list.query.all({ surface: "head" });
    return [...children, ...rows].map((r) => r.name);
  },
});
`)})
	if out.Status != Completed || string(out.Output) != `["A","B","C","D","E"]` || len(m.requests) != 4 {
		t.Fatalf("outcome: %+v after %d requests", out, len(m.requests))
	}
}

const listID = "0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01"

func TestWaitsSuspendAndResume(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-3", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
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
	// The wait returns the operation as it ended, without reading it again.
	if len(out.Logs) != 1 || out.Logs[0].Message != "finished succeeded" || len(m.requests) != 1 {
		t.Fatalf("last slice logs %+v after %d requests", out.Logs, len(m.requests))
	}
}

func TestWaitingForSeveralOperationsSuspendsOnce(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-parallel", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
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

func TestWaitsStartedTogetherSuspendOnce(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "run-together", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { OperationError, workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const update = async (status: string) => {
      const op = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status } });
      return (await ctx.wait(op!)).status;
    };
    const settled = await Promise.allSettled([update("a"), update("b"), update("c")]);
    return settled.map((s) => s.status === "fulfilled" ? s.value : (s.reason as OperationError).operation.status);
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended || m.waits != 1 || len(m.requests) != 3 {
		t.Fatalf("outcome: %+v after %d waits and %d requests", out, m.waits, len(m.requests))
	}
	m.finish("00000000-0000-4000-8000-000000000001", "succeeded")
	m.finish("00000000-0000-4000-8000-000000000002", "failed")
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("the wait must last until every operation finished: %+v", out)
	}
	m.finish("00000000-0000-4000-8000-000000000003", "succeeded")
	if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `["succeeded","failed","succeeded"]` || m.waits != 1 {
		t.Fatalf("outcome: %+v after %d waits", out, m.waits)
	}
}

func TestTasksAskedTogetherAreAnsweredTogether(t *testing.T) {
	const ws = "7c1e0d52-8f0e-4b8b-9d0e-3c7f6a2b1e01"
	var m *memory
	var posted []string
	m = newMemory(func(req Request) Response {
		switch {
		case req.Method == "POST" && req.URL == "/v1/workspaces/"+ws+"/tasks":
			posted = append(posted, string(req.Body))
			id := fmt.Sprintf("00000000-0000-4000-9000-%012d", len(posted))
			m.operations[id] = "pending"
			return jsonResponse(201, `{"id":"task-`+id+`","title":"t","operation_id":"`+id+`"}`)
		case req.Method == "POST" && req.URL == "/v1/workspaces/"+ws+"/notifications":
			posted = append(posted, string(req.Body))
			return jsonResponse(200, `{"sent":2,"skipped":0,"deduplicated":1}`)
		}
		return jsonResponse(404, `{"code":"not_found","detail":"route not found"}`)
	})
	e := New(m, 0)
	run := Run{ID: "run-ask", Context: map[string]any{"workspace_id": ws}, Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const [legal, finance] = await Promise.all([
      ctx.ask<{ amount: number }>({
        title: "Legal review", assignees: ["group:legal"],
        inputSchema: { type: "object", properties: { amount: { type: "number" } } }, values: { amount: 3 },
      }),
      ctx.ask({ title: "Budget check", kind: "approval" }),
    ]);
    const sent = await ctx.notify({ recipients: ["actor"], title: "Reviewed", dedupKey: "reviewed" });
    return [legal.outcome, legal.input?.amount, legal.responses.length, finance.outcome, sent.sent];
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended || m.waits != 1 || len(posted) != 2 {
		t.Fatalf("outcome: %+v after %d waits and %d tasks", out, m.waits, len(posted))
	}
	if posted[0] != `{"assignees":["group:legal"],"input_schema":{"type":"object","properties":{"amount":{"type":"number"}}},"title":"Legal review","values":{"amount":3}}` ||
		posted[1] != `{"kind":"approval","title":"Budget check"}` {
		t.Fatalf("tasks: %v", posted)
	}
	m.results["00000000-0000-4000-9000-000000000001"] = `{"task_id":"a","status":"completed","outcome":"done","comment":"","input":{"amount":4},"responses":[{"subject":"bob","assignee":"group:legal","outcome":"done","comment":""}]}`
	m.results["00000000-0000-4000-9000-000000000002"] = `{"task_id":"b","status":"completed","outcome":"approved","comment":"","responses":[]}`
	m.finish("00000000-0000-4000-9000-000000000002", "succeeded")
	if out := execute(t, e, run); out.Status != Suspended {
		t.Fatalf("one answered task must not end the wait: %+v", out)
	}
	m.finish("00000000-0000-4000-9000-000000000001", "succeeded")
	out := execute(t, e, run)
	if out.Status != Completed || string(out.Output) != `["done",4,1,"approved",2]` || m.waits != 1 {
		t.Fatalf("outcome: %+v after %d waits", out, m.waits)
	}
	if len(posted) != 3 || posted[2] != `{"dedup_key":"reviewed","recipients":["actor"],"title":"Reviewed"}` {
		t.Fatalf("notification: %v", posted)
	}
}

func TestCallsMadeTogetherStartAndWaitTogether(t *testing.T) {
	const ws = "7c1e0d52-8f0e-4b8b-9d0e-3c7f6a2b1e01"
	var m *memory
	var bodies []string
	m = newMemory(func(req Request) Response {
		if req.Method != "POST" || req.URL != "/v1/workspaces/"+ws+"/workflow-calls" {
			return jsonResponse(404, `{"code":"not_found","detail":"route not found"}`)
		}
		bodies = append(bodies, string(req.Body))
		var in struct {
			Calls []json.RawMessage `json:"calls"`
		}
		_ = json.Unmarshal(req.Body, &in)
		ids := make([]string, len(in.Calls))
		for i := range in.Calls {
			ids[i] = fmt.Sprintf(`"wf:check:%d"`, len(m.operations))
			m.operations[strings.Trim(ids[i], `"`)] = "pending"
		}
		return jsonResponse(202, `{"run_ids":[`+strings.Join(ids, ",")+`]}`)
	})
	e := New(m, 0)
	run := Run{ID: "run-calls", Context: map[string]any{"workspace_id": ws}, Artifact: bundle(t, `
import { OperationError, workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const results = await Promise.allSettled(["a", "b", "c"].map((id) => ctx.call<{ total: number }>("check", { strict: true }, { itemId: id })));
    return results.map((r) => r.status === "fulfilled" ? r.value.total : (r.reason as OperationError).operation.status);
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended || len(bodies) != 1 || m.waits != 1 {
		t.Fatalf("outcome: %+v after %d call requests and %d waits", out, len(bodies), m.waits)
	}
	if bodies[0] != `{"calls":[{"input":{"strict":true},"item_id":"a"},{"input":{"strict":true},"item_id":"b"},{"input":{"strict":true},"item_id":"c"}],"workflow":"check"}` {
		t.Fatalf("call request: %s", bodies[0])
	}
	m.results["wf:check:0"], m.results["wf:check:2"] = `{"total":1}`, `{"total":3}`
	m.finish("wf:check:0", "succeeded")
	m.finish("wf:check:1", "failed")
	m.finish("wf:check:2", "succeeded")
	if out := execute(t, e, run); out.Status != Completed || string(out.Output) != `[1,"failed",3]` || len(bodies) != 1 || m.waits != 1 {
		t.Fatalf("outcome: %+v after %d call requests and %d waits", out, len(bodies), m.waits)
	}
}

func TestRunsContinueAsNew(t *testing.T) {
	m := newMemory(func(Request) Response {
		return jsonResponse(200, `{"id":"`+listID+`","kind":"list","name":"Records"}`)
	})
	e := New(m, 0)
	source := bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const n = (ctx.state as { n: number } | undefined)?.n ?? 0;
    await ctx.api.v1.resources.byId("`+listID+`").get();
    try {
      if (n < 2) ctx.continueAsNew({ n: n + 1 });
    } catch {
      return "caught";
    }
    return { n, generation: ctx.generation };
  },
});
`)
	// The runner starts each generation with a fresh journal and the state.
	context := map[string]any{"generation": 0}
	for generation := range 3 {
		run := Run{ID: "run-continue", Context: context, Artifact: source}
		m.steps[run.ID] = nil
		out := execute(t, e, run)
		if generation < 2 {
			want := fmt.Sprintf(`{"n":%d}`, generation+1)
			if out.Status != Continued || string(out.State) != want {
				t.Fatalf("generation %d: %+v", generation, out)
			}
			// A replay of the generation continues the same way.
			if again := execute(t, e, run); again.Status != Continued || string(again.State) != want {
				t.Fatalf("replayed generation %d: %+v", generation, again)
			}
			context = map[string]any{"generation": generation + 1, "state": out.State}
			continue
		}
		if out.Status != Completed || string(out.Output) != `{"n":2,"generation":2}` {
			t.Fatalf("last generation: %+v", out)
		}
	}
	// A read is recorded with the next durable step, and a generation ends
	// before one: replaying a generation repeats its read, as a crash would.
	if len(m.requests) != 5 {
		t.Fatalf("%d requests for three generations, two of them replayed", len(m.requests))
	}

	// A run that continues with work it has not awaited is told so.
	pending := execute(t, New(newMemory(nil), 0), Run{ID: "run-pending", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx, input: { list: string }) {
    const op = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ values: { status: "a" } });
    void ctx.wait(op!);
    ctx.continueAsNew({});
  },
});
`)})
	if pending.Status != Failed || !strings.Contains(pending.Error, "await ctx.call and ctx.wait before ctx.continueAsNew") {
		t.Fatalf("continuing with a pending wait: %+v", pending)
	}
}

func TestJournalsAreBounded(t *testing.T) {
	page := jsonResponse(200, `{"value":[],"padding":"`+strings.Repeat("x", 1<<20)+`"}`)
	m := newMemory(func(Request) Response { return page })
	// The script reads 32 MiB without waiting; its slice is long enough
	// for that under the race detector, which TestSliceLimit covers.
	out := execute(t, New(m, time.Minute), Run{ID: "run-journal", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    for (;;) await ctx.api.v1.resources.byId("`+listID+`").children.get();
  },
});
`)})
	// The outcome holds the megabyte responses; the failure names only its status.
	if out.Status != Failed || !strings.Contains(out.Error, "more than 32 MiB") || len(m.requests) != maxJournal/(1<<20) {
		t.Fatalf("outcome %s: %s, after %d requests", out.Status, out.Error, len(m.requests))
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
	run := Run{ID: "run-5", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
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
	run := Run{ID: "run-4", Context: withInput(`{"list":"` + listID + `"}`), Artifact: bundle(t, `
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

// A version's definition is read from its export without running it:
// triggers may depend on parameters, and effects are refused.
func TestManifest(t *testing.T) {
	ctx := context.Background()
	e := New(newMemory(nil), 0)
	art := bundle(t, `
import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: (p: { collection: string }) => [{ type: "file.added", collection_id: p.collection }, { type: "manual" }],
  concurrency: "coalesce",
  output_schema: { type: "object", properties: { pages: { type: "integer" } } },
  async run() { throw new Error("run is not called"); },
});
`)
	raw, err := e.Manifest(ctx, art, map[string]any{"collection": "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Triggers []struct {
			Type         string `json:"type"`
			CollectionID string `json:"collection_id"`
		} `json:"triggers"`
		Concurrency  string         `json:"concurrency"`
		OutputSchema map[string]any `json:"output_schema"`
	}
	if err = json.Unmarshal(raw, &m); err != nil || len(m.Triggers) != 2 || m.Triggers[0].CollectionID != "lib-1" || m.Concurrency != "coalesce" || m.OutputSchema == nil {
		t.Fatalf("%s %v", raw, err)
	}
	effects := bundle(t, `
import { workflow } from "@papergo/workflow";
const side = (globalThis as any).__papergo.request("GET", "http://papergo.workflow/v1/workspaces", {}, undefined);
export default workflow({ async run() { return side; } });
`)
	if _, err = e.Manifest(ctx, effects, nil); err == nil || !strings.Contains(err.Error(), "requests, waits and sleeps belong in run") {
		t.Fatalf("an effect at load time: %v", err)
	}
	if _, err = e.Manifest(ctx, bundle(t, `export default 1;`), nil); err == nil {
		t.Fatal("a script without workflow() was read")
	}
}

// A sleep suspends the run until its time; the resumed run continues past
// it, and a sleep whose time came is over at once.
func TestSleep(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	run := Run{ID: "sleeper", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";

export default workflow({
  async run(ctx) {
    await ctx.sleep(60_000);
    await ctx.sleep(0);
    return "awake";
  },
});
`)}
	out := execute(t, e, run)
	if out.Status != Suspended || out.Until == nil || out.Until.Before(time.Now().Add(50*time.Second)) {
		t.Fatalf("%+v", out)
	}
	if steps := m.steps["sleeper"]; len(steps) != 2 || steps[1].Status != runs.StepPending || steps[0].Kind != runs.StepValue {
		t.Fatalf("%+v", steps)
	}
	if out = execute(t, e, run); out.Status != Suspended {
		t.Fatalf("woke early: %+v", out)
	}
	// Time passes: the recorded wake time is in the past.
	m.steps["sleeper"][1].Input = json.RawMessage(`{"until":"2000-01-01T00:00:00Z"}`)
	m.steps["sleeper"][0].Output = json.RawMessage(fmt.Sprint(time.Date(1999, 12, 31, 23, 59, 0, 0, time.UTC).UnixNano()))
	m.steps["sleeper"][1].InputHash = digest(string(runs.StepWait), "sleep", "2000-01-01T00:00:00Z")
	if out = execute(t, e, run); out.Status != Completed || string(out.Output) != `"awake"` {
		t.Fatalf("%+v", out)
	}
	if steps := m.steps["sleeper"]; steps[1].Status != runs.StepCompleted || len(steps) != 4 || steps[3].Status != runs.StepCompleted {
		t.Fatalf("%+v", steps)
	}
}

func TestTriggerContextSurvivesReplayWithoutFetchingResource(t *testing.T) {
	m := newMemory(nil)
	e := New(m, 0)
	const operationID = "00000000-0000-4000-8000-000000000009"
	m.operations[operationID] = "pending"
	var trigger any
	if err := json.Unmarshal([]byte(`{"kind":"file","item":{"id":"item","name":"Event item","values":{"title":"Invoice"}},"file":{"id":"blob","version":1,"filename":"trigger.pdf"}}`), &trigger); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "trigger-context", Context: map[string]any{"trigger": trigger}, Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const trigger = ctx.trigger;
    if (trigger !== ctx.run.trigger || trigger?.kind !== "file") throw new Error("missing trigger");
    await ctx.wait("00000000-0000-4000-8000-000000000009");
    return { name: trigger.item.name, filename: trigger.file.filename, title: trigger.item.values?.title };
  },
});
`)}
	if out := execute(t, e, run); out.Status != Suspended || len(m.requests) != 0 {
		t.Fatalf("snapshot access performed a request or failed: %+v", out)
	}
	m.finish(operationID, "succeeded")
	for range 2 {
		out := execute(t, e, run)
		if out.Status != Completed || string(out.Output) != `{"name":"Event item","filename":"trigger.pdf","title":"Invoice"}` {
			t.Fatalf("snapshot lost on replay: %+v", out)
		}
	}
	if len(m.requests) != 0 {
		t.Fatal("snapshot access fetched the resource", m.requests)
	}
}

func TestManualTriggerFormAndSelectionReplay(t *testing.T) {
	for _, tc := range []struct {
		name, selection, primary, expected string
	}{
		{"standalone", `[]`, "", `{"note":"Saved form","primary":null,"items":[]}`},
		{"single", `[{"id":"a","snapshot":{"id":"a","name":"First"}}]`, "a", `{"note":"Saved form","primary":"a","items":[{"id":"a","name":"First"}]}`},
		{"selection", `[{"id":"a","snapshot":{"id":"a","name":"First"}},{"id":"b","snapshot":{"id":"b","name":"Second"}},{"id":"unavailable"}]`, "b", `{"note":"Saved form","primary":"b","items":[{"id":"a","name":"First"},{"id":"b","name":"Second"},{"id":"unavailable","name":null}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMemory(nil)
			e := New(m, 0)
			const operationID = "00000000-0000-4000-8000-000000000009"
			m.operations[operationID] = "pending"
			info := withInput(`{"note":"Saved form"}`)
			info["item_id"] = tc.primary
			var trigger any
			if err := json.Unmarshal([]byte(`{"kind":"manual","items":`+tc.selection+`}`), &trigger); err != nil {
				t.Fatal(err)
			}
			info["trigger"] = trigger
			run := Run{ID: "manual-" + tc.name, Context: info, Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow<{note: string}>({
  async run(ctx, input) {
    const trigger = ctx.trigger;
    if (trigger?.kind !== "manual" || trigger.input !== input || trigger !== ctx.run.trigger) throw new Error("missing form input");
    if (trigger.item !== trigger.items.find(item => item.id === ctx.itemId)?.snapshot) throw new Error("wrong primary item");
    await ctx.wait("00000000-0000-4000-8000-000000000009");
    return { note: trigger.input.note, primary: trigger.item?.id ?? null, items: trigger.items.map(item => ({id: item.id, name: item.snapshot?.name ?? null})) };
  },
});
`)}
			if out := execute(t, e, run); out.Status != Suspended || len(m.requests) != 0 {
				t.Fatalf("manual context failed or fetched resources: %+v", out)
			}
			m.finish(operationID, "succeeded")
			for range 2 {
				if out := execute(t, e, run); out.Status != Completed || string(out.Output) != tc.expected {
					t.Fatalf("manual form/selection lost on replay: %+v", out)
				}
			}
			if len(m.requests) != 0 {
				t.Fatal("manual context fetched resources", m.requests)
			}
		})
	}
}

// Reads are recorded with the next durable step, early once many wait,
// and with the outcome of a finished run; replaying repeats none of them.
func TestReadsAreRecordedWithTheNextDurableStep(t *testing.T) {
	m := newMemory(func(req Request) Response {
		return jsonResponse(200, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","kind":"item","name":"Invoice 7"}`)
	})
	e := New(m, 0)
	run := Run{ID: "run-reads", Artifact: bundle(t, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    const item = ctx.api.v1.resources.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01");
    for (let i = 0; i < 300; i++) await item.get();
    await item.children.post({ kind: "item", name: "After" });
    await item.get();
    return 1;
  },
});
`)}
	out, err := e.Execute(context.Background(), run)
	if err != nil || out.Status != Completed {
		t.Fatal(out, err)
	}
	steps, _ := m.Steps(context.Background(), run)
	if len(steps) != 301 || steps[300].Name != "POST /v1/resources/0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01/children" {
		t.Fatalf("%d steps recorded before the run ended", len(steps))
	}
	if len(out.Unsaved) != 1 || out.Unsaved[0].Seq != 301 || out.Unsaved[0].CompletedAt == nil {
		t.Fatalf("unsaved: %+v", out.Unsaved)
	}
	if err = m.Record(context.Background(), run, out.Unsaved); err != nil {
		t.Fatal(err)
	}
	requests := len(m.requests)
	if out = execute(t, e, run); out.Status != Completed || len(m.requests) != requests {
		t.Fatalf("the replay made %d requests: %+v", len(m.requests)-requests, out)
	}
}
