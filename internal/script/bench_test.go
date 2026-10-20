package script

import (
	"context"
	"testing"

	"github.com/dop251/goja"
	"papergo/sdk"
)

// BenchmarkSlice measures one slice of a script that returns at once: a
// runtime, the SDK and the script.
func BenchmarkSlice(b *testing.B) {
	e := New(newMemory(nil), 0)
	run := Run{ID: "bench", Artifact: benchBundle(b, `
import { workflow } from "@papergo/workflow";
export default workflow({ async run() { return 1; } });
`)}
	b.ReportAllocs()
	for b.Loop() {
		if out, err := e.Execute(context.Background(), run); err != nil || out.Status != Completed {
			b.Fatal(out, err)
		}
	}
}

// BenchmarkReplay measures replaying a run that has recorded 100 requests.
func BenchmarkReplay(b *testing.B) {
	m := newMemory(func(Request) Response {
		return jsonResponse(200, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","kind":"item","name":"Invoice 7"}`)
	})
	e := New(m, 0)
	run := Run{ID: "bench", Artifact: benchBundle(b, `
import { workflow } from "@papergo/workflow";
export default workflow({
  async run(ctx) {
    let n = 0;
    for (let i = 0; i < 100; i++) {
      const r = await ctx.api.v1.resources.byId("0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01").get();
      if (r?.name) n++;
    }
    return n;
  },
});
`)}
	if out := execute(b, e, run); string(out.Output) != "100" {
		b.Fatal(out)
	}
	b.ReportAllocs()
	for b.Loop() {
		if out, err := e.Execute(context.Background(), run); err != nil || string(out.Output) != "100" {
			b.Fatal(out, err)
		}
	}
	if len(m.requests) != 100 {
		b.Fatalf("%d requests", len(m.requests))
	}
}

// BenchmarkReplayHost is BenchmarkReplay calling the host directly, without
// the SDK: the cost of the engine itself.
func BenchmarkReplayHost(b *testing.B) {
	m := newMemory(func(Request) Response {
		return jsonResponse(200, `{"id":"5f0c8b8e-6d0a-4f1e-9a52-6a9a7c1f0a11","kind":"item","name":"Invoice 7"}`)
	})
	e := New(m, 0)
	run := Run{ID: "bench", Artifact: benchBundle(b, `
import { workflow } from "@papergo/workflow";
declare const __papergo: any;
export default workflow({
  async run() {
    let n = 0;
    for (let i = 0; i < 100; i++) {
      const r = __papergo.request("GET", "http://papergo.workflow/v1/resources/0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01", {}, undefined, false);
      if (JSON.parse(r.body).name) n++;
    }
    return n;
  },
});
`)}
	if out := execute(b, e, run); string(out.Output) != "100" {
		b.Fatal(out)
	}
	b.ReportAllocs()
	for b.Loop() {
		if out, err := e.Execute(context.Background(), run); err != nil || string(out.Output) != "100" {
			b.Fatal(out, err)
		}
	}
}

// BenchmarkRuntime measures the parts of a slice before the script runs.
func BenchmarkRuntime(b *testing.B) {
	sdkCode, err := sdkProgram()
	if err != nil {
		b.Fatal(err)
	}
	b.Run("globals", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			(&host{ctx: context.Background()}).runtime()
		}
	})
	b.Run("globals+sdk", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rt := (&host{ctx: context.Background()}).runtime()
			if _, err := rt.RunProgram(sdkCode); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("compile-sdk", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := goja.Compile("sdk.js", sdk.Bundle, true); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchBundle(b *testing.B, source string) Artifact {
	b.Helper()
	a, err := Bundle("workflow.ts", source)
	if err != nil {
		b.Fatal(err)
	}
	return a
}
