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
  /** Items per page, 200 by default. */
  top?: number;
  /** The fields to read; all by default. */
  select?: string[];
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
      if (!latest.current.matches(changes)) return;
      clearTimeout(timer);
      timer = setTimeout(() => latest.current.reload(), settle);
    });
    return () => {
      clearTimeout(timer);
      stop();
    };
  }, [bridge, enabled]);
}

function toSpec(q: Query): Record<string, unknown> {
  const { contentTypeId, ...rest } = q;
  return contentTypeId ? { ...rest, content_type_id: contentTypeId } : rest;
}

interface Page {
  value?: Item[];
  "@odata.nextLink"?: string | null;
}

/**
 * The items of a list that match query, as the viewer may read them. With
 * live, a change to one of the list's items reads them again.
 */
export function useQuery(listId: string | undefined, query: Query = {}, options: QueryOptions = {}): Loaded<Item[]> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<{ data: Item[]; loading: boolean; error?: Error }>({ data: [], loading: Boolean(listId) });
  const key = JSON.stringify([listId, query, options.all, options.top, options.select]);
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);
  useEffect(() => {
    if (!listId) return;
    let stale = false;
    setState((s) => ({ ...s, loading: true }));
    (async () => {
      const body: Record<string, unknown> = { query: toSpec(query) };
      if (options.select) body.select = options.select;
      let url: string | undefined = `/v1/resources/${encodeURIComponent(listId)}/query?$top=${options.top ?? 200}`;
      const items: Item[] = [];
      while (url) {
        const page: Page = (await ctx.request<Page>("POST", url, { body })).data;
        items.push(...(page.value ?? []));
        url = options.all ? page["@odata.nextLink"] ?? undefined : undefined;
      }
      if (!stale) setState({ data: items, loading: false });
    })().catch((error: Error) => {
      if (!stale) setState((s) => ({ ...s, loading: false, error }));
    });
    return () => {
      stale = true;
    };
    // The key stands for the query's content.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx, key, round]);
  useLive(Boolean(options.live && listId), (changes) => changes.some((c) => c.collectionId === listId || state.data.some((i) => i.id === c.id)), reload);
  return { ...state, reload };
}

/** One item, as the viewer may read it; with live, read again when it changes. */
export function useItem(id: string | undefined, options: { live?: boolean } = {}): Loaded<Item | undefined> {
  const { ctx } = useRuntime();
  const [state, setState] = useState<{ data?: Item; loading: boolean; error?: Error }>({ loading: Boolean(id) });
  const [round, setRound] = useState(0);
  const reload = useCallback(() => setRound((n) => n + 1), []);
  useEffect(() => {
    if (!id) return;
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
