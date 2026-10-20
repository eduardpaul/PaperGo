// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ComponentType, ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { SearchHit } from '@/api/types';
import { Route } from '@/routes/_app/search';

const navigation = vi.hoisted(() => ({ search: {} as Record<string, string | undefined>, navigate: vi.fn() }));
vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options, useSearch: () => navigation.search, fullPath: '/search' }),
  useNavigate: () => navigation.navigate,
  Link: ({ children }: { children: ReactNode }) => <a href="#workspace">{children}</a>,
}));

afterEach(() => {
  cleanup();
  catalogRoles = ['search.index'];
  navigation.search = {};
  navigation.navigate.mockReset();
  vi.unstubAllGlobals();
});

function hit(id: string, name: string, extra: Partial<SearchHit> = {}): SearchHit {
  return {
    id,
    kind: 'item',
    surface: 'published',
    workspace_id: 'ws1',
    collection_id: 'col1',
    name,
    matched_by: ['keyword'],
    rank: 1,
    modified_at: '2026-10-13T10:00:00Z',
    modified_by: 'alice',
    ...extra,
  };
}

// catalogRoles are the workflow roles the deployment registers: search's
// when it runs search.
let catalogRoles = ['search.index'];

function stub(search: (url: URL) => unknown) {
  const fetch = vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const url = new URL(String(input), window.location.origin);
    let body: unknown;
    if (url.pathname === '/v1/workspaces')
      body = {
        value: [
          { id: 'ws1', kind: 'workspace', name: 'Finance' },
          { id: 'ws2', kind: 'workspace', name: 'Legal' },
        ],
      };
    else if (url.pathname === '/v1/terms') body = { value: [{ id: 'paid', name: 'Paid' }] };
    else if (url.pathname === '/v1/workflow-catalog') body = { roles: catalogRoles.map((key) => ({ key })) };
    else if (url.pathname === '/v1/search/content-types') body = [{ id: 'invoice', name: 'Invoice type' }];
    else if (url.pathname.endsWith('/relationships')) body = { value: [{ id: 'link', source_id: 'a', target_id: 'other', type_id: 'rel', directed: true, related_item: { id: 'other', name: 'Related contract', workspace_id: 'ws1', container_id: 'col1' } }] };
    else if (url.pathname === '/v1/relationship-types') body = { value: [{ id: 'rel', term: { name: 'Contract' } }] };
    else if (url.pathname === '/v1/$batch') {
      const { requests } = JSON.parse(String(init.body)) as { requests: { id: string }[] };
      body = {
        responses: requests.map((request) => ({
          id: request.id,
          status: 200,
          body: { id: request.id, name: request.id === 'col1' ? 'Invoices' : 'Contracts' },
        })),
      };
    } else if (url.pathname === '/v1/search') body = search(url);
    else body = {};
    return new Response(stringifyJSON(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const SearchPage = Route.options.component as ComponentType;
  return render(
    <QueryClientProvider client={client}>
      <SearchPage />
    </QueryClientProvider>,
  );
}

it('says so when the deployment runs without search', async () => {
  catalogRoles = [];
  const fetch = stub(() => ({ value: [] }));
  show();
  expect(await screen.findByText('Search is not set up')).toBeTruthy();
  expect(fetch.mock.calls.some(([input]) => new URL(String(input)).pathname === '/v1/search')).toBe(false);
});

it('asks for words or a filter before searching', async () => {
  const fetch = stub(() => ({ value: [] }));
  show();
  expect(await screen.findByText('Type to search')).toBeTruthy();
  expect(fetch.mock.calls.some(([input]) => new URL(String(input)).pathname === '/v1/search')).toBe(false);
});

it('shows hits with highlighted snippets, totals and facets that narrow the search', async () => {
  navigation.search = { q: 'invoice' };
  const fetch = stub(() => ({
    value: [
      hit('a', 'Invoice March', { snippet: 'Paid invoice for March', highlights: [[5, 12]], page: 3 }),
      hit('b', 'Invoice April', { surface: 'head' }),
      hit('c', 'Electricity bill', { matched_by: ['semantic'], page: 2 }),
    ],
    total: 3,
    total_estimated: true,
    mode: 'keyword',
    facets: {
      workspace: [{ value: 'ws1', count: 3 }],
      collection: [
        { value: 'col1', count: 2 },
        { value: 'col2', count: 1 },
      ],
      content_type: [],
      term: [],
      keyword: [{ value: 'paid', count: 1 }],
    },
    '@odata.nextLink': null,
  }));
  show();
  expect(await screen.findByText('Invoice March')).toBeTruthy();
  const request = fetch.mock.calls
    .map(([input]) => new URL(String(input), window.location.origin))
    .find((url) => url.pathname === '/v1/search')!;
  expect(request.searchParams.get('q')).toBe('invoice');
  expect(request.searchParams.get('kind')).toBe('item');
  expect(screen.getByText('About 3 results')).toBeTruthy();
  const snippet = screen.getAllByTestId('snippet')[0];
  expect(within(snippet).getByText('invoice').tagName).toBe('MARK');
  expect(screen.getByText('Draft')).toBeTruthy();
  expect(screen.getByText(/page 3/)).toBeTruthy();
  expect(screen.getByText(/page 2 · similar meaning/)).toBeTruthy();
  expect(screen.getAllByText(/similar meaning/).length).toBe(1);
  await waitFor(() => expect(screen.getAllByText(/Invoices · Finance/).length).toBe(3));

  const lists = screen.getByRole('region', { name: 'Lists and libraries' });
  fireEvent.click(await within(lists).findByText('Contracts'));
  const update = navigation.navigate.mock.lastCall![0].search as (current: object) => object;
  expect(update({ q: 'invoice' })).toEqual({ q: 'invoice', collection: 'col2' });
});

it('sends facet filters and clears them from their chips', async () => {
  navigation.search = { q: 'invoice', workspace: 'ws1', keyword: 'paid', kind: 'all' };
  const fetch = stub(() => ({
    value: [hit('a', 'Invoice March')],
    total: 1,
    facets: { workspace: [], collection: [], content_type: [], term: [], keyword: [] },
  }));
  show();
  expect(await screen.findByText('Invoice March')).toBeTruthy();
  const request = fetch.mock.calls
    .map(([input]) => new URL(String(input), window.location.origin))
    .find((url) => url.pathname === '/v1/search')!;
  expect(request.searchParams.get('workspace_id')).toBe('ws1');
  expect(request.searchParams.get('keyword')).toBe('paid');
  expect(request.searchParams.get('kind')).toBe('item,folder');
  fireEvent.click(screen.getByRole('button', { name: 'Remove filter Paid' }));
  const update = navigation.navigate.mock.lastCall![0].search as (current: object) => object;
  expect(update({ q: 'invoice', workspace: 'ws1', keyword: 'paid' })).toEqual({ q: 'invoice', workspace: 'ws1', keyword: undefined });
});

it('offers workspaces whose names match, which the index does not hold', async () => {
  navigation.search = { q: 'fin' };
  stub(() => ({ value: [], total: 0, facets: { workspace: [], collection: [], content_type: [], term: [], keyword: [] } }));
  show();
  const places = await screen.findByRole('navigation', { name: 'Workspaces' });
  expect(within(places).getByText('Finance')).toBeTruthy();
  expect(within(places).queryByText('Legal')).toBeNull();
  expect(await screen.findByText('Nothing matches “fin”')).toBeTruthy();
});

it('filters by content type without requiring text and clears the URL filter', async () => {
  navigation.search = { content_type: 'invoice' };
  const fetch = stub(() => ({ value: [hit('a', 'Invoice March')], total: 1 }));
  show();
  expect(await screen.findByText('Invoice March')).toBeTruthy();
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Content type' }).textContent).toContain('Invoice type'));
  const request = fetch.mock.calls.map(([input]) => new URL(String(input))).find(url => url.pathname === '/v1/search')!;
  expect(request.searchParams.get('content_type_id')).toBe('invoice');
  fireEvent.click(screen.getByRole('button', { name: 'Remove filter Invoice type' }));
  expect(navigation.navigate.mock.lastCall![0].search({ content_type: 'invoice' })).toEqual({ content_type: undefined });
});

it('loads related badges only on expansion and displays the authorized target name', async () => {
  navigation.search = { q: 'invoice' };
  const fetch = stub(() => ({ value: [hit('a', 'Invoice March')], total: 1 }));
  show();
  expect(await screen.findByText('Invoice March')).toBeTruthy();
  expect(fetch.mock.calls.some(([input]) => String(input).includes('/relationships'))).toBe(false);
  const details = screen.getByText('Relationships').closest('details')!;
  details.open = true;
  fireEvent(details, new Event('toggle'));
  expect(await screen.findByRole('link', { name: 'Contract → Related contract' })).toBeTruthy();
});
