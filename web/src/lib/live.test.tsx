// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { liveRefreshInterval, onItemChange, subscribeStream, useLiveEvents, type ItemChange } from './live';
import { keys } from '@/api/queries';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('refreshes once for a burst of item events', async () => {
  vi.useFakeTimers();
  const frames = Array.from({ length: 50 }, (_, i) => `id: ${i}\nevent: item\ndata: {}\n\n`).join('');
  let served = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      if (served) return new Promise<Response>(() => {});
      served = true;
      return new Response(frames, { headers: { 'Content-Type': 'text/event-stream' } });
    }),
  );
  const client = new QueryClient();
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { unmount } = renderHook(() => useLiveEvents(), { wrapper });
  await act(async () => {});
  await act(async () => { await vi.advanceTimersByTimeAsync(liveRefreshInterval - 1); });
  expect(invalidate).not.toHaveBeenCalled();
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  for (const queryKey of [keys.resources, keys.home, keys.inboxes]) {
    expect(invalidate).toHaveBeenCalledWith({ queryKey });
  }
  await act(async () => { await vi.advanceTimersByTimeAsync(liveRefreshInterval); });
  expect(invalidate).toHaveBeenCalledTimes(3);
  unmount();
  client.clear();
});

it('tells item listeners which item changed, in which list, whether it is new, and when to read everything again', async () => {
  const frames = 'id: 1\nevent: item\ndata: {"id":"i1","action":"created","item":{"id":"i1","workspace_id":"w1","container_id":"l1","content_type_id":"t1"}}\n\nid: 2\nevent: item\ndata: {"id":"i2","workspace_id":"w1","container_id":"l1","content_type_id":"t2","@removed":{"reason":"deleted"}}\n\nid: 3\nevent: resync\ndata: {}\n\n';
  let served = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      if (served) return new Promise<Response>(() => {});
      served = true;
      return new Response(frames, { headers: { 'Content-Type': 'text/event-stream' } });
    }),
  );
  const changes: ItemChange[] = [];
  const stop = onItemChange((change) => changes.push(change));
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { unmount } = renderHook(() => useLiveEvents(), { wrapper });
  await vi.waitFor(() => expect(changes).toHaveLength(3));
  expect(changes).toEqual([
    { id: 'i1', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't1', removed: false, created: true },
    { id: 'i2', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't2', removed: true, created: false },
    { id: '', resync: true },
  ]);
  stop();
  unmount();
});

it('subscribes the page stream to module frames, again on each stream, and ends a subscription whose access is lost', async () => {
  const encoder = new TextEncoder();
  const posts: { url: string; body: unknown }[] = [];
  let push: ((frame: string) => void) | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | URL, init?: RequestInit) => {
      if (String(url).endsWith('/subscriptions')) {
        posts.push({ url: String(url), body: JSON.parse(String(init?.body)) });
        return new Response(JSON.stringify({ id: 'sub-1' }), { status: 201 });
      }
      if (push) return new Promise<Response>(() => {});
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          push = (frame) => controller.enqueue(encoder.encode(frame));
          push('id: 1\nevent: connected\ndata: {"resynchronize":true,"stream":"stream-1"}\n\n');
        },
      });
      return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } });
    }),
  );
  const frames: unknown[] = [];
  let ended: unknown;
  const stop = subscribeStream({ source: 'connections', workspace: 'w1', key: 'home', channel: 'sensors' }, { frame: (event, data) => frames.push([event, data.messages]), ended: (end) => (ended = end) });
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { unmount } = renderHook(() => useLiveEvents(), { wrapper });
  await vi.waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toEqual({ url: '/v1/events/stream-1/subscriptions', body: { source: 'connections', workspace: 'w1', key: 'home', channel: 'sensors' } });
  await act(async () => {});
  push?.('id: 1\nevent: connection.messages\ndata: {"subscription":"other","messages":[1]}\n\n');
  push?.('id: 1\nevent: connection.messages\ndata: {"subscription":"sub-1","messages":[2]}\n\n');
  await vi.waitFor(() => expect(frames).toEqual([['connection.messages', [2]]]));
  push?.('id: 1\nevent: access\ndata: {"subscription":"sub-1"}\n\n');
  await vi.waitFor(() => expect(ended).toEqual({ reason: 'access' }));
  stop();
  unmount();
});

it('keeps the frames a subscription delivers before the answer that names it', async () => {
  const encoder = new TextEncoder();
  let push: ((frame: string) => void) | undefined;
  let answer: ((response: Response) => void) | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string | URL) => {
      if (String(url).endsWith('/subscriptions')) return new Promise<Response>((resolve) => (answer = resolve));
      if (push) return new Promise<Response>(() => {});
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          push = (frame) => controller.enqueue(encoder.encode(frame));
          push('id: 1\nevent: connected\ndata: {"resynchronize":true,"stream":"stream-2"}\n\n');
        },
      });
      return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } });
    }),
  );
  const frames: unknown[] = [];
  const stop = subscribeStream({ source: 'connections', workspace: 'w1', key: 'home', channel: 'sensors' }, { frame: (event, data) => frames.push([event, data.messages]) });
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { unmount } = renderHook(() => useLiveEvents(), { wrapper });
  await vi.waitFor(() => expect(answer).toBeDefined());
  // The current values come on the stream before the subscription's ID does.
  push?.('id: 1\nevent: connection.messages\ndata: {"subscription":"sub-2","messages":["current"]}\n\n');
  await act(async () => {});
  expect(frames).toEqual([]);
  answer?.(new Response(JSON.stringify({ id: 'sub-2' }), { status: 201 }));
  await vi.waitFor(() => expect(frames).toEqual([['connection.messages', ['current']]]));
  push?.('id: 1\nevent: connection.messages\ndata: {"subscription":"sub-2","messages":["next"]}\n\n');
  await vi.waitFor(() => expect(frames).toHaveLength(2));
  stop();
  unmount();
});
