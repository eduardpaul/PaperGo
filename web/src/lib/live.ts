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
    const frame = JSON.parse(data) as Place & { id?: string; item?: Place; '@removed'?: unknown };
    if (!frame.id) return undefined;
    // A removal says where the item was, as it can no longer send the item.
    const place = frame.item ?? frame;
    return { id: frame.id, workspaceId: place.workspace_id, collectionId: place.container_id ?? undefined, contentTypeId: place.content_type_id ?? undefined, removed: frame['@removed'] !== undefined };
  } catch {
    return undefined;
  }
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
    return () => controller.abort();
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
