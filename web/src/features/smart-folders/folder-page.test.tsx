// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { SmartFolderPage } from './folder-page';

const navigate = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate, Link: ({ children }: { children: ReactNode }) => <a>{children}</a> }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); navigate.mockReset(); });
function show(mode = 'query', editable = true) {
  const requests: { path: string; body: { sort?: { field: string }; path?: unknown } }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const path = new URL(String(input)).pathname;
    const body = init.body ? JSON.parse(String(init.body)) : {};
    requests.push({ path, body });
    let result: unknown = { value: [] };
    if (path === '/v1/identity/me') result = { subject: 'alice', scopes: editable ? ['list.read', 'list.write'] : ['list.read'] };
    if (path === '/v1/smart-folders/f') result = { id: 'f', name: 'My rules', owner_id: 'alice', version: 3, definition: { mode, group_by: [{ field: '$modified_at', by: 'year' }] } };
    if (path === '/v1/search/fields') result = { value: [] };
    if (path === '/v1/search/content-types') result = [];
    if (path === '/v1/smart-folders/f/query') result = { total: 1, value: [{ workspace_id: 'w', collection_id: 'lib', collection_name: 'Source library', collection_kind: 'library', item: { id: 'i', workspace_id: 'w', container_id: 'lib', name: 'Visible item', kind: 'item', version: 2 } }] };
    if (path.endsWith('/query/groups')) result = { value: [{ value: '2026', label: '2026', count: 1 }] };
    return new Response(JSON.stringify(result));
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><SmartFolderPage folderId="f" /></QueryClientProvider>);
  return { requests, client };
}
it('opens matching items directly with provenance, sorting and optional group browsing', async () => {
  const { requests, client } = show();
  expect(await screen.findByText('Visible item')).toBeTruthy();
  expect(screen.getByText('Source library')).toBeTruthy();
  expect(screen.getByText('Saved search')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Classify item…' })).toBeNull();
  expect(requests.some(request => request.path.endsWith('/query/groups'))).toBe(false);
  fireEvent.change(screen.getByLabelText('Sort results'), { target: { value: '$name:asc' } });
  await waitFor(() => expect(requests.some(request => request.path.endsWith('/query') && request.body.sort?.field === '$name')).toBe(true));
  fireEvent.change(screen.getByLabelText('Results layout'), { target: { value: 'gallery' } });
  expect(await screen.findByText('Visible item')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Browse groups' }));
  fireEvent.click(await screen.findByRole('button', { name: /2026/ }));
  await waitFor(() => expect(requests.some(request => request.path.endsWith('/query') && JSON.stringify(request.body.path) === '["2026"]')).toBe(true));
  client.clear();
});
it('shows classification actions only for that mode and hides management for readers', async () => {
  const { client } = show('classification', false);
  expect(await screen.findByText('Classification folder')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Edit rules…' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Classify item…' })).toBeNull();
  client.clear();
});
