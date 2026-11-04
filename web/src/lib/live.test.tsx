// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { liveRefreshInterval, onItemChange, useLiveEvents, type ItemChange } from './live';
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

it('tells item listeners which item changed, in which list, and when to read everything again', async () => {
  const frames = 'id: 1\nevent: item\ndata: {"id":"i1","action":"updated","item":{"id":"i1","workspace_id":"w1","container_id":"l1","content_type_id":"t1"}}\n\nid: 2\nevent: item\ndata: {"id":"i2","workspace_id":"w1","container_id":"l1","content_type_id":"t2","@removed":{"reason":"deleted"}}\n\nid: 3\nevent: resync\ndata: {}\n\n';
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
    { id: 'i1', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't1', removed: false },
    { id: 'i2', workspaceId: 'w1', collectionId: 'l1', contentTypeId: 't2', removed: true },
    { id: '', resync: true },
  ]);
  stop();
  unmount();
});
