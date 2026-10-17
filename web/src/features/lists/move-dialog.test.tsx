// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
      if (url.pathname === '/v1/workspaces') body = { value: [resource('w', 'Finance', 'workspace')] };
      else if (url.pathname === '/v1/resources/w/children') body = { value: [{ ...resource('lib', 'Archive', 'library') }] };
      else if (url.pathname === '/v1/resources/lib' || url.pathname === '/v1/resources/f2') body = { ...resource(url.pathname.split('/').at(-1)!, 'Destination', 'folder'), access: ['read', 'write'] };
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
  await waitFor(() => expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(false));
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
    const body = path === '/v1/resources/w/children' ? { value: [collection] } : path === '/v1/resources/list/children' ? { value: [resource('folder', 'Archive', 'folder')] } : path === '/v1/resources/folder' ? { ...resource('folder', 'Archive', 'folder'), access: ['read', 'write'] } : path.endsWith('/move-selection') ? [{ id: 'a', version: 4 }, { id: 'b', version: 9 }] : { value: [] };
    return new Response(stringifyJSON(body));
  }));
  const moved = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MoveDialog item={a} items={[a, b]} collection={collection} onClose={vi.fn()} onMoved={moved} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'Archive' }));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'Move here' }));
  await waitFor(() => expect(moved).toHaveBeenCalled());
  const bulk = calls.filter(call => call.path.endsWith('/move-selection'));
  expect(bulk).toHaveLength(1);
  expect(parseJSON(String(bulk[0].init.body))).toEqual({ parent_id: 'folder', items: [{ id: 'a', version: 3 }, { id: 'b', version: 8 }] });
});

it.each(['Inbox', 'Project files'])('moves a file from %s to a library in another workspace, following destination pages', async (sourceName) => {
  const calls: { url: URL; init: RequestInit }[] = [];
  const source = resource('source', sourceName, 'library');
  const dest = { ...resource('dest', 'Shared documents', 'library'), workspace_id: 'team' };
  const item = { ...resource('file', 'Invoice.pdf', 'item'), container_id: source.id, parent_id: source.id, version: 7 };
  const onMoved = vi.fn();
  const onClose = vi.fn();
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const url = new URL(String(input));
    calls.push({ url, init });
    let body: unknown = { value: [] };
    if (url.pathname === '/v1/workspaces') body = url.searchParams.has('$skiptoken')
      ? { value: [{ ...resource('team', 'Team', 'workspace'), access: ['read'] }] }
      : { value: [resource('w', 'Home', 'workspace')], '@odata.nextLink': 'http://api:8080/v1/workspaces?$skiptoken=second' };
    if (url.pathname === '/v1/resources/w/children') body = { value: [source] };
    if (url.pathname === '/v1/resources/team/children') body = url.searchParams.has('$skiptoken')
      ? { value: [dest] }
      : { value: [], '@odata.nextLink': 'http://api:8080/v1/resources/team/children?kind=library&$skiptoken=second' };
    if (url.pathname === '/v1/resources/dest') body = { ...dest, access: ['read', 'write'] };
    if (url.pathname === '/v1/items/file/move') body = { ...item, workspace_id: 'team', container_id: dest.id, parent_id: dest.id, version: 8 };
    return new Response(stringifyJSON(body));
  }));
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MoveDialog item={item} collection={source} onClose={onClose} onMoved={onMoved} /></QueryClientProvider>);
  await screen.findByRole('option', { name: 'Team' });
  fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'team' } });
  expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(true);
  await screen.findByRole('option', { name: 'Shared documents' });
  fireEvent.change(screen.getByLabelText('Library'), { target: { value: 'dest' } });
  await waitFor(() => expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(false));
  const libraryPages = calls.filter(call => call.url.pathname === '/v1/resources/team/children');
  expect(libraryPages).toHaveLength(2);
  expect(libraryPages[0].url.searchParams.get('kind')).toBe('library');
  expect(libraryPages[1].url.origin).toBe(window.location.origin);
  fireEvent.click(screen.getByRole('button', { name: 'Move here' }));
  await waitFor(() => expect(onMoved).toHaveBeenCalledWith(expect.objectContaining({ workspace_id: 'team', container_id: 'dest' })));
  expect(onClose).toHaveBeenCalledOnce();
  const move = calls.find(call => call.url.pathname === '/v1/items/file/move')!;
  expect(parseJSON(String(move.init.body))).toEqual({ collection_id: 'dest' });
  expect((move.init.headers as Record<string, string>)['If-Match']).toBe('"7"');
});

it('checks each destination folder and clears the chosen folder when changing libraries', async () => {
  const source = resource('source', 'Documents', 'library');
  const readonly = resource('readonly', 'Read-only library', 'library');
  const writable = resource('writable', 'Writable library', 'library');
  const calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string) => {
    const url = new URL(String(input));
    calls.push(url.pathname);
    let body: unknown = { value: [] };
    if (url.pathname === '/v1/workspaces') body = { value: [resource('w', 'Workspace', 'workspace')] };
    if (url.pathname === '/v1/resources/w/children') body = { value: [source, readonly, writable] };
    if (url.pathname === '/v1/resources/readonly') body = { ...readonly, access: ['read'] };
    if (url.pathname === '/v1/resources/readonly/children') body = { value: [resource('contribute', 'Contribute here', 'folder')] };
    if (url.pathname === '/v1/resources/contribute') body = { ...resource('contribute', 'Contribute here', 'folder'), access: ['read', 'write'] };
    if (url.pathname === '/v1/resources/writable') body = { ...writable, access: ['read', 'write'] };
    if (url.pathname === '/v1/resources/writable/children') body = { value: [resource('restricted', 'Restricted folder', 'folder')] };
    if (url.pathname === '/v1/resources/restricted') body = { ...resource('restricted', 'Restricted folder', 'folder'), access: ['read'] };
    return new Response(stringifyJSON(body));
  }));
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MoveDialog item={{ ...resource('file', 'Scan', 'item'), parent_id: 'source' }} collection={source} onClose={vi.fn()} onMoved={vi.fn()} /></QueryClientProvider>);
  await screen.findByRole('option', { name: 'Read-only library' });
  fireEvent.change(screen.getByLabelText('Library'), { target: { value: 'readonly' } });
  await screen.findByText(/You don’t have write access here/);
  expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(await screen.findByRole('button', { name: 'Contribute here' }));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.change(screen.getByLabelText('Library'), { target: { value: 'writable' } });
  expect(within(screen.getByRole('navigation', { name: 'Destination folder' })).queryByRole('button', { name: 'Contribute here' })).toBeNull();
  fireEvent.click(await screen.findByRole('button', { name: 'Restricted folder' }));
  await screen.findByText(/You don’t have write access here/);
  expect((screen.getByRole('button', { name: 'Move here' }) as HTMLButtonElement).disabled).toBe(true);
  expect(calls.filter(path => path.endsWith('/move'))).toHaveLength(0);
});
