// Connections: the MQTT brokers and HTTP services a workspace manager
// opened, which an app reaches by key through named channels as its
// viewer. The app never sees a connection's address or credentials, and
// PaperGo checks what the viewer may see and control.

import { useContext, useEffect, useRef, useState } from "react";
import type { TopicMessage } from "./bridge.js";
import { RuntimeContext, type AppContext, type Response, type Runtime } from "./context.js";

export type { SubscriptionEnd, TopicMessage } from "./bridge.js";

/** What a caller does with a channel: subscribe or GET (see), publish or change (control). */
export type ConnectionUse = "see" | "control";

/** A channel of a connection as the viewer sees it. */
export interface ConnectionChannel {
  key: string;
  name: string;
  /** MQTT: the topic filter. */
  topics?: string;
  /** HTTP: the path pattern and methods. */
  path?: string;
  methods?: string[];
  retain?: boolean;
  uses: ConnectionUse[];
  /** What the viewer may do with it now. */
  may: ConnectionUse[];
}

/** A connection of the app's workspace as the viewer sees it. */
export interface Connection {
  key: string;
  name: string;
  kind: "mqtt" | "http";
  /** Whether it works now: an MQTT broker is connected, an HTTP service is not failing. */
  connected: boolean;
  channels: ConnectionChannel[];
}

/** An HTTP service's answer: its status and headers, its body parsed when JSON, as text when text, otherwise in base64. */
export interface Answer<T = unknown> {
  status: number;
  headers: Record<string, string>;
  body?: T;
  body_base64?: string;
}

/** What a publish answers: the request ID receivers see as the papergo-request user property. */
export interface Published {
  request: string;
  topic: string;
  qos: number;
  retain: boolean;
}

export interface PublishOptions {
  /** 0 or 1; 1 by default, which waits for the broker's acknowledgement. */
  qos?: 0 | 1;
  /** Keep the message as the topic's last on the broker; only on channels that allow it. */
  retain?: boolean;
}

export interface CallOptions {
  /** Accept, Content-Type, If-Match, If-None-Match or Idempotency-Key; nothing else is sent. */
  headers?: Record<string, string>;
}

export interface FetchOptions {
  /** HEAD instead of GET. */
  method?: "GET" | "HEAD";
  accept?: string;
}

/** ctx.connections: publishing and calling through the workspace's connections. */
export interface Connections {
  /** The workspace's connections the viewer may read. */
  list(): Promise<Connection[]>;
  /** Publishes payload to a topic inside an MQTT channel. A string is sent as its text, other values as JSON. */
  publish(connection: string, channel: string, topic: string, payload: unknown, options?: PublishOptions): Promise<Published>;
  /** GETs a path inside an HTTP channel, with its query. */
  fetch<T = unknown>(connection: string, channel: string, path: string, options?: FetchOptions): Promise<Answer<T>>;
  /** Calls a path inside an HTTP channel with any method it allows. */
  call<T = unknown>(connection: string, channel: string, method: string, path: string, body?: unknown, options?: CallOptions): Promise<Answer<T>>;
}

const segment = encodeURIComponent;

/** ctx.connections over ctx.request, for the app's workspace. */
export function connections(workspace: string, request: AppContext["request"]): Connections {
  const base = (connection: string, channel?: string) =>
    `/v1/workspaces/${segment(workspace)}/connections/${segment(connection)}${channel === undefined ? "" : `/channels/${segment(channel)}`}`;
  const data = async <T>(r: Promise<Response<T>>) => (await r).data;
  return {
    list: async () => (await data(request<{ value: Connection[] }>("GET", `/v1/workspaces/${segment(workspace)}/connections`))).value,
    publish: (connection, channel, topic, payload, options = {}) =>
      data(request<Published>("POST", `${base(connection, channel)}/publish`, { body: { topic, payload, ...options } })),
    fetch: <T>(connection: string, channel: string, path: string, options: FetchOptions = {}) => {
      const query = new URLSearchParams({ path });
      if (options.method === "HEAD") query.set("method", "HEAD");
      if (options.accept) query.set("accept", options.accept);
      return data(request<Answer<T>>("GET", `${base(connection, channel)}/fetch?${query}`));
    },
    call: <T>(connection: string, channel: string, method: string, path: string, body?: unknown, options: CallOptions = {}) =>
      data(request<Answer<T>>("POST", `${base(connection, channel)}/call`, { body: { method: method.toUpperCase(), path, ...(body === undefined ? {} : { body }), ...(options.headers ? { headers: options.headers } : {}) } })),
  };
}

