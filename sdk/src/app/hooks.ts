// Hooks that read the workspace's data as the viewer and, when asked, read
// it again when it changes.

import { useCallback, useContext, useEffect, useRef, useState } from "react";
import type { Change } from "./bridge.js";
import { RuntimeContext, type AppContext, type Runtime } from "./context.js";

/** An item or folder as queries return it, with plain JSON values. */
export interface Item {
  id: string;
  kind: string;
  name: string;
  workspace_id: string;
  container_id?: string;
  parent_id?: string;
  content_type_id?: string;
  values: Record<string, unknown>;
  version: number;
  created_at: string;
  updated_at: string;
  created_by?: string;
  [key: string]: unknown;
}

/** A collection query: the query filter language, sort and type (docs/application-foundation.md). */
export interface Query {
  filter?: unknown;
  sort?: { field: string; direction?: "asc" | "desc" }[];
  contentTypeId?: string;
  search?: string;
  [key: string]: unknown;
}

export interface QueryOptions {
  /** Read every page, not only the first. */
  all?: boolean;
  /** Read again when an item of the list changes. */
  live?: boolean;
  /** Items per page, 500 (the most) by default. */
  top?: number;
  /** The fields to read; all by default. */
  select?: string[];
  /**
   * Compact items: id, workspace_id, kind, name, parent_id, content_type_id,
   * version, values and keywords, whether an item is archived, and the
   * built-in columns select names, under half the size of a full item. For
   * reading many items.
   */
  compact?: boolean;
}

export interface Loaded<T> {
  data: T;
  loading: boolean;
  error?: Error;
  /** Reads it again. */
  reload(): void;
}

function useRuntime(): Runtime {
  const runtime = useContext(RuntimeContext);
  if (!runtime) throw new Error("PaperGo hooks run inside an app");
  return runtime;
}

/** The running app's context, for components below render. */
export function useApp<P = Record<string, unknown>>(): AppContext<P> {
  return useRuntime().ctx as unknown as AppContext<P>;
}

// Live changes arriving within this many milliseconds read once.
const settle = 250;

function useLive(enabled: boolean, matches: (changes: Change[]) => boolean, reload: () => void): void {
  const { bridge } = useRuntime();
  const latest = useRef({ matches, reload });
  latest.current = { matches, reload };
  useEffect(() => {
    if (!enabled) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = bridge.watch((changes) => {
      if (!changes.some((c) => c.resync) && !latest.current.matches(changes)) return;
      clearTimeout(timer);
      timer = setTimeout(() => latest.current.reload(), settle);
    });
    return () => {
      clearTimeout(timer);
      stop();
    };
  }, [bridge, enabled]);
}

/** What a list says of a type it accepts, to tell what a query of a type covers. */
export interface ListType {
  id: string;
  parent_id?: string | null;
  collection_id?: string | null;
  ancestor_ids?: string[];
}

/**
 * Whether an item of a type cannot be in a query of a type: a query names a
 * type with its subtypes, and a list's own copy of a type stands for the type
 * it copies, as the server reads them. Whatever is not known may match.
 */
export function cannotMatch(types: ListType[] | undefined, queried: string | undefined, type: string | undefined): boolean {
  if (!types || !queried || !type || !types.some((t) => t.id === type)) return false;
  const named = types.find((t) => t.id === queried);
  const base = named?.collection_id ? named.parent_id ?? queried : queried;
  return !types.some((t) => t.id === type && (t.ancestor_ids ?? []).includes(base));
}

// The types of each list, read once for all of an app's live queries.
const listTypes = new Map<string, Promise<ListType[]>>();

