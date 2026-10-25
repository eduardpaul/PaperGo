// The generated client's request builders without Proxies. Kiota builds
// each builder as a Proxy that interprets its metadata on every property
// read and makes every request through a RequestInformation, whose headers
// and options are Proxies too. A replay makes every recorded request again,
// so here each kind of builder is a plain prototype, made once from the same
// metadata, and requests go to the adapter as method, URL, headers and body.
// They are the requests Kiota's builders make: test/builders.test.ts sends
// every request of the client both ways and compares them.
//
// A list request is paged when its query takes $skiptoken; its builder also
// has all(), which scripts/generate.mjs declares. Where both its GET and
// POST are paged, all() posts.

import {
  DateOnly,
  Duration,
  HttpMethod,
  MultipartBody,
  RequestInformation,
  TimeOnly,
  type NavigationMetadata,
  type RequestConfiguration,
  type RequestMetadata,
  type RequestsMetadata,
  type SerializationWriter,
} from "@microsoft/kiota-abstractions";
import { baseUrl, type HostRequestAdapter } from "./adapter.js";
import { expand } from "./uritemplate.js";

// The request methods builders can have, as RequestsMetadata names them.
const methods = ["get", "post", "put", "patch", "delete", "head", "options"] as const;
type Method = (typeof methods)[number];

const httpMethods: Record<Method, HttpMethod> = {
  get: HttpMethod.GET, post: HttpMethod.POST, put: HttpMethod.PUT, patch: HttpMethod.PATCH,
  delete: HttpMethod.DELETE, head: HttpMethod.HEAD, options: HttpMethod.OPTIONS,
};

// Kiota's path parameter for a builder that withUrl made.
const rawUrl = "request-raw-url";

const state = Symbol("builder");

interface State {
  adapter: HostRequestAdapter;
  // Never changed once a builder has it, so builders share it.
  params: Record<string, unknown>;
}

interface Builder {
  [state]: State;
}

type Navigation = Record<string, NavigationMetadata> | undefined;

/** The root builder of the generated client, PaperGoClient. */
export function client<T>(adapter: HostRequestAdapter, navigation: Record<string, NavigationMetadata>): T {
  return make(navigation, undefined, { adapter, params: { baseurl: baseUrl } }) as T;
}

const empty = {};
// Prototypes by navigation metadata, then requests metadata: each builder
// type has its own.
const prototypes = new WeakMap<object, WeakMap<object, object>>();

function make(navigation: Navigation, requests: RequestsMetadata | undefined, s: State): Builder {
  const byRequests = prototypes.get(navigation ?? empty) ?? new WeakMap<object, object>();
  prototypes.set(navigation ?? empty, byRequests);
  let proto = byRequests.get(requests ?? empty);
  if (!proto) {
    proto = prototype(navigation, requests);
    byRequests.set(requests ?? empty, proto);
  }
  const builder = Object.create(proto) as Builder;
  builder[state] = s;
  return builder;
}

const paged = (metadata: RequestMetadata | undefined) => metadata?.queryParametersMapper?.skiptoken !== undefined;

function prototype(navigation: Navigation, requests: RequestsMetadata | undefined): object {
  const proto: Record<string, unknown> = {};
  for (const method of methods) {
    const metadata = requests?.[method];
    if (!metadata) continue;
    proto[method] = function (this: Builder, ...args: unknown[]) {
      const s = this[state];
      const r = request(s, metadata, method, args);
      return s.adapter.call(metadata, httpMethods[method], r.url, r.headers, r.content);
    };
    const name = `to${method[0].toUpperCase()}${method.slice(1)}RequestInformation`;
    proto[name] = function (this: Builder, ...args: unknown[]) {
      return information(this[state], metadata, method, args);
    };
  }
  const pages = paged(requests?.post) ? "post" : paged(requests?.get) ? "get" : undefined;
  if (pages) {
    proto.all = function (this: Builder, ...args: unknown[]) {
      return all(this, pages, args);
    };
  }
  proto.withUrl = function (this: Builder, url: string) {
    if (!url) throw new Error("rawUrl cannot be undefined");
    return make(navigation, requests, { adapter: this[state].adapter, params: { [rawUrl]: url } });
  };
  for (const [name, next] of Object.entries(navigation ?? {})) {
    const mappings = next.pathParametersMappings ?? [];
    if (mappings.length === 0) {
      Object.defineProperty(proto, name, {
        get(this: Builder) {
          return make(next.navigationMetadata, next.requestsMetadata, this[state]);
        },
      });
      continue;
    }
    proto[name] = function (this: Builder, ...ids: unknown[]) {
      const s = this[state];
      const params = { ...s.params };
      ids.forEach((id, i) => {
        if (id === undefined || id === null || id === "") throw new Error(`path parameter ${mappings[i]} cannot be undefined`);
        params[mappings[i]] = id;
      });
      return make(next.navigationMetadata, next.requestsMetadata, { adapter: s.adapter, params });
    };
  }
  return proto;
}

// The arguments of a request method as Kiota's builders read them: get
// takes a configuration; the others a body first and the configuration
// last, and a stream body's media type between them.
function argumentsOf(metadata: RequestMetadata, method: Method, args: unknown[]) {
  if (method === "get") return { body: undefined, mediaType: undefined, config: args[0] as RequestConfiguration<object> | undefined };
  const mediaType = args.length > 2 && !metadata.requestBodySerializer && metadata.requestInformationContentSetMethod === "setStreamContent" && typeof args[1] === "string" ? args[1] : undefined;
  return { body: args[0], mediaType, config: args[args.length - 1] as RequestConfiguration<object> | undefined };
}

