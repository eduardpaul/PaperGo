// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { FieldDefinition } from '@/api/types';
import { ViewsEditor } from './views-editor';

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
});

const fields: FieldDefinition[] = [
  { id: 'a', key: 'period', label: 'Period', type: 'term', indexed: true, options: { term_set_id: 'set' } },
  { id: 'b', key: 'tags', label: 'Tags', type: 'term', indexed: true, options: { term_set_id: 'set', multiple: true, apply_parents: true } },
  { id: 'c', key: 'amount', label: 'Amount', type: 'integer', indexed: true },
];

function setup() {
  const posts: Record<string, unknown>[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const path = new URL(String(input), window.location.origin).pathname;
      if (init.method === 'POST') posts.push(parseJSON(String(init.body)) as Record<string, unknown>);
      const body = path === '/v1/resources/list' ? { id: 'list', kind: 'list', name: 'List', access: ['read', 'write', 'manage'] } : { value: [], '@odata.nextLink': null };
      return new Response(stringifyJSON(body), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <ViewsEditor collectionId="list" fields={fields} contentTypes={[]} />
    </QueryClientProvider>,
  );
  return posts;
}

it('saves a navigation view browsing several columns and explains subterms', async () => {
  const posts = setup();
  fireEvent.click(await screen.findByRole('button', { name: /New view/ }));
  fireEvent.change(screen.getByLabelText('Name', { selector: '#view-name' }), { target: { value: 'Browse' } });
  fireEvent.change(screen.getByLabelText('Layout'), { target: { value: 'navigation' } });
  const save = screen.getByRole<HTMLButtonElement>('button', { name: 'Save view' });
  expect(save.disabled).toBe(true);
  const add = () => screen.getByLabelText<HTMLSelectElement>('Add a column to browse');
  // Item keywords and taxonomy columns, not other columns.
  expect([...add().options].map((option) => option.value)).toEqual(['', '$keywords', 'period', 'tags']);
  fireEvent.change(add(), { target: { value: 'tags' } });
  fireEvent.change(add(), { target: { value: 'period' } });
  expect([...add().options].map((option) => option.value)).toEqual(['', '$keywords']);
  // A column that applies parent terms always shows the items of subterms.
  const tags = within(screen.getByRole('listitem', { name: 'Tags' }));
  const tagSubterms = tags.getByLabelText<HTMLInputElement>('Show items of subterms at each term');
  expect(tagSubterms.checked && tagSubterms.disabled).toBe(true);
  expect(tags.getByText(/applies parent terms/)).toBeTruthy();
  const period = within(screen.getByRole('listitem', { name: 'Period' }));
  const periodSubterms = period.getByLabelText<HTMLInputElement>('Show items of subterms at each term');
  expect(periodSubterms.checked || periodSubterms.disabled).toBe(false);
  fireEvent.click(periodSubterms);
  fireEvent.click(period.getByRole('button', { name: 'Move Period up' }));
  fireEvent.change(screen.getByLabelText('Show items that match'), { target: { value: 'any' } });
  expect(screen.getByText(/tagged with either/)).toBeTruthy();
  fireEvent.click(screen.getByLabelText('Hide terms without items'));
  fireEvent.click(save);
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({
    name: 'Browse',
    layout: 'navigation',
    settings: { fields: [{ field: 'period', include_subterms: true }, { field: 'tags' }], match: 'any', hide_empty: true },
  });
});
