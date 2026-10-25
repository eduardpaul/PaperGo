// Every paged list a script can call has all(), and all() follows
// @odata.nextLink through the request adapter until the last page.

import type { NavigationMetadata } from "@microsoft/kiota-abstractions";
import { createPaperGoClient, PaperGoClientNavigationMetadata } from "../src/client/paperGoClient.js";
import { HostRequestAdapter } from "../src/adapter.js";
import type { HostResponse } from "../src/host.js";
import { withPages } from "../src/pages.js";
import openapi from "../../api/openapi.json";
import scope from "../scope.json";

declare const process: { exit(code: number): never };

let failed = false;
function expect(what: string, got: unknown, want: unknown): void {
  if (JSON.stringify(got) === JSON.stringify(want)) return;
  console.error(`${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  failed = true;
}

const excluded = scope.exclude.map(
  (glob) => new RegExp(`^${glob.replace(/[$.]/g, "\\$&").replace(/\*\*/g, "\0").replace(/\*/g, "[^/]+").replace(/\0/g, ".+")}$`),
);
type Operation = { parameters?: { name?: string }[] };
const want: string[] = [];
for (const [path, item] of Object.entries(openapi.paths as Record<string, Record<string, Operation>>)) {
  if (excluded.some((glob) => glob.test(path))) continue;
  const common = (item as { parameters?: { name?: string }[] }).parameters ?? [];
  const methods = ["get", "post"].filter((method) => [...common, ...(item[method]?.parameters ?? [])].some((p) => p.name === "$skiptoken"));
  if (methods.length) want.push(`${methods.includes("post") ? "POST" : "GET"} ${path}`);
}

// The builders all() reaches, by the method it sends and the path.
const got: string[] = [];
function walk(navigation: Record<string, NavigationMetadata> | undefined): void {
  for (const next of Object.values(navigation ?? {})) {
    for (const method of ["post", "get"] as const) {
      const metadata = next.requestsMetadata?.[method];
      if (metadata?.queryParametersMapper?.skiptoken === undefined) continue;
      got.push(`${method.toUpperCase()} ${metadata.uriTemplate.replace("{+baseurl}", "").replace(/\{\?.*$/, "")}`);
      break;
    }
    walk(next.navigationMetadata);
  }
}
walk(PaperGoClientNavigationMetadata);
expect("paged lists", got.sort(), want.sort());

const requests: { method: string; url: string; body?: string }[] = [];
let pages: Record<string, unknown>[] = [];
(globalThis as Record<string, unknown>).__papergo = {
  request(method: string, url: string, _headers: Record<string, string>, body: string | undefined): HostResponse {
    requests.push({ method, url, body });
    return { status: 200, headers: { "content-type": "application/json" }, body: JSON.stringify(pages.shift()) };
  },
};
const api = withPages(createPaperGoClient(new HostRequestAdapter()), PaperGoClientNavigationMetadata);

pages = [
  { value: [{ id: "a" }, { id: "b" }], "@odata.nextLink": "http:///v1/term-sets/s/terms?%24skiptoken=t1&%24top=2&q=x" },
  { value: [], "@odata.nextLink": "https://papergo.example/v1/term-sets/s/terms?%24skiptoken=t2&%24top=2&q=x" },
  { value: [{ id: "c" }], "@odata.nextLink": null },
];
const terms = await api.v1.termSets.byId("s").terms.all({ queryParameters: { q: "x", top: 2 } });
expect("terms", terms.map((term) => term.id), ["a", "b", "c"]);
expect("term requests", requests.splice(0), [
  { method: "GET", url: "http://papergo.workflow/v1/term-sets/s/terms?%24top=2&q=x" },
  { method: "GET", url: "http://papergo.workflow/v1/term-sets/s/terms?%24skiptoken=t1&%24top=2&q=x" },
  { method: "GET", url: "http://papergo.workflow/v1/term-sets/s/terms?%24skiptoken=t2&%24top=2&q=x" },
]);

pages = [
  { value: [{ name: "a" }], total: 2, "@odata.nextLink": "http:///v1/resources/c/query?%24skiptoken=t1" },
  { value: [{ name: "b" }], total: 2, "@odata.nextLink": null },
];
const items = await api.v1.resources.byId("c").query.all({ surface: "head" });
expect("items", items.map((item) => item.name), ["a", "b"]);
expect("query requests", requests.splice(0), [
  { method: "POST", url: "http://papergo.workflow/v1/resources/c/query", body: '{"surface":"head"}' },
  { method: "POST", url: "http://papergo.workflow/v1/resources/c/query?%24skiptoken=t1", body: '{"surface":"head"}' },
]);

pages = [{ value: [{ id: "a" }], "@odata.nextLink": null }];
await api.v1.search.all({ q: "invoice" });
expect("search sends", requests.splice(0).map((request) => request.method), ["POST"]);

// Builders reached through withUrl keep all().
pages = [{ value: [{ id: "a" }], "@odata.nextLink": null }];
const reached = api.v1.termSets.byId("s").terms.withUrl("http://papergo.workflow/v1/term-sets/s/terms?%24top=1");
expect("withUrl", (await reached.all()).map((term) => term.id), ["a"]);
expect("withUrl request", requests.splice(0).map((request) => request.url), ["http://papergo.workflow/v1/term-sets/s/terms?%24top=1"]);

if (failed) process.exit(1);
console.log(`all() reads every page of the ${want.length} paged lists`);
