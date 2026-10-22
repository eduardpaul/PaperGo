// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { FieldDefinition, ListView, NavigationNode, Term } from '@/api/types';
import { PreferencesProvider } from '@/lib/preferences';
import { ItemsNavigation } from './navigation-layout';

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
});

function renderWithClient(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <PreferencesProvider>{children}</PreferencesProvider>
    </QueryClientProvider>,
  );
}

type Call = { path: string; body: Record<string, unknown> };
function mockAPI(handle: (call: Call) => unknown) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const url = new URL(String(input), window.location.origin);
      const call = { path: url.pathname, body: (init.body ? parseJSON(String(init.body)) : {}) as Record<string, unknown> };
      calls.push(call);
      return new Response(stringifyJSON(handle(call) ?? {}), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

const field: FieldDefinition = { id: 'f', key: 'period', label: 'Period', type: 'term', indexed: true, options: { term_set_id: 'set' } };
function navigationView(match?: 'all' | 'any'): ListView {
  return {
    id: 'v',
    container_id: 'list',
    name: 'Browse',
    version: 1,
    layout: 'navigation',
    columns: ['$name'],
    settings: { fields: [{ field: 'period' }, { field: '$keywords' }], match },
  };
}

const nodes: Record<string, NavigationNode[]> = {
  'period/': [
    { term_id: 'fiscal', name: 'Fiscal', has_children: true, count: 1, subtree_count: 3 },
    { term_id: 'calendar', name: 'Calendar', has_children: false, count: 0, subtree_count: 0 },
  ],
  'period/fiscal': [{ term_id: 'q1', name: 'Q1', has_children: false, count: 1, subtree_count: 1 }],
  '$keywords/': [{ term_id: 'finance', name: 'Finance', has_children: true, count: 2, subtree_count: 2 }],
  '$keywords/finance': [{ term_id: 'invoices', name: 'Invoices', has_children: false, count: 1, subtree_count: 1 }],
};
const terms: Record<string, Term> = {
  fiscal: { id: 'fiscal', name: 'Fiscal', path: '/fiscal/' } as Term,
  finance: { id: 'finance', name: 'Finance', path: '/finance/' } as Term,
};

function api() {
  return mockAPI(({ path, body }) => {
    if (path === '/v1/views/v/navigation') {
      const field = body.field as string;
      const parent = body.parent_id as string | undefined;
      return {
        value: nodes[`${field}/${parent ?? ''}`] ?? [],
        '@odata.nextLink': null,
        field,
        term_set_id: 'set',
        include_subterms: field === '$keywords',
        hide_empty: false,
        ...(parent ? { parent_id: parent } : { no_value_count: 2 }),
      };
    }
    if (path === '/v1/views/v/query') {
      const empty = (body.selected as unknown[]).length > 2;
      return { value: empty ? [] : [{ id: 'i', kind: 'item', name: 'Budget', version: 1, values: {} }], '@odata.nextLink': null, total: empty ? 0 : 1 };
    }
    if (path.startsWith('/v1/terms/')) return terms[path.slice('/v1/terms/'.length)];
    if (path === '/v1/terms') return { value: Object.values(terms) };
    return {};
  });
}

const navigationCalls = (calls: Call[], field: string) => calls.filter((call) => call.path === '/v1/views/v/navigation' && call.body.field === field);

it('browses each field as a tree of folders and explains what a term shows', async () => {
  const calls = api();
  const onSelect = vi.fn();
  renderWithClient(<ItemsNavigation view={navigationView()} fields={[field]} columns={['$name']} publishing={false} selected={[]} onSelect={onSelect} onOpen={vi.fn()} />);
  const period = await screen.findByRole('tree', { name: 'Period' });
  const keywords = await screen.findByRole('tree', { name: 'Keywords' });
  expect(within(keywords).getByText('Finance')).toBeTruthy();
  expect(screen.getByText('This term only')).toBeTruthy();
  expect(screen.getByText('Includes subterms')).toBeTruthy();
  // A child-only tree says how many more items are tagged below a term.
  expect(within(period).getByText('+2 below')).toBeTruthy();
  expect(within(screen.getByRole('navigation', { name: 'Period navigation' })).getByText('No period')).toBeTruthy();
  // The top level shows the first field's terms, and the items without one, as folders.
  expect(within(screen.getByRole('list', { name: 'Period terms' })).getByText('No period')).toBeTruthy();
  fireEvent.click(within(period).getByRole('button', { name: 'Expand Fiscal' }));
  expect(await within(period).findByText('Q1')).toBeTruthy();
  expect(navigationCalls(calls, 'period').some((call) => call.body.parent_id === 'fiscal')).toBe(true);
  fireEvent.click(within(period).getByText('Fiscal'));
  expect(onSelect).toHaveBeenCalledWith([{ field: 'period', term_id: 'fiscal' }]);
});

it('lists the items matching every opened term and counts each tree within the others', async () => {
  const calls = api();
  const onSelect = vi.fn();
  const selected = [
    { field: 'period', term_id: 'fiscal' },
    { field: '$keywords', term_id: 'finance' },
  ];
  renderWithClient(<ItemsNavigation view={navigationView()} fields={[field]} columns={['$name']} publishing={false} selected={selected} onSelect={onSelect} onOpen={vi.fn()} />);
  expect(await screen.findByText('Budget')).toBeTruthy();
  expect(screen.getByText('Items matching all of')).toBeTruthy();
  const chips = screen.getByRole('list', { name: 'Opened terms' });
  expect(await within(chips).findByText('Fiscal')).toBeTruthy();
  expect(await within(chips).findByText('Finance')).toBeTruthy();
  expect(within(chips).getByText('and')).toBeTruthy();
  expect(await screen.findByText(/its items and those tagged with any term below it/)).toBeTruthy();
  expect(screen.getByRole('status').textContent).toBe('1 item');
  expect(calls.find((call) => call.path === '/v1/views/v/query')?.body).toEqual({ selected, total_limit: 1000 });
  // Each tree counts the items holding the terms opened in the other one.
  expect(navigationCalls(calls, 'period').every((call) => stringifyJSON(call.body.selected) === stringifyJSON([selected[1]]))).toBe(true);
  expect(navigationCalls(calls, '$keywords').every((call) => stringifyJSON(call.body.selected) === stringifyJSON([selected[0]]))).toBe(true);
  // The latest opened term's subterms are folders.
  expect(within(await screen.findByRole('list', { name: 'Subterms' })).getByText('Invoices')).toBeTruthy();
  fireEvent.click(within(chips).getByRole('button', { name: 'Remove Period' }));
  expect(onSelect).toHaveBeenCalledWith([selected[1]]);
  // Each tree names its opened term and clears it; all terms clear at once.
  fireEvent.click(screen.getByRole('button', { name: 'Clear Keywords' }));
  expect(onSelect).toHaveBeenLastCalledWith([selected[0]]);
  fireEvent.click(screen.getByRole('button', { name: 'Clear all' }));
  expect(onSelect).toHaveBeenLastCalledWith([]);
});

it('lists the items matching any opened term and counts the whole view', async () => {
  const calls = api();
  const selected = [
    { field: 'period', no_value: true },
    { field: '$keywords', term_id: 'finance' },
  ];
  renderWithClient(<ItemsNavigation view={navigationView('any')} fields={[field]} columns={['$name']} publishing={false} selected={selected} onSelect={vi.fn()} onOpen={vi.fn()} />);
  expect(await screen.findByText('Budget')).toBeTruthy();
  expect(screen.getByText('Items matching any of')).toBeTruthy();
  expect(within(screen.getByRole('list', { name: 'Opened terms' })).getByText('or')).toBeTruthy();
  expect(screen.getByText('No period: the items without a period.')).toBeTruthy();
  expect(calls.find((call) => call.path === '/v1/views/v/query')?.body).toEqual({ selected, total_limit: 1000 });
  expect(calls.filter((call) => call.path === '/v1/views/v/navigation').every((call) => call.body.selected === undefined)).toBe(true);
});

it('says how to widen a selection no item matches', async () => {
  api();
  const onSelect = vi.fn();
  const fields = [{ field: 'period' }, { field: '$keywords' }, { field: 'tags' }];
  const view = { ...navigationView(), settings: { fields } };
  const selected = [
    { field: 'period', term_id: 'fiscal' },
    { field: '$keywords', term_id: 'finance' },
    { field: 'tags', no_value: true },
  ];
  renderWithClient(<ItemsNavigation view={view} fields={[field]} columns={['$name']} publishing={false} selected={selected} onSelect={onSelect} onOpen={vi.fn()} />);
  expect(await screen.findByText('No items have all of these terms')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Remove the last opened term' }));
  expect(onSelect).toHaveBeenCalledWith(selected.slice(0, 2));
});
