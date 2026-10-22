// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { liveRefreshInterval, useLiveEvents } from './live';
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
