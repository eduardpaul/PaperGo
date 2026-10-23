// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ComponentType } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { Route, summary } from './assistants';

vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options }),
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const workspace = { id: 'w1', kind: 'workspace', name: 'Operations' };
const lists = [
  { id: 'l1', kind: 'list', name: 'Invoices' },
  { id: 'l2', kind: 'library', name: 'Contracts' },
];

function serve(granted: string[], existing: unknown[] = []) {
  const posts: unknown[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL, init: RequestInit = {}) => {
    const path = input.pathname;
    if (init.method === 'POST') {
      posts.push(JSON.parse(String(init.body)));
      return new Response(JSON.stringify({ token: { id: 'new' }, secret: 'pgo_assistant_secret' }));
    }
    const data = path.endsWith('/me/scopes') ? granted
      : path.endsWith('/me/tokens') ? existing
      : path === '/v1/workspaces' ? { value: [workspace], '@odata.nextLink': null }
      : path === '/v1/resources/w1/children' ? { value: lists, '@odata.nextLink': null }
      : [];
    return new Response(JSON.stringify(data));
  }));
  return posts;
}

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const Page = Route.options.component as ComponentType;
  render(<QueryClientProvider client={client}><Page /></QueryClientProvider>);
  return client;
}

const reading = ['workspace.read', 'list.read', 'contentType.read', 'document.read', 'taxonomy.read', 'user.read'];

it('connects a read-only assistant to everything in one step', async () => {
  const posts = serve([...reading, 'list.write', 'workspace.manage']);
  const client = show();
  const create = screen.getByRole('button', { name: 'Create connection' }) as HTMLButtonElement;
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.click(create);
  expect(await screen.findByDisplayValue('pgo_assistant_secret')).toBeTruthy();
  expect(posts).toEqual([{ name: 'AI assistant', scopes: reading, expires_in_days: 90 }]);
  expect(screen.getByDisplayValue(`${window.location.origin}/v1/mcp`)).toBeTruthy();
  expect((screen.getByLabelText('Claude Code command') as HTMLInputElement).value).toContain('Authorization: Bearer pgo_assistant_secret');
  client.clear();
});

it('limits a read-and-write connection to chosen places under advanced options', async () => {
  const posts = serve([...reading, 'list.write']);
  const client = show();
  fireEvent.click(await screen.findByLabelText(/Read and write/));
  fireEvent.click(screen.getByRole('button', { name: /Advanced options/ }));
  fireEvent.click(screen.getByLabelText(/Only selected/));
  const create = screen.getByRole('button', { name: 'Create connection' }) as HTMLButtonElement;
  // Limited, but nothing chosen yet.
  await waitFor(() => expect(create.disabled).toBe(true));
  fireEvent.click(await screen.findByRole('button', { name: "Show Operations's lists and libraries" }));
  fireEvent.click(await screen.findByLabelText(/Invoices/));
  fireEvent.click(screen.getByLabelText(/Contracts/));
  fireEvent.change(screen.getByLabelText('Expires in days'), { target: { value: '30' } });
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.click(create);
  await screen.findByDisplayValue('pgo_assistant_secret');
  expect(posts).toEqual([{ name: 'AI assistant', scopes: [...reading, 'list.write'], resources: ['l1', 'l2'], expires_in_days: 30 }]);
  client.clear();
});

it('takes a whole workspace instead of its lists', async () => {
  const posts = serve(reading);
  const client = show();
  fireEvent.click(await screen.findByRole('button', { name: /Advanced options/ }));
  fireEvent.click(screen.getByLabelText(/Only selected/));
  fireEvent.click(await screen.findByRole('button', { name: "Show Operations's lists and libraries" }));
  fireEvent.click(await screen.findByLabelText(/Invoices/));
  fireEvent.click(screen.getByLabelText('Operations'));
  expect((screen.getByLabelText(/Invoices/) as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Create connection' }));
  await screen.findByDisplayValue('pgo_assistant_secret');
  expect((posts[0] as { resources: string[] }).resources).toEqual(['w1']);
  client.clear();
});

it('offers read and write only to people who can write', async () => {
  serve(reading);
  const client = show();
  await waitFor(() => expect((screen.getByLabelText(/Read and write/) as HTMLInputElement).disabled).toBe(true));
  client.clear();
});

it('summarises what a connection reaches', () => {
  const token = { id: 't', name: 'n', prefix: 'p', subject: 's', integration: false, created_by: 's', created_at: '', expires_at: null };
  expect(summary({ ...token, scopes: reading })).toBe('Read only · Everything');
  expect(summary({ ...token, scopes: [...reading, 'list.write'], resources: ['a', 'b'] })).toBe('Read and write · 2 places');
  expect(summary({ ...token, scopes: reading, resources: ['a'] })).toBe('Read only · 1 place');
});

it('asks before revoking a connection', async () => {
  const deleted: string[] = [];
  const existing = [{ id: 'tok1', name: 'Claude', prefix: 'pgo_x', subject: 'a', integration: false, created_by: 'a', created_at: '', expires_at: null,
    scopes: reading, resources: ['l1'] }];
  vi.stubGlobal('fetch', vi.fn(async (input: URL, init: RequestInit = {}) => {
    if (init.method === 'DELETE') {
      deleted.push(input.pathname);
      return new Response(null, { status: 204 });
    }
    return new Response(JSON.stringify(input.pathname.endsWith('/me/tokens') ? existing : reading));
  }));
  const client = show();
  expect(await screen.findByText(/Read only · 1 place/)).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
  expect(deleted).toEqual([]);
  const dialog = await screen.findByRole('alertdialog');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));
  await waitFor(() => expect(deleted).toEqual(['/v1/identity/me/tokens/tok1']));
  client.clear();
});