function useRuntime(): Runtime {
  const runtime = useContext(RuntimeContext);
  if (!runtime) throw new Error("PaperGo hooks run inside an app");
  return runtime;
}

/** useConnection's answer: the connection, while it loads, and whether it works. */
export interface ConnectionState {
  connection?: Connection;
  kind?: Connection["kind"];
  connected: boolean;
  loading: boolean;
  /** Set when the workspace has no such connection or the viewer may not read it. */
  error?: Error;
}

// How often useConnection reads whether its connection works.
const connectionEvery = 30_000;

/** A connection of the app's workspace by key, read again every 30 seconds. */
export function useConnection(key: string): ConnectionState {
  const { ctx } = useRuntime();
  const [state, setState] = useState<ConnectionState>({ connected: false, loading: true });
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const read = async () => {
      try {
        const connection = (await ctx.request<Connection>("GET", `/v1/workspaces/${segment(ctx.workspace.id)}/connections/${segment(key)}`)).data;
        if (!stopped) setState({ connection, kind: connection.kind, connected: connection.connected, loading: false });
      } catch (error) {
        if (!stopped) setState({ connected: false, loading: false, error: error as Error });
      }
      if (!stopped) timer = setTimeout(read, connectionEvery);
    };
    void read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [ctx, key]);
  return state;
}

/**
 * The messages of an MQTT channel's topics that filter matches, by topic,
 * latest first heard: current values when the app opens, then each new
 * one. A fast topic is delivered at the latest value, not every message.
 */
export function useTopics(connection: string, channel: string, filter?: string): Map<string, TopicMessage> {
  const { bridge } = useRuntime();
  const [topics, setTopics] = useState<Map<string, TopicMessage>>(() => new Map());
  useEffect(() => {
    setTopics(new Map());
    return bridge.subscribe(connection, channel, filter, {
      messages: (messages) =>
        setTopics((current) => {
          const next = new Map(current);
          for (const m of messages) next.set(m.topic, m);
          return next;
        }),
    });
  }, [bridge, connection, channel, filter]);
  return topics;
}

/** The last message of one MQTT topic inside a channel, or undefined until one is heard. */
export function useTopic(connection: string, channel: string, topic: string): TopicMessage | undefined {
  return useTopics(connection, channel, topic).get(topic);
}

/** useFetch's answer. */
export interface Fetched<T> {
  data?: Answer<T>;
  error?: Error;
  loading: boolean;
  /** Reads it again now. */
  refresh(): void;
}

export interface UseFetchOptions extends FetchOptions {
  /** Read again this often, in milliseconds; at least 5000. Without it, read once. */
  every?: number;
}

// One request serves every component that asks for the same path at once.
const inflight = new Map<string, Promise<Answer>>();

/**
 * GETs a path inside an HTTP channel, and again every options.every
 * milliseconds while the page is shown. Components that ask for the same
 * path at once share one request.
 */
export function useFetch<T = unknown>(connection: string, channel: string, path: string, options: UseFetchOptions = {}): Fetched<T> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<Omit<Fetched<T>, "refresh">>({ loading: true });
  const refresh = useRef<() => void>(() => undefined);
  const every = options.every === undefined ? undefined : Math.max(5000, options.every);
  const { method, accept } = options;
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const key = JSON.stringify([connection, channel, path, method, accept]);
    const read = async () => {
      clearTimeout(timer);
      let pending = inflight.get(key);
      if (!pending) {
        pending = ctx.connections.fetch(connection, channel, path, { method, accept });
        inflight.set(key, pending);
        void pending.finally(() => inflight.delete(key)).catch(() => undefined);
      }
      try {
        const data = (await pending) as Answer<T>;
        if (!stopped) setState({ data, loading: false });
      } catch (error) {
        if (!stopped) setState((current) => ({ ...current, error: error as Error, loading: false }));
      }
      schedule();
    };
    const schedule = () => {
      if (stopped || every === undefined || document.hidden) return;
      timer = setTimeout(() => void read(), every);
    };
    const shown = () => {
      if (every === undefined) return;
      if (!document.hidden) void read();
      else clearTimeout(timer);
    };
    refresh.current = () => void read();
    void read();
    document.addEventListener("visibilitychange", shown);
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", shown);
    };
  }, [ctx, connection, channel, path, method, accept, every]);
  return { ...state, refresh: () => refresh.current() };
}
