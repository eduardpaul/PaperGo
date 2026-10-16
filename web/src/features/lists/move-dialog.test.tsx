// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Resource } from '@/api/types';
import { MoveDialog } from './move-dialog';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const resource = (id: string, name: string, kind: Resource['kind']): Resource => ({ id, workspace_id: 'w', kind, name, version: 1 });

it('lists only folders, page by page, and moves into the chosen one', async () => {
  const calls: { path: string; search: URLSearchParams; init: RequestInit }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const url = new URL(String(input), window.location.origin);
      calls.push({ path: url.pathname, search: url.searchParams, init });
      let body: unknown = { value: [] };
      if (url.pathname === '/v1/resources/w/children') body = { value: [{ ...resource('lib', 'Archive', 'library') }] };
      else if (url.pathname === '/v1/resources/lib/children' && url.searchParams.get('$skiptoken') === 'next')
        body = { value: [resource('f2', 'Letters', 'folder')], '@odata.nextLink': null };
      else if (url.pathname === '/v1/resources/lib/children')
        body = { value: [resource('f1', 'Invoices', 'folder')], '@odata.nextLink': `${window.location.origin}/v1/resources/lib/children?kind=folder&$skiptoken=next` };
      else if (url.pathname === '/v1/items/item/move') body = { ...resource('item', 'Scan', 'item'), parent_id: 'f2' };
      return new Response(stringifyJSON(body), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const onMoved = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MoveDialog
        item={{ ...resource('item', 'Scan', 'item'), parent_id: 'lib', container_id: 'lib' }}
        collection={resource('lib', 'Archive', 'library')}
        onClose={vi.fn()}
        onMoved={onMoved}
      />
    </QueryClientProvider>,
  );
  expect(await screen.findByRole('button', { name: 'Invoices' })).toBeTruthy();
  const first = calls.find((call) => call.path === '/v1/resources/lib/children');
  expect(first?.search.get('kind')).toBe('folder');
  fireEvent.click(screen.getByRole('button', { name: 'More folders' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Letters' }));
  await waitFor(() => expect(calls.some((call) => call.path === '/v1/resources/f2/children')).toBe(true));
  fireEvent.click(screen.getByRole('button', { name: 'Move here' }));
  await waitFor(() => expect(onMoved).toHaveBeenCalled());
  const move = calls.find((call) => call.path === '/v1/items/item/move');
  expect(parseJSON(String(move?.init.body))).toEqual({ collection_id: 'lib', parent_id: 'f2' });
});

it('moves a selection between folders with one atomic operation and each item version', async () => {
  const calls: { path: string; init: RequestInit }[] = [];
  const collection = resource('list', 'Tasks', 'list');
  const a = { ...resource('a', 'First', 'item'), parent_id: 'list', version: 3 };
  const b = { ...resource('b', 'Second', 'item'), parent_id: 'list', version: 8 };
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const path = new URL(String(input)).pathname;
    calls.push({ path, init });
    const body = path === '/v1/resources/w/children' ? { value: [collection] } : path === '/v1/resources/list/children' ? { value: [resource('folder', 'Archive', 'folder')] } : path.endsWith('/move-selection') ? [{ id: 'a', version: 4 }, { id: 'b', version: 9 }] : { value: [] };
    return new Response(stringifyJSON(body));
  }));
  const moved = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MoveDialog item={a} items={[a, b]} collection={collection} onClose={vi.fn()} onMoved={moved} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'Archive' }));
  fireEvent.click(screen.getByRole('button', { name: 'Move here' }));
  await waitFor(() => expect(moved).toHaveBeenCalled());
  const bulk = calls.filter(call => call.path.endsWith('/move-selection'));
  expect(bulk).toHaveLength(1);
  expect(parseJSON(String(bulk[0].init.body))).toEqual({ parent_id: 'folder', items: [{ id: 'a', version: 3 }, { id: 'b', version: 8 }] });
});
