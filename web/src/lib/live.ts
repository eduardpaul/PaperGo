import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { clearToken } from '@/api/http';
import { authorizationHeaders } from '@/api/auth';
import { keys } from '@/api/queries';

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
    void readEvents(controller.signal, (event) => {
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

async function readEvents(signal: AbortSignal, onEvent: (event: string) => void) {
  let lastId = '';
  while (!signal.aborted) {
    const headers: Record<string, string> = { Accept: 'text/event-stream', ...authorizationHeaders() };
    if (lastId) headers['Last-Event-ID'] = lastId;
    try {
      const response = await fetch('/v1/events', { headers, signal, credentials: 'same-origin' });
      if (response.status === 401) {
        onEvent('reauthenticate');
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
          for (const line of frame.split('\n')) {
            if (line.startsWith('event:')) event = line.slice(6).trim();
            else if (line.startsWith('id:')) lastId = line.slice(3).trim();
          }
          if (event !== 'message') onEvent(event);
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
