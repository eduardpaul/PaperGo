// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { Resource } from '@/api/types';
import { ItemContextMenu } from './item-context-menu';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const collection: Resource = { id: 'list', workspace_id: 'w', kind: 'list', name: 'List', version: 1 };
const item: Resource = { id: 'one', workspace_id: 'w', container_id: 'list', kind: 'item', name: 'One', version: 1 };
function show(access: string[], selection = [item]) {
  const fetch = vi.fn(async (input: URL | string, init?: RequestInit) => {
    const url = new URL(String(input));
    const body = url.pathname === '/v1/resources/one' ? { ...item, version: 4, access } : url.pathname === '/v1/$batch' ? { responses: selection.map(item => ({ id: item.id, status: 204 })) } : { value: [] };
    return new Response(JSON.stringify(body), { status: init?.method === 'DELETE' ? 204 : 200 });
  });
  vi.stubGlobal('fetch', fetch);
  const select = vi.fn();
  const details = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><ItemContextMenu item={item} collection={collection} selection={selection} onSelect={select} onOpen={vi.fn()} onDetails={details}><button>One</button></ItemContextMenu></QueryClientProvider>);
  fireEvent.contextMenu(screen.getByRole('button', { name: 'One' }), { clientX: 120, clientY: 80 });
  return { fetch, select, details, client };
}
it('opens a context menu and gates mutation actions with fresh permissions', async () => {
  const { details, client } = show(['read']);
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Details' }));
  expect(details).toHaveBeenCalledWith(expect.objectContaining({ id: 'one' }));
  expect(screen.queryByRole('menuitem', { name: 'Rename…' })).toBeNull();
  client.clear();
});
it('retains the selected targets and conditionally deletes the whole selection', async () => {
  const second = { ...item, id: 'two', name: 'Two', version: 7 };
  const { fetch, select, client } = show(['read', 'write'], [item, second]);
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete selected…' }));
  expect(select).toHaveBeenCalledOnce();
  expect(screen.getByRole('alertdialog').textContent).toContain('Delete 2 selected entries?');
  fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(fetch.mock.calls.some(([input]) => new URL(String(input)).pathname === '/v1/$batch')).toBe(true));
  const [, init] = fetch.mock.calls.find(([input]) => new URL(String(input)).pathname === '/v1/$batch')!;
  expect(JSON.parse(String(init?.body)).requests).toEqual([
    expect.objectContaining({ id: 'one', headers: { 'If-Match': '"4"' } }),
    expect.objectContaining({ id: 'two', headers: { 'If-Match': '"7"' } }),
  ]);
  client.clear();
});
