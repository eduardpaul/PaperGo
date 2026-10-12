import { afterEach, expect, it, vi } from 'vitest';
import { allPages } from './pages';
afterEach(() => vi.unstubAllGlobals());
it('loads the entire taxonomy directory through proxy-safe continuation URLs', async () => {
  vi.stubGlobal('window', { location: { origin: 'http://localhost:5173' } });
  const fetch = vi.fn(
    async (url: URL) =>
      new Response(
        JSON.stringify(
          url.searchParams.has('$skiptoken')
            ? { value: [{ id: 'second' }] }
            : { value: [{ id: 'first' }], '@odata.nextLink': 'http://api:8080/v1/term-sets?$skiptoken=next&$top=200' },
        ),
      ),
  );
  vi.stubGlobal('fetch', fetch);
  expect(await allPages('/v1/term-sets')).toEqual([{ id: 'first' }, { id: 'second' }]);
  expect(fetch.mock.calls[1][0].origin).toBe('http://localhost:5173');
  expect(fetch.mock.calls[1][0].searchParams.get('$skiptoken')).toBe('next');
});