/** Whether a live change of an item of a list can concern a query of a type. */
function useConcerns(listId: string | undefined, contentTypeId: string | undefined, live: boolean): (change: Change) => boolean {
  const { ctx } = useRuntime();
  const [types, setTypes] = useState<ListType[]>();
  useEffect(() => {
    if (!live || !listId || !contentTypeId) return;
    let stale = false;
    let read = listTypes.get(listId);
    if (!read) {
      read = ctx.request<{ value?: ListType[] }>("GET", `/v1/resources/${encodeURIComponent(listId)}/content-types`).then((r) => r.data.value ?? []);
      listTypes.set(listId, read);
    }
    read.then(
      (value) => !stale && setTypes(value),
      // Without the types every change of the list may concern the query.
      () => listTypes.delete(listId),
    );
    return () => {
      stale = true;
    };
  }, [ctx, listId, contentTypeId, live]);
  return (change) => change.collectionId === listId && !cannotMatch(types, contentTypeId, change.contentTypeId);
}

function toSpec(q: Query): Record<string, unknown> {
  const { contentTypeId, ...rest } = q;
  return { ...rest, ...(contentTypeId ? { content_type_id: contentTypeId } : {}) };
}

interface Page<T> {
  value?: T[];
  "@odata.nextLink"?: string | null;
}

/** Sends a request and reads its answer, as an app's context does. */
type Requester = <T>(method: "POST", url: string, init: { body: unknown }) => Promise<{ data: T }>;

/** The values of a POST request's first page, or of every page with all. */
export async function readPages<T>(request: Requester, first: string, body: unknown, all: boolean): Promise<T[]> {
  const values: T[] = [];
  let url: string | undefined = first;
  while (url) {
    const page: Page<T> = (await request<Page<T>>("POST", url, { body })).data;
    values.push(...(page.value ?? []));
    url = all ? page["@odata.nextLink"] ?? undefined : undefined;
  }
  return values;
}

/** The URL and body of a useQuery request. */
export function queryRequest(listId: string, query: Query, options: QueryOptions): { url: string; body: Record<string, unknown> } {
  const body: Record<string, unknown> = { query: toSpec(query) };
  if (options.select) body.select = options.select;
  const route = options.compact ? "query/compact" : "query";
  return { url: `/v1/resources/${encodeURIComponent(listId)}/${route}?$top=${options.top ?? 500}`, body };
}

/** The URL and body of a useAggregates request. */
export function aggregateRequest(listId: string, request: AggregateQuery, top: number | undefined): { url: string; body: Record<string, unknown> } {
  const body: Record<string, unknown> = { query: toSpec(request.query ?? {}), aggregates: request.aggregates };
  if (request.groups) body.groups = request.groups.map(({ timeZone, ...group }) => ({ ...group, ...(timeZone ? { time_zone: timeZone } : {}) }));
  if (request.sort) body.sort = request.sort;
  return { url: `/v1/resources/${encodeURIComponent(listId)}/query/aggregates?$top=${top ?? 500}`, body };
}

/**
 * The items of a list that match query, as the viewer may read them. With
 * live, a change to one of the list's items reads them again.
 */
