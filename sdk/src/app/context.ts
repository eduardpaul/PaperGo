// What an app's render receives: the API as the viewer, its parameters,
// who views it and where, and PaperGo's formats.

import { createContext as reactContext } from "react";
import { HostRequestAdapter } from "../adapter.js";
import { client } from "../builders.js";
import { createPaperGoClient, PaperGoClientNavigationMetadata, type PaperGoClient } from "../client/paperGoClient.js";
import type { HostResponse } from "../host.js";
import { Bridge, type OpenTarget } from "./bridge.js";
import { formatter, type Format, type Preferences } from "./format.js";

export type { OpenTarget } from "./bridge.js";

/** The viewer's role in the workspace. */
export type Role = "owner" | "member" | "visitor";

/** What the page tells the app when it starts it. */
export interface Init {
  app: { id: string; key: string; name: string; version: number };
  params: Record<string, unknown>;
  user: { subject: string; name: string; preferences: Preferences };
  workspace: { id: string; name: string; role: Role };
  theme: "light" | "dark";
}

/** A request the API refused: its status, and the problem's code and detail. */
export class RequestError extends Error {
  constructor(
    readonly status: number,
    readonly code: string | undefined,
    readonly detail: string | undefined,
    readonly body: unknown,
  ) {
    super([`request failed with status ${status}`, code, detail].filter(Boolean).join(": "));
    this.name = "RequestError";
  }
}

export interface RequestOptions {
  /** A JSON body, or text with its content-type header. */
  body?: unknown;
  headers?: Record<string, string>;
}

/** A response of ctx.request: its status, headers and body, JSON parsed. */
export interface Response<T = unknown> {
  status: number;
  headers: Record<string, string>;
  data: T;
}

export interface AppContext<P = Record<string, unknown>> {
  /** The REST API, sent as the person viewing the app. */
  readonly api: PaperGoClient;
  /** The app's parameters, chosen in Workspace settings. */
  readonly params: P;
  /** Who is viewing, with their preferences. */
  readonly user: Init["user"];
  /** The app's workspace and the viewer's role in it. */
  readonly workspace: Init["workspace"];
  /** The app itself and the version running. */
  readonly app: Init["app"];
  /** Numbers and dates in the viewer's preferences. */
  readonly format: Format;
  /**
   * Any API route by method and path, such as GET /v1/me/tasks?key=…, as
   * the viewer. A JSON body is sent as JSON; the response's JSON body is
   * parsed. A response of 400 or more throws a RequestError.
   */
  request<T = unknown>(method: string, path: string, options?: RequestOptions): Promise<Response<T>>;
  /** Opens an item, a task or a list in PaperGo. */
  open(target: OpenTarget): void;
}

function parse(response: HostResponse): unknown {
  const type = (response.headers["content-type"] ?? "").toLowerCase();
  if (response.bytes) return response.bytes;
  if (/json/.test(type) && response.body !== "") return JSON.parse(response.body);
  return response.body === "" ? undefined : response.body;
}

/** The context of an app started with init, over bridge. */
export function appContext<P>(init: Init, bridge: Bridge): AppContext<P> {
  const adapter = new HostRequestAdapter((method, url, headers, body) => bridge.request(method, url, headers, body));
  // Kiota's client registers the serializers; apps use the SDK's builders.
  createPaperGoClient(adapter);
  const api = client<PaperGoClient>(adapter, PaperGoClientNavigationMetadata);
  return {
    api,
    params: init.params as P,
    user: init.user,
    workspace: init.workspace,
    app: init.app,
    format: formatter(init.user.preferences),
    async request<T>(method: string, path: string, options: RequestOptions = {}): Promise<Response<T>> {
      const headers: Record<string, string> = { accept: "application/json" };
      for (const [k, v] of Object.entries(options.headers ?? {})) headers[k.toLowerCase()] = v;
      let body: string | ArrayBuffer | undefined;
      if (options.body !== undefined) {
        if (typeof options.body === "string" || options.body instanceof ArrayBuffer) body = options.body;
        else {
          body = JSON.stringify(options.body);
          headers["content-type"] ??= "application/json";
        }
      }
      const response = await bridge.request(method.toUpperCase(), path, headers, body);
      const data = parse(response);
      if (response.status >= 400) {
        const problem = (data ?? {}) as { code?: string; detail?: string };
        throw new RequestError(response.status, problem.code, problem.detail, data);
      }
      return { status: response.status, headers: response.headers, data: data as T };
    },
    open: (target) => bridge.open(target),
  };
}

/** The running app's context and bridge, for the hooks. */
export interface Runtime {
  ctx: AppContext<Record<string, unknown>>;
  bridge: Bridge;
}

export const RuntimeContext = reactContext<Runtime | null>(null);
