// The app runtime's formats read and write numbers and dates in the
// viewer's preferences, and its context sends requests through the bridge,
// the generated client's included, and reads their answers.

import { Bridge, type AppMessage, type PageMessage } from "../../src/app/bridge.js";
import { appContext, RequestError, type Init } from "../../src/app/context.js";
import { formatter } from "../../src/app/format.js";
import { json } from "../../src/values.js";

declare const process: { exit(code: number): never };

let failed = false;
function expect(what: string, got: unknown, want: unknown): void {
  if (JSON.stringify(got) === JSON.stringify(want)) return;
  console.error(`${what}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  failed = true;
}

const comma = formatter({ language: "es", time_zone: "Europe/Madrid", date_format: "dd/MM/yyyy", time_format: "24h", number_format: "de" });
expect("41,5 in a comma format", comma.parseDecimal("41,5"), "41.5");
expect("a dot is always a decimal point", comma.parseDecimal(" 41.50 "), "41.5");
expect("both separators", comma.parseDecimal("1.041,5"), undefined);
expect("not a number", comma.parseDecimal("4x"), undefined);
expect("a whole number", comma.parseDecimal("007"), "7");
expect("a fraction", comma.parseDecimal(",5"), "0.5");
expect("a negative", comma.parseDecimal("-0,25"), "-0.25");
expect("a decimal in a comma format", comma.decimal("93.18", 1), "93,2");
expect("a date in the viewer's zone and format", comma.date("2026-11-03T23:30:00Z"), "04/11/2026");
expect("a date and time", comma.dateTime("2026-11-03T23:30:00Z"), "04/11/2026 00:30");
const dot = formatter({ language: "en", time_zone: "UTC", date_format: "yyyy-MM-dd", time_format: "12h", number_format: "en" });
expect("41,5 in a dot format", dot.parseDecimal("41,5"), undefined);
expect("a 12-hour time", dot.dateTime("2026-11-03T13:05:00Z"), "2026-11-03 1:05 PM");
expect("a number", dot.number(1234.567), "1,234.57");
expect("days ago", dot.relative("2026-11-01T00:00:00Z", new Date("2026-11-04T00:00:00Z")), "3 days ago");

// A page that answers every request with what answer returns.
function page(answer: (m: Extract<AppMessage, { type: "papergo:request" }>) => { status: number; body: unknown }) {
  const sent: AppMessage[] = [];
  let listener: ((m: PageMessage) => void) | undefined;
  const bridge = new Bridge({
    post(message) {
      sent.push(message);
      if (message.type !== "papergo:request") return;
      const r = answer(message);
      const body = r.body === undefined ? "" : JSON.stringify(r.body);
      queueMicrotask(() => listener?.({ type: "papergo:response", id: message.id, response: { status: r.status, headers: { "content-type": "application/json" }, body } }));
    },
    listen(handler) {
      listener = handler;
    },
  });
  return { bridge, sent };
}

const init: Init = {
  app: { id: "a1", key: "diesel", name: "Diesel tracker", version: 2 },
  params: { list: "l1" },
  user: { subject: "bob", name: "Bob", preferences: { language: "en", time_zone: "UTC", date_format: "yyyy-MM-dd", time_format: "24h", number_format: "en" } },
  workspace: { id: "w1", name: "Farm", role: "member" },
  theme: "light",
};

const { bridge, sent } = page((m) => {
  if (m.url.startsWith("/v1/me/tasks")) return { status: 200, body: { value: [{ id: "t1" }] } };
  if (m.url === "/v1/resources/l1/children") return { status: 201, body: { id: "6a1f8e9c-1b2d-4c3e-9f00-112233445566", name: "Reading", values: JSON.parse(String(m.body)).values } };
  return { status: 404, body: { code: "not_found", detail: "resource not found" } };
});
const ctx = appContext<{ list: string }>(init, bridge);
expect("params", ctx.params.list, "l1");

const tasks = await ctx.request<{ value: { id: string }[] }>("get", "/v1/me/tasks?key=diesel_measure&status=open");
expect("a raw request", [tasks.status, tasks.data.value[0].id], [200, "t1"]);
const request = sent.find((m) => m.type === "papergo:request") as Extract<AppMessage, { type: "papergo:request" }>;
expect("its method, path and accept header", [request.method, request.url, request.headers.accept], ["GET", "/v1/me/tasks?key=diesel_measure&status=open", "application/json"]);

const created = await ctx.api.v1.resources.byId("l1").children.post({ kind: "item", name: "Reading", values: json({ depth_cm: "41.5" }) } as never);
expect("the generated client goes through the page without its origin", (sent.at(-1) as { url: string }).url, "/v1/resources/l1/children");
expect("its answer", String((created as { id?: unknown }).id), "6a1f8e9c-1b2d-4c3e-9f00-112233445566");

try {
  await ctx.request("GET", "/v1/resources/missing");
  expect("a refused request throws", true, false);
} catch (e) {
  expect("a refused request", e instanceof RequestError ? [e.status, e.code, e.detail] : String(e), [404, "not_found", "resource not found"]);
}

ctx.open({ itemId: "i1" });
expect("open asks the page", sent.at(-1), { type: "papergo:open", target: { itemId: "i1" } });

let seen: unknown;
const stop = bridge.watch((changes) => (seen = changes));
stop();
expect("a stopped watcher hears nothing", seen, undefined);

if (failed) process.exit(1);
console.log("the app runtime reads formats and sends requests through the page");
