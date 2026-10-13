// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { liveRefreshInterval, useLiveEvents } from './live';

afterEach(() => vi.unstubAllGlobals());

it('refreshes once for a burst of item events', async () => {
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
  await waitFor(() => expect(invalidate).toHaveBeenCalled(), { timeout: liveRefreshInterval * 3 });
  await new Promise((resolve) => setTimeout(resolve, liveRefreshInterval * 1.5));
  // Resources, home and inboxes, once.
  expect(invalidate).toHaveBeenCalledTimes(3);
  unmount();
  client.clear();
});
