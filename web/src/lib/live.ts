import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { clearToken } from '@/api/http';
import { authorizationHeaders } from '@/api/auth';
import { keys } from '@/api/queries';

/**
 * A live change of an item the viewer may see, for apps that read again
 * when their items change. A resync has no item: the stream reconnected and
 * may have missed changes, so everything should be read again.
 */
export interface ItemChange {
  id: string;
  /** The item's workspace; a resync has none. */
  workspaceId?: string;
  /** The item's list or library, when the stream says. */
  collectionId?: string;
  /** The item's content type, when it has one. */
  contentTypeId?: string;
  /** The item was deleted or is no longer visible. */
  removed?: boolean;
  /** The item was created. */
  created?: boolean;
  /** Changes may have been missed; read everything again. */
  resync?: boolean;
}

const itemListeners = new Set<(change: ItemChange) => void>();

/** Calls listener with each live item change while the page's stream runs; returns what stops it. */
export function onItemChange(listener: (change: ItemChange) => void): () => void {
  itemListeners.add(listener);
  return () => itemListeners.delete(listener);
}

function itemChange(data: string): ItemChange | undefined {
  try {
    type Place = { workspace_id?: string; container_id?: string | null; content_type_id?: string | null };
    const frame = JSON.parse(data) as Place & { id?: string; action?: string; item?: Place; '@removed'?: unknown };
    if (!frame.id) return undefined;
    // A removal says where the item was, as it can no longer send the item.
    const place = frame.item ?? frame;
    return { id: frame.id, workspaceId: place.workspace_id, collectionId: place.container_id ?? undefined, contentTypeId: place.content_type_id ?? undefined, removed: frame['@removed'] !== undefined, created: frame.action === 'created' };
  } catch {
    return undefined;
  }
}

/** Why a module subscription ended: the viewer lost access, or the server refused it. */
export interface SubscriptionEnd {
  reason: 'access' | 'refused';
  detail?: string;
}

/** What a module subscription on the page's stream is told. */
export interface StreamSubscriber {
  /** A frame of the subscription: its event and its JSON data. */
  frame(event: string, data: Record<string, unknown>): void;
  ended?(end: SubscriptionEnd): void;
}

interface Subscribed {
  spec: Record<string, unknown>;
  subscriber: StreamSubscriber;
  /** The server's ID for it on the current stream. */
  id?: string;
}

const subscriptions = new Set<Subscribed>();
// The page's current stream, which subscriptions are added to; a new stream
// starts without any.
let currentStream: string | undefined;
// How many subscriptions are being added, whose IDs the page does not know
// yet, and the frames that named an unknown subscription meanwhile, by its
// ID: a subscription's current values can arrive on the stream before the
// answer that names it.
let adding = 0;
const early = new Map<string, { event: string; frame: Record<string, unknown> }[]>();
const earlyFrames = 32;

/** Hands a frame of the stream to the subscription it names. */
function deliver(s: Subscribed, event: string, frame: Record<string, unknown>) {
  if (event === 'access') {
    subscriptions.delete(s);
    s.subscriber.ended?.({ reason: 'access' });
  } else s.subscriber.frame(event, frame);
}

