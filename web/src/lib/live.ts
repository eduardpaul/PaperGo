import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { clearToken, token } from '@/api/http';
import { keys } from '@/api/queries';

/** Live changes from GET /v1/events. Fetch carries the bearer token; EventSource cannot. */
export function useLiveEvents() {
  const queryClient = useQueryClient();
  useEffect(() => {
    const controller = new AbortController();
    void readEvents(controller.signal, (event) => {
      if (event === 'item' || event === 'resync' || event === 'connected') {
        void queryClient.invalidateQueries({ queryKey: keys.resources });
        void queryClient.invalidateQueries({ queryKey: keys.home });
        void queryClient.invalidateQueries({ queryKey: keys.inboxes });
      }
      if (event === 'run') void queryClient.invalidateQueries({ queryKey: ['workflow-runs'] });
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
    const headers: Record<string, string> = { Accept: 'text/event-stream' };
    const bearer = token();
    if (bearer) headers.Authorization = `Bearer ${bearer}`;
    if (lastId) headers['Last-Event-ID'] = lastId;
    try {
      const response = await fetch('/v1/events', { headers, signal });
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
