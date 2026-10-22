// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { FieldDefinition, ListView } from '@/api/types';
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
const view: ListView = { id: 'v', container_id: 'list', name: 'By period', version: 1, layout: 'navigation', columns: ['$name'], settings: { field: 'period' } };

function level(parent: string | undefined, subterms: boolean) {
  const nodes = parent
    ? [{ term_id: 'q1', name: 'Q1', has_children: false, count: 1, subtree_count: 1 }]
    : [
        { term_id: 'fiscal', name: 'Fiscal', has_children: true, count: 1, subtree_count: 3 },
        { term_id: 'calendar', name: 'Calendar', has_children: false, count: 0, subtree_count: 0 },
      ];
  return { value: nodes, '@odata.nextLink': null, field: 'period', term_set_id: 'set', include_subterms: subterms, hide_empty: false, ...(parent ? { parent_id: parent } : { no_value_count: 2 }) };
}

function api(subterms: boolean) {
  return mockAPI(({ path, body }) => {
    if (path === '/v1/views/v/navigation') return level(body.parent_id as string | undefined, subterms);
    if (path === '/v1/views/v/query') return { value: [{ id: 'i', kind: 'item', name: body.no_value ? 'Loose' : 'Budget', version: 1, values: {} }], '@odata.nextLink': null };
    if (path === '/v1/terms/fiscal') return { id: 'fiscal', name: 'Fiscal', path: '/fiscal/' };
    if (path === '/v1/terms') return { value: [{ id: 'fiscal', name: 'Fiscal' }] };
    return {};
  });
}

it('browses terms as folders and explains what a term shows', async () => {
  const calls = api(false);
  const onTerm = vi.fn();
  renderWithClient(<ItemsNavigation view={view} fields={[field]} columns={['$name']} publishing={false} onTerm={onTerm} onOpen={vi.fn()} />);
  const tree = await screen.findByRole('tree', { name: 'Period' });
  expect(within(tree).getByText('Fiscal')).toBeTruthy();
  expect(screen.getByText('This term only')).toBeTruthy();
  // A child-only view says how many more items are tagged below a term.
  expect(within(tree).getByText('+2 below')).toBeTruthy();
  expect(screen.getByText('No period')).toBeTruthy();
  fireEvent.click(within(tree).getByRole('button', { name: 'Expand Fiscal' }));
  expect(await within(tree).findByText('Q1')).toBeTruthy();
  expect(calls.some((call) => call.path === '/v1/views/v/navigation' && call.body.parent_id === 'fiscal')).toBe(true);
  fireEvent.click(within(tree).getByText('Fiscal'));
  expect(onTerm).toHaveBeenCalledWith('fiscal');
});

it('lists the items of the opened term with its path', async () => {
  const calls = api(true);
  renderWithClient(<ItemsNavigation view={view} fields={[field]} columns={['$name']} publishing={false} term="fiscal" onTerm={vi.fn()} onOpen={vi.fn()} />);
  expect(await screen.findByText('Budget')).toBeTruthy();
  expect(screen.getByText('Includes subterms')).toBeTruthy();
  expect(await screen.findByText('Items tagged Fiscal or any term below it')).toBeTruthy();
  expect(within(screen.getByRole('navigation', { name: 'Term path' })).getByText('Fiscal')).toBeTruthy();
  // Subterms of the opened term are folders too.
  expect(within(await screen.findByRole('list', { name: 'Subterms' })).getByText('Q1')).toBeTruthy();
  expect(calls.find((call) => call.path === '/v1/views/v/query')?.body).toEqual({ term_id: 'fiscal' });
});

it('lists the items without a term', async () => {
  const calls = api(false);
  renderWithClient(<ItemsNavigation view={view} fields={[field]} columns={['$name']} publishing={false} term="none" onTerm={vi.fn()} onOpen={vi.fn()} />);
  expect(await screen.findByText('Loose')).toBeTruthy();
  expect(calls.find((call) => call.path === '/v1/views/v/query')?.body).toEqual({ no_value: true });
});