// Header values by name, as Kiota's Headers keeps them: names compare
// without case, values are sets in the order they were added, and an
// exactly named Content-Type, Content-Encoding or Content-Length holds one.
class HeaderValues {
  private readonly values = new Map<string, Set<string>>();

  add(name: string, values: string[]): void {
    if (values.length === 0) return;
    const key = name.toLowerCase();
    const set = this.values.get(key);
    if (name === "Content-Type" || name === "Content-Encoding" || name === "Content-Length") this.values.set(key, new Set([values[0]]));
    else if (set) for (const value of values) set.add(value);
    else this.values.set(key, new Set(values));
  }
  tryAdd(name: string, value: string): void {
    const key = name.toLowerCase();
    if (!this.values.has(key)) this.values.set(key, new Set([value]));
  }
  get(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const [name, values] of this.values) out[name] = Array.from(values).join(", ");
    return out;
  }
}

function request(s: State, metadata: RequestMetadata, method: Method, args: unknown[]) {
  const { body, mediaType, config } = argumentsOf(metadata, method, args);
  const headers = new HeaderValues();
  const query: Record<string, unknown> = {};
  if (config) {
    if (config.headers) {
      for (const name of Object.keys(config.headers)) {
        const value = config.headers[name];
        headers.add(name, Array.isArray(value) ? value : [value]);
      }
    }
    queryParameters(query, config.queryParameters as Record<string, unknown> | undefined, metadata.queryParametersMapper);
  }
  if (metadata.responseBodyContentType) headers.tryAdd("Accept", metadata.responseBodyContentType);
  let content: ArrayBuffer | undefined;
  if (metadata.requestBodySerializer) {
    if (!body) throw new Error("body cannot be undefined");
    if (typeof metadata.requestBodySerializer !== "function") throw new Error("workflow requests have no scalar bodies");
    let contentType = metadata.requestBodyContentType ?? mediaType;
    if (!contentType) throw new Error("contentType cannot be undefined");
    const writer: SerializationWriter = s.adapter.getSerializationWriterFactory().getSerializationWriter(contentType);
    if (body instanceof MultipartBody) contentType += "; boundary=" + body.getBoundary();
    if (Array.isArray(body)) writer.writeCollectionOfObjectValues(undefined, body, metadata.requestBodySerializer);
    else writer.writeObjectValue(undefined, body as never, metadata.requestBodySerializer);
    headers.tryAdd("Content-Type", contentType);
    content = writer.getSerializedContent();
  } else if (metadata.requestInformationContentSetMethod === "setStreamContent") {
    if (!body) throw new Error("body cannot be undefined");
    headers.tryAdd("Content-Type", metadata.requestBodyContentType ?? mediaType ?? "application/octet-stream");
    content = body as ArrayBuffer;
  }
  const raw = s.params[rawUrl];
  const url = typeof raw === "string" ? raw : expand(metadata.uriTemplate, { ...query, ...s.params });
  return { url, headers: headers.get(), content };
}

// queryParameters sets the query of a configuration as Kiota's
// RequestInformation does: by the names the mapper gives, with dates and
// durations as text, and other objects left out.
function queryParameters(out: Record<string, unknown>, q: Record<string, unknown> | undefined, mapper: Record<string, string> | undefined): void {
  if (q === null || q === undefined) return;
  for (const [k, v] of Object.entries(q)) {
    const key = mapper?.[k] || k;
    if (typeof v === "boolean" || typeof v === "number" || typeof v === "string" || Array.isArray(v)) out[key] = v;
    else if (v instanceof DateOnly || v instanceof TimeOnly || v instanceof Duration) out[key] = v.toString();
    else if (v instanceof Date) out[key] = v.toISOString();
    else if (v === undefined) out[key] = undefined;
  }
}

// information is the RequestInformation Kiota's to...RequestInformation
// methods return, for scripts that ask for one.
function information(s: State, metadata: RequestMetadata, method: Method, args: unknown[]): RequestInformation {
  const { body, mediaType, config } = argumentsOf(metadata, method, args);
  const info = new RequestInformation(httpMethods[method], metadata.uriTemplate, { ...s.params });
  info.configure(config, metadata.queryParametersMapper);
  if (metadata.responseBodyContentType) info.headers.tryAdd("Accept", metadata.responseBodyContentType);
  if (metadata.requestBodySerializer) {
    if (!body) throw new Error("body cannot be undefined");
    const contentType = metadata.requestBodyContentType ?? mediaType;
    if (typeof metadata.requestBodySerializer === "function") info.setContentFromParsable(s.adapter, contentType!, body as never, metadata.requestBodySerializer);
    else info.setContentFromScalar(s.adapter, contentType!, body as never);
  } else if (metadata.requestInformationContentSetMethod === "setStreamContent") {
    if (!body) throw new Error("body cannot be undefined");
    info.setStreamContent(body as ArrayBuffer, metadata.requestBodyContentType ?? mediaType);
  }
  return info;
}

interface Page {
  value?: unknown[] | null;
  odataNextLink?: string | null;
}

async function all(builder: Builder, method: "get" | "post", args: unknown[]): Promise<unknown[]> {
  const send = (b: Builder) => (b as unknown as Record<string, (...a: unknown[]) => Promise<Page | undefined>>)[method](...args);
  const withUrl = (url: string) => (builder as unknown as { withUrl(url: string): Builder }).withUrl(url);
  const values: unknown[] = [];
  let page = await send(builder);
  for (;;) {
    if (page?.value) values.push(...page.value);
    const link = page?.odataNextLink;
    if (!link) return values;
    // The link carries the API's origin as the server saw it; the path
    // and query are the next page.
    page = await send(withUrl(baseUrl + link.replace(/^[a-z][a-z0-9+.-]*:\/\/[^/]*/i, "")));
  }
}
