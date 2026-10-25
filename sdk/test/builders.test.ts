// The SDK's builders must make every request Kiota's builders make. Every
// request of the client is made both ways, with query parameters of every
// kind, headers and bodies; the requests the host receives and what the
// calls return or throw must be equal.

import { MultipartBody, type NavigationMetadata, type RequestMetadata, type RequestsMetadata } from "@microsoft/kiota-abstractions";
import { createPaperGoClient, PaperGoClientNavigationMetadata, type PaperGoClient } from "../src/client/paperGoClient.js";
import { HostRequestAdapter } from "../src/adapter.js";
import type { HostResponse } from "../src/host.js";
import { client } from "../src/builders.js";

declare const process: { exit(code: number): never };

type Step = { name: string; args: unknown[] };
type Sent = { method: string; url: string; headers: Record<string, string>; body: string | number[] | undefined };

let sent: Sent[] = [];
let response: HostResponse = { status: 204, headers: {}, body: "" };
(globalThis as Record<string, unknown>).__papergo = {
  request(method: string, url: string, headers: Record<string, string>, body: string | ArrayBuffer | undefined): HostResponse {
    sent.push({ method, url, headers, body: body instanceof ArrayBuffer ? Array.from(new Uint8Array(body)) : body });
    return response;
  },
};

const kiotaAdapter = new HostRequestAdapter();
const kiota = createPaperGoClient(kiotaAdapter) as unknown as Record<string, unknown>;
const adapter = new HostRequestAdapter();
createPaperGoClient(adapter);
const ours = client<PaperGoClient>(adapter, PaperGoClientNavigationMetadata) as unknown as Record<string, unknown>;

function at(root: Record<string, unknown>, path: Step[]): Record<string, unknown> {
  let b = root;
  for (const step of path) {
    const next = b[step.name];
    b = (typeof next === "function" ? (b[step.name] as (...a: unknown[]) => unknown).call(b, ...step.args) : next) as Record<string, unknown>;
  }
  return b;
}

const date = new Date("2026-10-20T10:00:00Z");
// Query values cycle through every kind Kiota's RequestInformation handles.
const samples: unknown[] = ["text with spaces/and?&", 42, true, date, ["a", "b c"], undefined, 0, false, { ignored: true }];

function configs(metadata: RequestMetadata): object[] {
  const query: Record<string, unknown> = {};
  Object.keys(metadata.queryParametersMapper ?? {}).forEach((key, i) => (query[key] = samples[i % samples.length]));
  return [
    { headers: { "If-Match": '"3"', "x-several": ["a", "b"], "Content-Type": "text/plain" }, queryParameters: query },
    { queryParameters: Object.fromEntries(Object.keys(query).map((key) => [key, "only"])) },
  ];
}

function bodies(metadata: RequestMetadata): unknown[] {
  if (metadata.requestInformationContentSetMethod === "setStreamContent") return [new Uint8Array([1, 2, 3]).buffer];
  if (!metadata.requestBodySerializer) return [undefined];
  if (metadata.requestBodyContentType === "multipart/form-data") return [new MultipartBody()];
  return [{}, { additionalData: { extra: 1 } }];
}

function responseFor(metadata: RequestMetadata): HostResponse {
  switch (metadata.adapterMethodName) {
    case "send":
      return { status: 200, headers: { "content-type": "application/json" }, body: '{"id":"x","name":"n","value":[]}' };
    case "sendCollection":
    case "sendCollectionOfPrimitive":
      return { status: 200, headers: { "content-type": "application/json" }, body: '["a"]' };
    case "sendPrimitive":
      return { status: 200, headers: { "content-type": "application/octet-stream" }, body: "", bytes: new Uint8Array([9]).buffer };
    default:
      return { status: 204, headers: {}, body: "" };
  }
}

async function outcome(root: Record<string, unknown>, path: Step[], method: string, args: unknown[]): Promise<string> {
  sent = [];
  let result: unknown;
  try {
    const builder = at(root, path);
    result = await (builder[method] as (...a: unknown[]) => Promise<unknown>).call(builder, ...args);
  } catch (e) {
    result = { error: (e as Error).message };
  }
  return JSON.stringify({ sent, result: result instanceof ArrayBuffer ? Array.from(new Uint8Array(result)) : result });
}

let compared = 0;
let failed = false;
async function walk(path: Step[], navigation: Record<string, NavigationMetadata> | undefined, requests: RequestsMetadata | undefined): Promise<void> {
  for (const [method, metadata] of Object.entries(requests ?? {}) as [string, RequestMetadata][]) {
    response = responseFor(metadata);
    const calls: unknown[][] = method === "get" ? [[], ...configs(metadata).map((c) => [c])] : [[], ...bodies(metadata).flatMap((b) => [[b], ...configs(metadata).map((c) => [b, c])])];
    if (metadata.requestInformationContentSetMethod === "setStreamContent") calls.push([new Uint8Array([4]).buffer, "image/png", {}]);
    for (const args of calls) {
      const want = await outcome(kiota, path, method, args);
      const got = await outcome(ours, path, method, args);
      compared++;
      if (want !== got) {
        console.error(`${path.map((s) => s.name).join(".")}.${method}(${JSON.stringify(args)}):\n  kiota ${want}\n  sdk   ${got}`);
        failed = true;
      }
    }
  }
  for (const [name, next] of Object.entries(navigation ?? {})) {
    const args = (next.pathParametersMappings ?? []).map((m, i) => `${m}-${i} value`);
    await walk([...path, { name, args }], next.navigationMetadata, next.requestsMetadata);
  }
}

await walk([], PaperGoClientNavigationMetadata, undefined);

// withUrl gives a builder for a raw URL, whose requests and navigation use it.
response = { status: 200, headers: { "content-type": "application/json" }, body: "{}" };
for (const root of [kiota, ours]) {
  const sets = at(root, [{ name: "v1", args: [] }, { name: "termSets", args: [] }]);
  const builder = (sets.withUrl as (u: string) => Record<string, unknown>).call(sets, "http://papergo.workflow/v1/term-sets?%24top=1");
  sent = [];
  await (builder.get as () => Promise<unknown>).call(builder);
  if (sent[0]?.url !== "http://papergo.workflow/v1/term-sets?%24top=1") {
    console.error("withUrl request", sent);
    failed = true;
  }
}

if (failed) process.exit(1);
console.log(`the SDK's builders make the requests Kiota's make in ${compared} calls`);
