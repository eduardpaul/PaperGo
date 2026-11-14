// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { TermStore } from './taxonomy';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const noUse = {
  child_terms: 0,
  items: 0,
  deleted_items: 0,
  workspaces: 0,
  collections: 0,
  fields: 0,
  content_types: 0,
  templates: 0,
  views: 0,
  smart_folders: 0,
  workflows: 0,
  relationships: 0,
};

/** Serves a keywords set with Budget, which two items and a recycled one hold, and Junk, which nothing holds. */
function serve() {
  const calls: { method: string; path: string; ifMatch?: string }[] = [];
  let terms = [
    { id: 'budget', term_set_id: 'kw', name: 'Budget', has_children: false, version: 2 },
    { id: 'junk', term_set_id: 'kw', name: 'Junk', has_children: false, version: 3 },
  ];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const path = new URL(String(input), window.location.href).pathname;
      const method = init.method ?? 'GET';
      calls.push({ method, path, ifMatch: new Headers(init.headers).get('If-Match') ?? undefined });
      if (method === 'DELETE' && path === '/v1/terms/budget') {
        return new Response(
          JSON.stringify({ status: 409, code: 'term_in_use', detail: 'the term is in use', usage: { ...noUse, items: 2, deleted_items: 1 } }),
          { status: 409 },
        );
      }
      if (method === 'DELETE' && path === '/v1/terms/junk') {
        terms = terms.filter((term) => term.id !== 'junk');
        return new Response(null, { status: 204 });
      }
      let data: unknown = { value: [] };
      if (path === '/v1/identity/me') data = { subject: 'admin', scopes: ['taxonomy.read', 'taxonomy.manage'] };
      if (path === '/v1/term-groups') data = { value: [{ id: 'system', name: 'System', description: '', is_system: true, version: 1 }] };
      if (path === '/v1/term-sets') {
        data = { value: [{ id: 'kw', group_id: 'system', key: 'keywords', name: 'Keywords', description: '', is_open: true, is_keywords: true, version: 1 }] };
      }
      if (path === '/v1/term-sets/kw/terms') data = { value: terms };
      return new Response(JSON.stringify(data));
    }),
  );
  return calls;
}

async function openKeywords() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TermStore />
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByRole('button', { name: /^Keywords/ }));
  await screen.findByText('Junk');
}

it('deletes a term nothing holds, with its version', async () => {
  const calls = serve();
  await openKeywords();
  fireEvent.click(screen.getByRole('button', { name: 'Delete Junk' }));
  const dialog = await screen.findByRole('alertdialog');
  expect(within(dialog).getByText(/its name becomes free/)).toBeTruthy();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
  expect(calls).toContainEqual({ method: 'DELETE', path: '/v1/terms/junk', ifMatch: '"3"' });
  await waitFor(() => expect(screen.queryByText('Junk')).toBeNull());
  expect(screen.getByText('Budget')).toBeTruthy();
});

it('says what holds a term the server refuses to delete', async () => {
  const calls = serve();
  await openKeywords();
  fireEvent.click(screen.getByRole('button', { name: 'Delete Budget' }));
  const dialog = await screen.findByRole('alertdialog');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
  expect(await within(dialog).findByText(/still in use/)).toBeTruthy();
  expect(within(dialog).getByText('2 items')).toBeTruthy();
  expect(within(dialog).getByText('1 item in a recycle bin')).toBeTruthy();
  expect(within(dialog).queryByText(/workflow/)).toBeNull();
  expect(calls).toContainEqual({ method: 'DELETE', path: '/v1/terms/budget', ifMatch: '"2"' });
  expect(screen.getByText('Budget')).toBeTruthy();
});
