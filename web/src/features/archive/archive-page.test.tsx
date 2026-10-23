// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ComponentType, ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { Route } from '@/routes/_app/archive';

const flags = vi.hoisted(() => ({ search: false }));
vi.mock('@tanstack/react-router', async original => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options }),
  Link: ({ children }: { children: ReactNode }) => <a href="#item">{children}</a>,
}));
vi.mock('@/lib/search', () => ({ useSearchAvailable: () => flags.search }));
vi.mock('@/components/keyword-input', () => ({ KeywordInput: () => <input aria-label="Keyword" /> }));
vi.mock('@/features/fields/editor', () => ({ FieldEditor: () => <input aria-label="Archived by" /> }));

afterEach(() => { cleanup(); vi.unstubAllGlobals(); flags.search = false; });

const archived = { id: 'a', workspace_id: 'w', container_id: 'lib', parent_id: 'folder', kind: 'item', name: 'Archived invoice', version: 4, archived: true, archived_at: '2026-10-20T12:00:00Z', archived_by: 'alice', can_write: true };
function show(readonly = false) {
  const fetch = vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const url = new URL(String(input), window.location.origin);
    let body: unknown = {};
    if (url.pathname === '/v1/archive') body = { value: [{ ...archived, can_write: !readonly }], '@odata.nextLink': null };
    if (url.pathname === '/v1/workspaces') body = { value: [{ id: 'w', name: 'Finance', kind: 'workspace' }] };
    if (url.pathname === '/v1/search/content-types') body = [{ id: 'invoice', name: 'Invoice' }];
    if (url.pathname === '/v1/users') body = { value: [{ subject: 'alice', display_name: 'Alice' }] };
    if (url.pathname === '/v1/resources/lib') body = { id: 'lib', kind: 'library', name: 'Documents' };
    if (url.pathname === '/v1/resources/a') body = { ...archived, access: ['read', 'write'] };
    if (url.pathname === '/v1/search') body = { value: [{ ...archived, collection_id: 'lib', matched_by: ['keyword'], modified_at: archived.archived_at, modified_by: 'alice', surface: 'published', rank: 1 }], '@odata.nextLink': null };
    if (url.pathname === '/v1/$batch') {
      const { requests } = JSON.parse(String(init.body)) as { requests: { id: string }[] };
      body = { responses: requests.map(request => ({ id: request.id, status: 200, body: { id: request.id, name: request.id === 'folder' ? 'Reports' : 'Documents' } })) };
    }
    if (url.pathname.endsWith('/bulk')) body = { data: [] };
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const Component = Route.options.component as ComponentType;
  render(<QueryClientProvider client={client}><Component /></QueryClientProvider>);
  return { fetch, client };
}

it('browses the archive without search and unarchives selected items with their versions', async () => {
  const { fetch, client } = show();
  expect(await screen.findByRole('link', { name: 'Archived invoice' })).toBeTruthy();
  expect(screen.queryByRole('option', { name: 'Full text' })).toBeNull();
  expect(fetch.mock.calls.some(([input]) => new URL(String(input), window.location.origin).pathname === '/v1/search')).toBe(false);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Archived invoice' }));
  fireEvent.click(screen.getByRole('button', { name: 'Unarchive selected' }));
  await waitFor(() => expect(fetch.mock.calls.some(([input]) => String(input).includes('/v1/resources/lib/bulk'))).toBe(true));
  const [, init] = fetch.mock.calls.find(([input]) => String(input).includes('/v1/resources/lib/bulk'))!;
  expect(JSON.parse(String(init?.body))).toEqual({ operations: [{ action: 'unarchive', id: 'a', version: 4 }] });
  client.clear();
});

it('keeps archived items readable while hiding unarchive actions from readers', async () => {
  const { client } = show(true);
  expect(await screen.findByRole('link', { name: 'Archived invoice' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Unarchive' })).toBeNull();
  expect((screen.getByRole('checkbox', { name: 'Select Archived invoice' }) as HTMLInputElement).disabled).toBe(true);
  client.clear();
});

it('uses archived-only full-text search and preserves archive provenance filters', async () => {
  flags.search = true;
  const { fetch, client } = show();
  await screen.findByRole('link', { name: 'Archived invoice' });
  fireEvent.change(screen.getByLabelText('Search in'), { target: { value: 'fulltext' } });
  await waitFor(() => expect(fetch.mock.calls.some(([input]) => new URL(String(input), window.location.origin).pathname === '/v1/search')).toBe(true));
  const [input] = fetch.mock.calls.find(([input]) => new URL(String(input), window.location.origin).pathname === '/v1/search')!;
  expect(new URL(String(input)).searchParams.get('archive')).toBe('archived');
  expect(new URL(String(input)).searchParams.get('kind')).toBe('item');
  client.clear();
});
