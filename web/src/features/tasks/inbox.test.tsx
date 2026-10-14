// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import type { ComponentType } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { Task } from '@/api/types';
import { Route } from '@/routes/_app/tasks';

const navigation = vi.hoisted(() => ({ search: {} as { view?: string; id?: string }, navigate: vi.fn() }));
vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options, useSearch: () => navigation.search, fullPath: '/tasks' }),
  useNavigate: () => navigation.navigate,
}));

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  navigation.search = {};
  navigation.navigate.mockReset();
  vi.unstubAllGlobals();
});

function task(id: string): Task {
  return {
    id, title: `Task ${id}`, workspace_id: 'workspace', kind: 'task', status: 'open',
    assignees: ['alice'], completion: 'any', outcomes: [{ key: 'done', label: 'Done' }],
    priority: 'normal', source: 'user', created_by: 'alice',
    created_at: '2026-10-13T10:00:00Z', updated_at: '2026-10-13T10:00:00Z', version: 1, can_respond: false,
  };
}

function response(body: unknown, status = 200) {
  return new Response(stringifyJSON(body), { status });
}

function stub(list: (url: URL) => Response | Promise<Response>) {
  const fetch = vi.fn(async (url: URL) => {
    if (url.pathname.startsWith('/v1/tasks/')) return response(task(url.pathname.split('/').at(-1)!));
    return list(url);
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  const TasksPage = Route.options.component as ComponentType;
  const element = () => <QueryClientProvider client={client}><TasksPage /></QueryClientProvider>;
  const mounted = render(element());
  return {
    client,
    navigate: () => {
      navigation.search = navigation.navigate.mock.lastCall![0].search;
      mounted.rerender(element());
    },
  };
}

it('loads beyond 100 tasks through each proxy-safe next link and keeps the selected task open', async () => {
  let finishNext!: (result: Response) => void;
  const fetch = stub((url) => {
    if (url.searchParams.get('$skiptoken') === 'second')
      return new Promise<Response>((resolve) => { finishNext = resolve; });
    if (url.searchParams.get('$skiptoken') === 'third') return response({ value: [task('102')] });
    return response({
      value: Array.from({ length: 100 }, (_, index) => task(String(index + 1))),
      '@odata.nextLink': 'http://api:8080/v1/me/tasks?waiting=true&$top=100&$skiptoken=second',
    });
  });
  const page = show();
  const rows = within(screen.getByRole('list', { name: 'Tasks' }));
  await screen.findByRole('article', { name: 'Task 1' });
  expect(rows.getAllByRole('listitem')).toHaveLength(100);
  expect(fetch.mock.calls.filter(([url]) => url.pathname === '/v1/me/tasks')).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
  expect((await screen.findByRole('button', { name: 'Loading…' }) as HTMLButtonElement).disabled).toBe(true);
  const next = fetch.mock.calls.find(([url]) => url.searchParams.has('$skiptoken'))![0];
  expect(next.origin).toBe(window.location.origin);
  expect(next.searchParams.get('waiting')).toBe('true');
  expect(next.searchParams.get('$top')).toBe('100');
  await act(async () => finishNext(response({
    value: [task('101')],
    '@odata.nextLink': 'http://api:8080/v1/me/tasks?waiting=true&$top=100&$skiptoken=third',
  })));
  await rows.findByRole('button', { name: /^Task 101\b/ });
  expect(screen.getByRole('article', { name: 'Task 1' })).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
  await rows.findByRole('button', { name: /^Task 102\b/ });
  expect(rows.getAllByRole('listitem')).toHaveLength(102);
  expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  fireEvent.click(rows.getByRole('button', { name: /^Task 101\b/ }));
  page.navigate();
  expect(await screen.findByRole('article', { name: 'Task 101' })).toBeTruthy();
});

it('keeps a pending continuation separate from the newly selected view', async () => {
  let finishNext!: (result: Response) => void;
  const fetch = stub((url) => {
    if (url.searchParams.has('$skiptoken'))
      return new Promise<Response>((resolve) => { finishNext = resolve; });
    if (url.searchParams.get('status') === 'completed') return response({ value: [task('completed')] });
    return response({ value: [task('waiting')], '@odata.nextLink': '/v1/me/tasks?waiting=true&$skiptoken=next' });
  });
  const page = show();
  await screen.findByRole('button', { name: 'Load more' });
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
  await screen.findByRole('button', { name: 'Loading…' });
  fireEvent.click(screen.getByRole('tab', { name: 'Completed' }));
  page.navigate();
  const rows = within(screen.getByRole('list', { name: 'Tasks' }));
  await rows.findByRole('button', { name: /^Task completed\b/ });
  await act(async () => finishNext(response({ value: [task('later')] })));
  expect(rows.getAllByRole('listitem')).toHaveLength(1);
  expect(rows.queryByRole('button', { name: /^Task later\b/ })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  const completed = fetch.mock.calls.find(([url]) => url.searchParams.get('status') === 'completed')![0];
  expect(completed.searchParams.has('waiting')).toBe(false);
  expect(completed.searchParams.has('$skiptoken')).toBe(false);
  fireEvent.click(screen.getByRole('tab', { name: 'Waiting for me' }));
  page.navigate();
  await rows.findByRole('button', { name: /^Task later\b/ });
  expect(rows.getAllByRole('listitem')).toHaveLength(2);
});

it('preserves loaded tasks after a continuation error and retries the same page', async () => {
  let attempts = 0;
  stub((url) => {
    if (url.searchParams.has('$skiptoken')) {
      attempts++;
      return attempts === 1
        ? response({ detail: 'Could not load the next tasks' }, 503)
        : response({ value: [task('second')] });
    }
    return response({ value: [task('first')], '@odata.nextLink': '/v1/me/tasks?waiting=true&$skiptoken=next' });
  });
  show();
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  expect((await screen.findByRole('alert')).textContent).toBe('Could not load the next tasks');
  const rows = within(screen.getByRole('list', { name: 'Tasks' }));
  expect(rows.getAllByRole('listitem')).toHaveLength(1);
  expect(screen.queryByText('Nothing is waiting for you')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
  await rows.findByRole('button', { name: /^Task second\b/ });
  expect(rows.getAllByRole('listitem')).toHaveLength(2);
  expect(attempts).toBe(2);
  expect(screen.queryByRole('alert')).toBeNull();
});

it('refreshes loaded pages after a task changes without keeping stale rows', async () => {
  let changed = false;
  stub((url) => url.searchParams.has('$skiptoken')
    ? response({ value: [task(changed ? 'new-second' : 'second')] })
    : response({ value: [task(changed ? 'new-first' : 'first')], '@odata.nextLink': '/v1/me/tasks?waiting=true&$skiptoken=next' }));
  const page = show();
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
  const rows = within(screen.getByRole('list', { name: 'Tasks' }));
  await rows.findByRole('button', { name: /^Task second\b/ });
  changed = true;
  await act(async () => { await page.client.invalidateQueries({ queryKey: ['tasks'] }); });
  await rows.findByRole('button', { name: /^Task new-second\b/ });
  expect(rows.getAllByRole('listitem')).toHaveLength(2);
  expect(rows.queryByRole('button', { name: /^Task first\b/ })).toBeNull();
  expect(rows.queryByRole('button', { name: /^Task second\b/ })).toBeNull();
});
