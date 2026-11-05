// The app runtime's formats read and write numbers and dates in the
// viewer's preferences, its kit speaks the viewer's language, and its
// context sends requests through the bridge, the generated client's
// included, and reads their answers.

import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server.browser";

import { Bridge, type AppMessage, type PageMessage } from "../../src/app/bridge.js";
import { appContext, RequestError, RuntimeContext, type Init } from "../../src/app/context.js";
import { formatter, wallClock } from "../../src/app/format.js";
import { aggregateRequest, cannotMatch, concerns, queryRequest, readPages } from "../../src/app/hooks.js";
import { BarChart, flipSign, kitWords, Modal, NumberField, Table, ticks } from "../../src/app/kit.js";
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
expect("an exponent, as PaperGo's fields read one", comma.parseDecimal("1,5e3"), "1500");
expect("a negative exponent", comma.parseDecimal("1.5e-2"), "0.015");
expect("a large exponent", comma.parseDecimal("1e999999"), undefined);
expect("spaces within a number", comma.parseDecimal("1 000"), undefined);
expect("exact digits", comma.parseDecimal("9007199254740993,50"), "9007199254740993.5");
expect("negative zero", comma.parseDecimal("-0,00"), "0");
expect("a decimal in a comma format", comma.decimal("93.18", 1), "93,2");
expect("a date in the viewer's zone and format", comma.date("2026-11-03T23:30:00Z"), "04/11/2026");
const honolulu = formatter({ language: "en", time_zone: "Pacific/Honolulu", date_format: "yyyy-MM-dd", time_format: "24h", number_format: "en" });
expect("a day without a time stays on its day", [honolulu.date("2026-10-01"), honolulu.dateTime("2026-10-01")], ["2026-10-01", "2026-10-01"]);
expect("a decimal keeps its digits", comma.decimal("9007199254740993.5", 1), "9.007.199.254.740.993,5");
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

// Hooks read what the API serves: compact items on their own route, groups by
// their own definitions, and every page of a chart's groups.
expect("a compact query", queryRequest("l/1", { contentTypeId: "t" }, { compact: true, select: ["a"] }), { url: "/v1/resources/l%2F1/query/compact?$top=500", body: { query: { content_type_id: "t" }, select: ["a"] } });
expect("a full query", queryRequest("l", {}, { top: 1 }), { url: "/v1/resources/l/query?$top=1", body: { query: {} } });
expect(
  "aggregates by day",
  aggregateRequest("l", { query: { contentTypeId: "t" }, groups: [{ field: "at", interval: "day", timeZone: "Europe/Madrid" }, { field: "kind" }], aggregates: [{ op: "avg", field: "c" }], sort: [{ field: "$count", direction: "desc" }] }, undefined),
  { url: "/v1/resources/l/query/aggregates?$top=500", body: { query: { content_type_id: "t" }, aggregates: [{ op: "avg", field: "c" }], groups: [{ field: "at", interval: "day", time_zone: "Europe/Madrid" }, { field: "kind" }], sort: [{ field: "$count", direction: "desc" }] } },
);
const served: string[] = [];
const pagesOf: Record<string, { value: number[]; "@odata.nextLink": string | null }> = { first: { value: [1, 2], "@odata.nextLink": "second" }, second: { value: [3], "@odata.nextLink": null } };
const serve = async <T,>(_method: "POST", url: string) => {
  served.push(url);
  return { data: pagesOf[url] as T };
};

// Charts put their ticks on round values, and ± turns a typed number's sign.
expect("ticks of a percentage", ticks(0, 100), [0, 25, 50, 75, 100]);
expect("ticks of temperatures", ticks(-1.5, 26.3), [0, 10, 20]);
expect("ticks of prices", ticks(1.0, 1.69), [1, 1.2, 1.4, 1.6]);
expect("ticks below zero", ticks(-12, 7), [-10, -5, 0, 5]);
expect("one value", ticks(3, 3), [3]);
expect("± turns a number negative", flipSign("3,5"), "-3,5");
expect("± turns a plus", flipSign(" +3"), "-3");
expect("± turns a negative back", flipSign("-3"), "3");
expect("every page", await readPages<number>(serve, "first", {}, true), [1, 2, 3]);
expect("every page's request", served.splice(0), ["first", "second"]);
expect("one page", await readPages<number>(serve, "first", {}, false), [1, 2]);
expect("one page's request", served.splice(0), ["first"]);

