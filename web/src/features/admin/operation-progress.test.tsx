// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { OperationProgress } from './operation-progress';
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('polls a pending export and offers the package when it succeeds', async () => {
  let reads = 0;
  const fetch = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          id: 'export',
          kind: 'portability.export',
          status: ++reads === 1 ? 'pending' : 'succeeded',
          progress: reads === 1 ? 25 : 100,
        }),
      ),
  );
  vi.stubGlobal('fetch', fetch);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { gcTime: 0, retry: false } } })}>
      <OperationProgress id="export" label="Export" />
    </QueryClientProvider>,
  );
  await screen.findByText('pending');
  expect(screen.queryByRole('button', { name: 'Download package' })).toBeNull();
  await screen.findByRole('button', { name: 'Download package' }, { timeout: 5000 });
  expect((screen.getByLabelText('Export progress') as HTMLProgressElement).value).toBe(100);
  expect(reads).toBe(2);
});
