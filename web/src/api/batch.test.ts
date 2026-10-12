// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { batch } from './batch';

afterEach(() => vi.unstubAllGlobals());
it('chunks at 20 and reports embedded failures after processing subsequent requests', async () => {
  const fetch = vi.fn(async (_url: URL | string, options?: RequestInit) => {
    const { requests } = JSON.parse(String(options?.body)) as { requests: { id: string }[] };
    return new Response(
      JSON.stringify({
        responses: requests.map((request) => ({
          id: request.id,
          status: request.id === '0' ? 412 : 204,
          body: request.id === '0' ? { detail: 'Item changed' } : undefined,
        })),
      }),
    );
  });
  vi.stubGlobal('fetch', fetch);
  await expect(
    batch(
      Array.from({ length: 21 }, (_, index) => ({
        id: String(index),
        method: 'DELETE',
        url: `/v1/resources/${index}`,
        headers: { 'If-Match': '"1"' },
      })),
    ),
  ).rejects.toThrow('1 actions failed: Item changed');
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)).requests).toHaveLength(1);
});
