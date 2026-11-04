package apps

import (
	"encoding/json"
	"strings"
	"testing"
)

const dashboard = `import { useState } from "react";
import { app, useQuery, Stat } from "@papergo/app";

function Tile({ label }: { label: string }) {
  const [open] = useState(false);
  return <Stat label={label} value={open ? "1" : "0"} />;
}

export default app({
  parameters: {
    type: "object",
    required: ["list"],
    properties: { list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } } },
  },
  render(ctx: { params: { list: string } }) {
    const rows = useQuery(ctx.params.list, {});
    return <><Tile label="Now" />{rows.data.length}</>;
  },
});
`

// An app compiles to code that reads the runtime's modules, and declares
// its parameters form.
func TestAppsCompileAndDeclareTheirParameters(t *testing.T) {
	c, err := Compile(dashboard)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hash) != 64 || !strings.Contains(c.Code, `globalThis.__papergoApp.modules["@papergo/app"]`) || !strings.Contains(c.Code, `globalThis.__papergoApp.modules["react/jsx-runtime"]`) {
		t.Fatalf("compiled code: %s", c.Code)
	}
	var schema map[string]any
	if err = json.Unmarshal(c.ParametersSchema, &schema); err != nil || schema["required"].([]any)[0] != "list" {
		t.Fatalf("parameters form: %s %v", c.ParametersSchema, err)
	}
	again, err := Compile(dashboard)
	if err != nil || again.Hash != c.Hash {
		t.Fatalf("compiling again changed the code: %v", err)
	}

	inline, err := Compile(`import { app } from "@papergo/app";
export default app({ render: () => "</script><b>" });`)
	if err != nil || strings.Contains(strings.ToLower(inline.Code), "</script") {
		t.Fatalf("code with a closing script tag in a string: %v %s", err, inline.Code)
	}

	plain, err := Compile(`import { app } from "@papergo/app";
export default app({ render: () => null });`)
	if err != nil || string(plain.ParametersSchema) != `{"type":"object","properties":{}}` {
		t.Fatalf("an app without parameters: %s %v", plain.ParametersSchema, err)
	}
}

func TestAppsThatDoNotCompileAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ source, want string }{
		"empty":           {"  ", "no source"},
		"too large":       {strings.Repeat("x", MaxSource+1), "limited to"},
		"another module":  {`import confetti from "canvas-confetti";` + "\nexport default confetti;", `not "canvas-confetti"`},
		"a relative file": {`import x from "./other";` + "\nexport default x;", `not "./other"`},
		"syntax":          {"export default app({ render: () => <div> });", "app.tsx:1:"},
		"no render":       {`import { app } from "@papergo/app"; export default app({});`, "app({ render })"},
		"no default":      {`export const x = 1;`, "app({ render })"},
		"bad parameters":  {`import { app } from "@papergo/app"; export default app({ parameters: [1], render: () => null });`, "parameters must be a form"},
		"a loop":          {`import { app } from "@papergo/app"; while (true) {} export default app({ render: () => null });`, "too long"},
		"a throw":         {`import { app } from "@papergo/app"; throw new Error("boom"); export default app({ render: () => null });`, "boom"},
	} {
		_, err := Compile(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if _, ok := err.(*CompileError); err != nil && !ok {
			t.Errorf("%s: not a CompileError: %T", name, err)
		}
	}
}
