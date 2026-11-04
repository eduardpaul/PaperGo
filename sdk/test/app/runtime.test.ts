// The app runtime's formats read and write numbers and dates in the
// viewer's preferences, and its context sends requests through the bridge,
// the generated client's included, and reads their answers.

import { Bridge, type AppMessage, type PageMessage } from "../../src/app/bridge.js";
import { appContext, RequestError, type Init } from "../../src/app/context.js";
import { formatter, wallClock } from "../../src/app/format.js";
import { flipSign, ticks } from "../../src/app/kit.js";
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

// The date fields pick and show dates on the viewer's clock, whatever the
// device's zone.
const madrid = wallClock("Europe/Madrid");
expect("Madrid's clock at an instant", madrid.at(new Date("2026-11-03T23:30:00Z")), { year: 2026, month: 11, day: 4, hour: 0, minute: 30 });
expect("noon in Madrid in winter", madrid.instant(2026, 11, 4, 12).toISOString(), "2026-11-04T11:00:00.000Z");
expect("noon in Madrid in summer", madrid.instant(2026, 7, 1, 12).toISOString(), "2026-07-01T10:00:00.000Z");
expect("the first minutes of a day in Madrid", madrid.instant(2026, 11, 4, 0, 30).toISOString(), "2026-11-03T23:30:00.000Z");
const york = wallClock("America/New_York");
expect("the day after the clocks go back", york.instant(2026, 11, 2, 0).toISOString(), "2026-11-02T05:00:00.000Z");
expect("a time the clocks skip is the time after", york.instant(2026, 3, 8, 2, 30).toISOString(), "2026-03-08T07:30:00.000Z");
expect("a time the clocks show twice is the first", york.instant(2026, 11, 1, 1, 30).toISOString(), "2026-11-01T05:30:00.000Z");
expect("just before Madrid's clocks go forward", madrid.instant(2026, 3, 29, 1, 30).toISOString(), "2026-03-29T00:30:00.000Z");
expect("an unknown zone is UTC", wallClock("Nowhere/Else").instant(2026, 11, 4, 12).toISOString(), "2026-11-04T12:00:00.000Z");

// Charts put their ticks on round values, and ± turns a typed number's sign.
expect("ticks of a percentage", ticks(0, 100), [0, 25, 50, 75, 100]);
expect("ticks of temperatures", ticks(-1.5, 26.3), [0, 10, 20]);
expect("ticks of prices", ticks(1.0, 1.69), [1, 1.2, 1.4, 1.6]);
expect("ticks below zero", ticks(-12, 7), [-10, -5, 0, 5]);
expect("one value", ticks(3, 3), [3]);
expect("± turns a number negative", flipSign("3,5"), "-3,5");
expect("± turns a plus", flipSign(" +3"), "-3");
expect("± turns a negative back", flipSign("-3"), "3");

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
