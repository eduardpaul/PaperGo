// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ComponentType, ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { Notification } from '@/api/types';
import { NotificationBell } from '@/features/notifications/notifications';
import { Route } from '@/routes/_app/notifications';

const navigation = vi.hoisted(() => ({ search: {} as { view?: string }, navigate: vi.fn() }));
vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({
    options,
    useSearch: () => navigation.search,
    fullPath: '/notifications',
  }),
  useNavigate: () => navigation.navigate,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  navigation.search = {};
  navigation.navigate.mockReset();
  vi.unstubAllGlobals();
});

function notification(id: string, extra: Partial<Notification> = {}): Notification {
  return {
    id,
    type: 'message',
    count: 1,
    title: `Note ${id}`,
    source: 'user',
    sender: 'carol',
    created_at: '2026-10-13T10:00:00Z',
    ...extra,
  };
}

function response(body: unknown, status = 200) {
  return new Response(body === undefined ? null : stringifyJSON(body), { status });
}

/** Serves an inbox and records what was marked read or deleted. */
function stub(rows: Notification[]) {
  const marked: unknown[] = [];
  const deleted: string[] = [];
  const fetch = vi.fn(async (url: URL, init?: RequestInit) => {
    if (url.pathname === '/v1/me/notifications/unread-count')
      return response({ count: rows.filter((row) => !row.read_at).length });
    if (url.pathname === '/v1/me/notifications/read') {
      marked.push(JSON.parse(String(init?.body)));
      return response({ marked: 1 });
    }
    if (url.pathname.startsWith('/v1/me/notifications/') && init?.method === 'DELETE') {
      deleted.push(url.pathname.split('/').at(-1)!);
      return response(undefined, 204);
    }
    if (url.pathname === '/v1/me/notifications')
      return response({ value: url.searchParams.get('unread') === 'true' ? rows.filter((row) => !row.read_at) : rows });
    if (url.pathname.startsWith('/v1/resources/'))
      return response({ id: 'item', kind: 'item', name: 'Lease', container_id: 'list', workspace_id: 'ws' });
    return response({ detail: 'unexpected' }, 404);
  });
  vi.stubGlobal('fetch', fetch);
  return { marked, deleted };
}

function show(element: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  return render(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
}

it('counts unread notifications on the bell and opens a task from the latest', async () => {
  const calls = stub([
    notification('task', { type: 'task.assigned', title: 'Check the lease', task_id: 'task-1', workspace_id: 'ws' }),
    notification('old', { read_at: '2026-10-13T11:00:00Z' }),
  ]);
  show(<NotificationBell />);
  const bell = await screen.findByRole('button', { name: 'Notifications, 1 unread' });
  fireEvent.click(bell);
  const latest = await screen.findByRole('list', { name: 'Latest notifications' });
  expect(await within(latest).findAllByRole('listitem')).toHaveLength(2);
  expect(within(latest).getByText(/Task for you · carol/)).toBeTruthy();
  fireEvent.click(within(latest).getByRole('button', { name: /Check the lease/ }));
  await waitFor(() =>
    expect(navigation.navigate).toHaveBeenCalledWith({ to: '/tasks', search: { view: 'all', id: 'task-1' } }),
  );
  await waitFor(() => expect(calls.marked).toEqual([{ ids: ['task'] }]));
});

it('marks everything read from the bell', async () => {
  const calls = stub([notification('a'), notification('b')]);
  show(<NotificationBell />);
  fireEvent.click(await screen.findByRole('button', { name: 'Notifications, 2 unread' }));
  fireEvent.click(await screen.findByRole('button', { name: 'Mark all read' }));
  await waitFor(() => expect(calls.marked).toEqual([{}]));
});

it('lists the inbox, keeps the unread view to unread ones, opens items and deletes', async () => {
  const calls = stub([
    notification('item', { title: 'Lease renewed', item_id: 'item', workspace_id: 'ws', body: 'Through 2031' }),
    notification('seen', { title: 'Seen before', read_at: '2026-10-13T11:00:00Z' }),
  ]);
  const NotificationsPage = Route.options.component as ComponentType;
  const page = show(<NotificationsPage />);
  const rows = within(await screen.findByRole('list', { name: 'Notifications' }));
  await rows.findByText('Seen before');
  expect(rows.getByText('Through 2031')).toBeTruthy();
  fireEvent.click(rows.getByRole('button', { name: /^Lease renewed/ }));
  await waitFor(() =>
    expect(navigation.navigate).toHaveBeenCalledWith({
      to: '/w/$workspaceId/l/$listId',
      params: { workspaceId: 'ws', listId: 'list' },
      search: { item: 'item' },
    }),
  );
  fireEvent.click(rows.getByRole('button', { name: 'Delete Seen before' }));
  await waitFor(() => expect(calls.deleted).toEqual(['seen']));

  navigation.search = { view: 'unread' };
  page.rerender(
    <QueryClientProvider client={clients[0]}>
      <NotificationsPage />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.queryByText('Seen before')).toBeNull());
  expect(screen.getByRole('tab', { name: 'Unread (1)' }).getAttribute('aria-selected')).toBe('true');
});