async function subscribeOn(stream: string, s: Subscribed) {
  adding++;
  try {
    const response = await fetch(`/v1/events/${encodeURIComponent(stream)}/subscriptions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json', ...authorizationHeaders() },
      body: JSON.stringify(s.spec),
      credentials: 'same-origin',
    });
    const body = (await response.json().catch(() => ({}))) as { id?: string; detail?: string };
    if (!subscriptions.has(s) || stream !== currentStream) {
      if (response.ok && body.id) void unsubscribeOn(stream, body.id);
      return;
    }
    if (response.ok && body.id) {
      s.id = body.id;
      const waiting = early.get(body.id) ?? [];
      early.delete(body.id);
      for (const { event, frame } of waiting) {
        if (subscriptions.has(s)) deliver(s, event, frame);
      }
      return;
    }
    // A stream that ended meanwhile subscribes again when the next connects.
    if (response.status === 404) return;
    subscriptions.delete(s);
    s.subscriber.ended?.({ reason: 'refused', detail: body.detail });
  } catch {
    // The next stream subscribes again.
  } finally {
    // Frames no answer claimed belong to no subscription of this page.
    if (--adding === 0) early.clear();
  }
}

async function unsubscribeOn(stream: string, id: string) {
  try {
    await fetch(`/v1/events/${encodeURIComponent(stream)}/subscriptions/${encodeURIComponent(id)}`, { method: 'DELETE', headers: authorizationHeaders(), credentials: 'same-origin' });
  } catch {
    // The subscription ends with its stream.
  }
}

/**
 * Subscribes the page's live stream to a module's frames, such as an MQTT
 * channel's messages ({ source: 'connections', … }), as the viewer. Every
 * stream the page opens subscribes again, so a reconnection keeps it;
 * returns what stops it.
 */
export function subscribeStream(spec: Record<string, unknown>, subscriber: StreamSubscriber): () => void {
  const s: Subscribed = { spec, subscriber };
  subscriptions.add(s);
  if (currentStream) void subscribeOn(currentStream, s);
  return () => {
    if (!subscriptions.delete(s)) return;
    if (currentStream && s.id) void unsubscribeOn(currentStream, s.id);
  };
}

/** Routes a stream's frames to its subscriptions. */
function streamFrame(event: string, data: string) {
  if (event === 'connected') {
    let stream: string | undefined;
    try {
      stream = (JSON.parse(data) as { stream?: string }).stream;
    } catch {
      stream = undefined;
    }
    currentStream = stream;
    early.clear();
    for (const s of subscriptions) {
      s.id = undefined;
      if (stream) void subscribeOn(stream, s);
    }
    return;
  }
  if (subscriptions.size === 0 || !data.includes('"subscription"')) return;
  let frame: Record<string, unknown>;
  try {
    frame = JSON.parse(data) as Record<string, unknown>;
  } catch {
    return;
  }
  let known = false;
  for (const s of subscriptions) {
    if (!s.id || s.id !== frame.subscription) continue;
    known = true;
    deliver(s, event, frame);
  }
  if (known || adding === 0 || typeof frame.subscription !== 'string') return;
  const waiting = early.get(frame.subscription) ?? [];
  if (waiting.length < earlyFrames) early.set(frame.subscription, [...waiting, { event, frame }]);
}

/** Changes arriving within this many milliseconds refresh the page once. */
export const liveRefreshInterval = 1000;

/** Live changes use the same bearer or proxy credentials as other API requests. */
export function useLiveEvents() {
  const queryClient = useQueryClient();
  useEffect(() => {
    const controller = new AbortController();
    // A bulk edit streams an event per item: refresh at most once per interval,
    // so an open page does not refetch everything for every changed item.
    let timer: ReturnType<typeof setTimeout> | undefined;
    const pending = new Set<string>();
    const flush = () => {
      timer = undefined;
      if (pending.has('content')) {
        void queryClient.invalidateQueries({ queryKey: keys.resources });
        void queryClient.invalidateQueries({ queryKey: keys.home });
        void queryClient.invalidateQueries({ queryKey: keys.inboxes });
      }
      if (pending.has('run')) void queryClient.invalidateQueries({ queryKey: ['workflow-runs'] });
      if (pending.has('comment')) void queryClient.invalidateQueries({ queryKey: keys.comments });
      // A notification may tell of a task, which the task lists show too.
      if (pending.has('notification')) {
        void queryClient.invalidateQueries({ queryKey: keys.notifications });
        void queryClient.invalidateQueries({ queryKey: ['tasks'] });
      }
      pending.clear();
    };
    const schedule = (kind: string) => {
      pending.add(kind);
      timer ??= setTimeout(flush, liveRefreshInterval);
    };
    controller.signal.addEventListener('abort', () => clearTimeout(timer), { once: true });
    void readEvents(controller.signal, (event, data) => {
      streamFrame(event, data);
      if (itemListeners.size > 0) {
        const change = event === 'item' ? itemChange(data) : event === 'resync' || event === 'connected' ? { id: '', resync: true } : undefined;
        if (change) for (const listener of itemListeners) listener(change);
      }
      if (event === 'item' || event === 'resync' || event === 'connected') schedule('content');
      if (event === 'run') schedule('run');
      if (event === 'comment' || event === 'connected' || event === 'resync') schedule('comment');
      if (event === 'notification' || event === 'connected' || event === 'resync') schedule('notification');
      if (event === 'reauthenticate') {
        clearToken();
        window.location.assign('/login');
      }
    });
    return () => {
      controller.abort();
      currentStream = undefined;
    };
  }, [queryClient]);
}

async function readEvents(signal: AbortSignal, onEvent: (event: string, data: string) => void) {
  let lastId = '';
  while (!signal.aborted) {
    const headers: Record<string, string> = { Accept: 'text/event-stream', ...authorizationHeaders() };
    if (lastId) headers['Last-Event-ID'] = lastId;
    try {
      const response = await fetch('/v1/events', { headers, signal, credentials: 'same-origin' });
      if (response.status === 401) {
        onEvent('reauthenticate', '');
        return;
      }
      if (!response.ok || !response.body) {
        await delay(2000, signal);
        continue;
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += decoder.decode(chunk.value, { stream: true });
        const frames = buffer.split('\n\n');
        buffer = frames.pop() ?? '';
        for (const frame of frames) {
          let event = 'message';
          let data = '';
          for (const line of frame.split('\n')) {
            if (line.startsWith('event:')) event = line.slice(6).trim();
            else if (line.startsWith('id:')) lastId = line.slice(3).trim();
            else if (line.startsWith('data:')) data += line.slice(5).trim();
          }
          if (event !== 'message') onEvent(event, data);
        }
      }
    } catch (error) {
      if (signal.aborted) return;
      if (error instanceof DOMException && error.name === 'AbortError') return;
      await delay(2000, signal);
    }
  }
}

function delay(ms: number, signal: AbortSignal) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => clearTimeout(timer), { once: true });
  });
}
