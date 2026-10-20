// HostParseNode must read every model exactly as Kiota's JsonParseNode
// does. Each model's fields get values of every JSON type, alone and with
// unknown keys beside them, read as an object and as a collection; both
// nodes must give equal models or the same error.

import { JsonParseNode } from "@microsoft/kiota-serialization-json";
import { DateOnly, Duration, TimeOnly, type Parsable, type ParsableFactory } from "@microsoft/kiota-abstractions";
import * as models from "../src/client/models/index.js";
import { HostParseNode } from "../src/parsenode.js";

declare const process: { exit(code: number): never };

const samples: unknown[] = [
  "text", "", 42, 0, -1.5, true, false, null,
  "2026-10-20T10:00:00Z", "2026-10-20", "10:30:00", "P1DT2H", "not a date",
  "0b0e3e2a-6f39-4a39-9a0e-2a7b5d1c9f01", "item", "read",
  [], ["a", "b"], [1, 2], [true], [null, "a"], ["read", "unknown"],
  [{ id: "x", name: "n", kind: "item" }], {}, { id: "x", nested: { a: 1, b: [1, "two"] } },
];

function normalize(x: unknown): unknown {
  if (x instanceof Date) return { date: Number.isNaN(x.getTime()) ? "invalid" : x.toISOString() };
  if (x instanceof DateOnly || x instanceof TimeOnly || x instanceof Duration) return { [x.constructor.name]: String(x) };
  if (x instanceof ArrayBuffer) return { bytes: Array.from(new Uint8Array(x)) };
  if (Array.isArray(x)) return x.map(normalize);
  if (typeof x === "function") return "function " + x.name;
  if (x && typeof x === "object") {
    const out: Record<string, unknown> = { "@": Object.getPrototypeOf(x)?.constructor?.name };
    for (const key of Object.keys(x).sort()) out[key] = normalize((x as Record<string, unknown>)[key]);
    if (typeof (x as { getValue?: unknown }).getValue === "function") out["@value"] = normalize((x as { getValue: () => unknown }).getValue());
    return out;
  }
  return x === undefined ? "undefined" : x;
}

function outcome(read: () => unknown): string {
  try {
    return JSON.stringify(normalize(read()));
  } catch (e) {
    return "error: " + (e as Error).message;
  }
}

const clone = (v: unknown): unknown => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));

let cases = 0;
const failures: string[] = [];
function compare(name: string, json: unknown, factory: ParsableFactory<Parsable>) {
  for (const shape of ["object", "collection"] as const) {
    const value = shape === "object" ? json : [json, json];
    const kiota = outcome(() => {
      const node = new JsonParseNode(clone(value), undefined);
      return shape === "object" ? node.getObjectValue(factory) : node.getCollectionOfObjectValues(factory);
    });
    const host = outcome(() => {
      const node = new HostParseNode(clone(value), undefined);
      return shape === "object" ? node.getObjectValue(factory) : node.getCollectionOfObjectValues(factory);
    });
    cases++;
    if (kiota !== host && failures.length < 20) failures.push(`${name} ${shape} ${JSON.stringify(value)}\n  kiota: ${kiota}\n  host:  ${host}`);
  }
}

const factories = Object.entries(models).filter(([name, f]) => /^create\w+FromDiscriminatorValue$/.test(name) && typeof f === "function") as [string, ParsableFactory<Parsable>][];
for (const [name, factory] of factories) {
  const fields = Object.keys(factory(new JsonParseNode({}, undefined))({}));
  for (const sample of samples) {
    compare(name, sample, factory);
    for (const field of fields) {
      compare(`${name}.${field}`, { [field]: sample }, factory);
      compare(`${name}.${field}+extra`, { [field]: sample, extra_key: sample }, factory);
    }
  }
  compare(`${name} all fields`, Object.fromEntries(fields.map((f, i) => [f, samples[i % samples.length]])), factory);
}

if (failures.length) {
  console.error(failures.join("\n"));
  console.error(`${failures.length}+ of ${cases} cases differ`);
  process.exit(1);
}
console.log(`HostParseNode equals JsonParseNode in ${cases} cases over ${factories.length} models`);