export function useQuery(listId: string | undefined, query: Query = {}, options: QueryOptions = {}): Loaded<Item[]> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<{ data: Item[]; loading: boolean; error?: Error }>({ data: [], loading: Boolean(listId) });
  const key = JSON.stringify([listId, query, options.all, options.top, options.select, options.compact]);
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);
  useEffect(() => {
    if (!listId) {
      setState({ data: [], loading: false });
      return;
    }
    let stale = false;
    setState((s) => ({ ...s, loading: true }));
    const { url, body } = queryRequest(listId, query, options);
    readPages<Item>(ctx.request, url, body, Boolean(options.all))
      .then((data) => !stale && setState({ data, loading: false }))
      .catch((error: Error) => !stale && setState((s) => ({ ...s, loading: false, error })));
    return () => {
      stale = true;
    };
    // The key stands for the query's content.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx, key, round]);
  const concerns = useConcerns(listId, query.contentTypeId, Boolean(options.live));
  useLive(Boolean(options.live && listId), (changes) => changes.some((c) => concerns(c) || state.data.some((i) => i.id === c.id)), reload);
  return { ...state, reload };
}

/** A figure of each group (docs/application-foundation.md): op of field's values, named as (op_field by default). */
export interface Aggregate {
  op: "count" | "sum" | "avg" | "min" | "max";
  field: string;
  as?: string;
}

/** One field of a group's key: its values, or a date or date-time's period of interval, in timeZone. */
export interface AggregateGroupBy {
  field: string;
  interval?: "day" | "week" | "month" | "year";
  /** Where days begin, for date-times; the viewer's time zone suits most apps. UTC by default. */
  timeZone?: string;
}

/** Figures of a list's items: all of them, or in groups by up to four fields of groups. */
export interface AggregateQuery {
  query?: Query;
  groups?: AggregateGroupBy[];
  aggregates: Aggregate[];
  /** The groups' order, by a figure's name, $count or a field of groups; by their keys after that. */
  sort?: { field: string; direction?: "asc" | "desc" }[];
}

/** A group's value of one of the request's groups. */
export interface AggregateKey {
  /** With an interval, the period's first day as YYYY-MM-DD; numbers as exact strings; null for items without one. */
  value: string | boolean | null;
  /** The term's name, for a term or keywords field. */
  label?: string;
}

/**
 * A group: its keys, one for each of the request's groups; its items; and
 * each figure by name: counts as numbers, numbers as exact strings, null
 * without values.
 */
export interface AggregateGroup {
  keys: AggregateKey[];
  count: number;
  values: Record<string, string | number | null>;
}

/**
 * Figures of the items of a list that match a query, worked out by the
 * server, as the viewer may read them: a chart of two years of hourly
 * readings reads 730 days, not 17,520 items. It reads every group, or with top
 * the first that many in the order of sort. With live, a change to one of the
 * list's items works them out again.
 */
export function useAggregates(listId: string | undefined, request: AggregateQuery, options: { live?: boolean; top?: number } = {}): Loaded<AggregateGroup[]> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<{ data: AggregateGroup[]; loading: boolean; error?: Error }>({ data: [], loading: Boolean(listId) });
  const key = JSON.stringify([listId, request, options.top]);
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);
  useEffect(() => {
    if (!listId) {
      setState({ data: [], loading: false });
      return;
    }
    let stale = false;
    setState((s) => ({ ...s, loading: true }));
    const { url, body } = aggregateRequest(listId, request, options.top);
    readPages<AggregateGroup>(ctx.request, url, body, !options.top)
      .then((data) => !stale && setState({ data, loading: false }))
      .catch((error: Error) => !stale && setState((s) => ({ ...s, loading: false, error })));
    return () => {
      stale = true;
    };
    // The key stands for the request's content.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx, key, round]);
  const concerns = useConcerns(listId, request.query?.contentTypeId, Boolean(options.live));
  useLive(Boolean(options.live && listId), (changes) => changes.some(concerns), reload);
  return { ...state, reload };
}

/** One item, as the viewer may read it; with live, read again when it changes. */
export function useItem(id: string | undefined, options: { live?: boolean } = {}): Loaded<Item | undefined> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<{ data?: Item; loading: boolean; error?: Error }>({ loading: Boolean(id) });
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);
  useEffect(() => {
    if (!id) {
      setState({ loading: false });
      return;
    }
    let stale = false;
    setState((s) => ({ ...s, loading: true }));
    ctx
      .request<Item>("GET", `/v1/resources/${encodeURIComponent(id)}`)
      .then((r) => !stale && setState({ data: r.data, loading: false }))
      .catch((error: Error) => !stale && setState((s) => ({ ...s, loading: false, error })));
    return () => {
      stale = true;
    };
  }, [ctx, id, round]);
  useLive(Boolean(options.live && id), (changes) => changes.some((c) => c.id === id), reload);
  return { data: state.data, loading: state.loading, error: state.error, reload };
}