// A link read already ends the reading; a replaced request stops it; figures
// that changed between pages are read again from the first page.
const looping = async <T,>(_method: "POST", url: string) => ({ data: { value: [url], "@odata.nextLink": "again" } as T });
expect("a repeated link", await readPages<string>(looping, "first", {}, true).then(() => "read", (e: Error) => e.message), "the server linked a page read already: again");
let reads = 0;
const replaced = async <T,>() => (reads++, { data: { value: [reads], "@odata.nextLink": "next" + reads } as T });
expect("a replaced request", await readPages<number>(replaced, "first", {}, true, () => reads >= 2).then(() => "read", (e: Error) => e.name), "Superseded");
expect("pages read before it stopped", reads, 2);
let changes = 1;
const changing = async <T,>(_method: "POST", url: string) => {
  if (url === "second" && changes-- > 0) throw new RequestError(409, "figures_changed", "changed", {});
  return { data: pagesOf[url] as T };
};
expect("figures read again from the first page", await readPages<number>(changing, "first", {}, true), [1, 2, 3]);

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

// A live query reads again for what may be in it: a query names a type with
// its subtypes, and the list's own copy of it counts as the type.
const types = [
  { id: "copy", parent_id: "invoice", collection_id: "l1", ancestor_ids: ["invoice"] },
  { id: "credit", parent_id: "credit-note", collection_id: "l1", ancestor_ids: ["credit-note", "invoice"] },
  { id: "reading", parent_id: "measure", collection_id: "l1", ancestor_ids: ["measure"] },
];
expect("a list's copy of the type", cannotMatch(types, "invoice", "copy"), false);
expect("a subtype", cannotMatch(types, "invoice", "credit"), false);
expect("the type named by the list's copy", cannotMatch(types, "copy", "credit"), false);
expect("another type", cannotMatch(types, "invoice", "reading"), true);
expect("a type the list did not have when read", cannotMatch(types, "invoice", "new"), false);
expect("a query of any type", cannotMatch(types, undefined, "reading"), false);
expect("an item without a type", cannotMatch(types, "invoice", undefined), false);
expect("before the types are read", cannotMatch(undefined, "invoice", "reading"), false);

// A change concerns a query where the item is now or where the page saw it.
const of = (change: Parameters<typeof concerns>[3]) => concerns("l1", "invoice", types, change);
expect("an item of the type in the list", of({ id: "a", collectionId: "l1", contentTypeId: "copy" }), true);
expect("an item that left the list", of({ id: "a", collectionId: "l2", contentTypeId: "copy", from: { collectionId: "l1", contentTypeId: "copy" } }), true);
expect("an item that changed to another type", of({ id: "a", collectionId: "l1", contentTypeId: "reading", from: { collectionId: "l1", contentTypeId: "copy" } }), true);
expect("an item of another type that stays one", of({ id: "a", collectionId: "l1", contentTypeId: "reading", from: { collectionId: "l1", contentTypeId: "reading" } }), false);
expect("an item of the list the page had not seen", of({ id: "a", collectionId: "l1", contentTypeId: "reading" }), true);
expect("a new item of another type", of({ id: "a", collectionId: "l1", contentTypeId: "reading", created: true }), false);
expect("an item removed from another list", of({ id: "a", collectionId: "l2", removed: true }), false);

// The kit's own words follow the viewer's language: Spanish for any es
// language, English otherwise and outside an app.
expect("Spanish words", kitWords("es-MX").close, "Cerrar");
expect("English words", [kitWords("en").close, kitWords("fr").empty, kitWords(undefined).noData], ["Close", "Nothing yet", "No data yet"]);
const kit = (language: string) => {
  const viewer = appContext<{ list: string }>({ ...init, user: { ...init.user, preferences: { ...init.user.preferences, language } } }, bridge);
  return renderToStaticMarkup(
    createElement(RuntimeContext.Provider, { value: { ctx: viewer, bridge } as never }, [
      createElement(Modal, { key: "m", open: true, title: "Lines", onClose: () => {} }),
      createElement(Table, { key: "t", columns: [], rows: [] }),
      createElement(Table, { key: "own", columns: [], rows: [], empty: "No receipts" }),
      createElement(BarChart, { key: "b", data: [] }),
      createElement(NumberField, { key: "n", label: "Outdoor", value: "3", onChange: () => {}, signed: true }),
    ]),
  );
};
const spanish = kit("es");
for (const said of ['aria-label="Cerrar"', "Aún no hay nada", "No receipts", "Aún no hay datos", 'aria-label="Cambiar el signo"']) expect(`Spanish kit says ${said}`, spanish.includes(said), true);
const english = kit("en");
for (const said of ['aria-label="Close"', "Nothing yet", "No data yet", 'aria-label="Change the sign"']) expect(`English kit says ${said}`, english.includes(said), true);
expect("no English in the Spanish kit", /Close|Nothing yet|No data yet/.test(spanish), false);

if (failed) process.exit(1);
console.log("the app runtime reads formats, speaks the viewer's language and sends requests through the page");
// React's renderer keeps a message channel open; the test is done.
process.exit(0);
