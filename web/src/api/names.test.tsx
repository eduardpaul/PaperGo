// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from './json';
import { resourceNames, termNames } from './names';
import { referencedIds, useResourceNames } from '@/features/fields/names';

afterEach(() => vi.unstubAllGlobals());

function respond(handle: (url: URL, init: RequestInit) => { status?: number; body: unknown }) {
  const fetch = vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const { status = 200, body } = handle(new URL(String(input), window.location.origin), init);
    return new Response(stringifyJSON(body), { status, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

it('reads looked-up item names in batches of 20 and marks unreadable ones', async () => {
  const ids = Array.from({ length: 21 }, (_, index) => `item-${index}`);
  const fetch = respond((_url, init) => {
    const { requests } = JSON.parse(String(init.body)) as { requests: { id: string; url: string }[] };
    return {
      body: {
        responses: requests.map((request) =>
          request.id === 'item-3'
            ? { id: request.id, status: 404, body: { code: 'not_found' } }
            : { id: request.id, status: 200, body: { id: request.id, name: `Name ${request.id}` } },
        ),
      },
    };
  });
  const names = await resourceNames(ids);
  expect(fetch).toHaveBeenCalledTimes(2);
  expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)).requests[0]).toEqual({ id: 'item-0', method: 'GET', url: '/v1/resources/item-0' });
  expect(names.get('item-0')).toBe('Name item-0');
  expect(names.get('item-20')).toBe('Name item-20');
  expect(names.get('item-3')).toBe('Unavailable item');
});

it('names terms, marks unknown ids and leaves ids alone without taxonomy access', async () => {
  respond((url) => {
    expect(url.pathname).toBe('/v1/terms');
    expect(url.searchParams.get('ids')).toBe('t1,t2');
    return { body: { value: [{ id: 't1', name: 'Finance' }] } };
  });
  const names = await termNames(['t1', 't2']);
  expect(names.get('t1')).toBe('Finance');
  expect(names.get('t2')).toBe('Unknown term');
  respond(() => ({ status: 403, body: { code: 'forbidden', detail: 'forbidden' } }));
  expect((await termNames(['t1'])).size).toBe(0);
});

it('collects distinct referenced ids of the requested field types', () => {
  const fields = [
    { id: 'a', key: 'customer', label: 'Customer', type: 'lookup' },
    { id: 'b', key: 'topics', label: 'Topics', type: 'keywords', options: { multiple: true } },
    { id: 'c', key: 'note', label: 'Note', type: 'text' },
  ];
  const rows = [
    { values: { customer: 'c2', topics: ['k1', 'k2'], note: 'x' } },
    { values: { customer: 'c1', topics: ['k2'] } },
    { values: null },
  ];
  expect(referencedIds(fields, rows, ['lookup'])).toEqual(['c1', 'c2']);
  expect(referencedIds(fields, rows, ['term', 'keywords'])).toEqual(['k1', 'k2']);
});

it('reads each name once, together with the other names asked for at the same time', async () => {
  const asked: string[][] = [];
  respond((_url, init) => {
    const { requests } = JSON.parse(String(init.body)) as { requests: { id: string }[] };
    asked.push(requests.map((request) => request.id));
    return { body: { responses: requests.map((request) => ({ id: request.id, status: 200, body: { name: `Name ${request.id}` } })) } };
  });
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result, rerender } = renderHook(({ ids }) => useResourceNames(ids), { wrapper, initialProps: { ids: ['a', 'b'] } });
  await waitFor(() => expect(result.current.get('b')).toBe('Name b'));
  rerender({ ids: ['a', 'b', 'c'] });
  await waitFor(() => expect(result.current.get('c')).toBe('Name c'));
  expect(asked).toEqual([['a', 'b'], ['c']]);
  client.clear();
});
