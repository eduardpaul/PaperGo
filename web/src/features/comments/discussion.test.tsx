// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { Comment, CommentPage, CommentThread } from '@/api/types';
import { Discussion, choose, fromTokens, labelled, toTokens } from '@/features/comments/discussion';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function comment(id: string, extra: Partial<Comment> = {}): Comment {
  return {
    id,
    item_id: 'item',
    thread_id: id,
    author: { subject: 'alice', name: 'Alice' },
    source: 'user',
    body: `Comment ${id}`,
    mentions: [],
    created_at: '2026-10-13T10:00:00Z',
    updated_at: '2026-10-13T10:00:00Z',
    version: 1,
    can_edit: true,
    can_delete: true,
    can_resolve: true,
    ...extra,
  };
}

function page(threads: CommentThread[], extra: Partial<CommentPage> = {}): CommentPage {
  return { value: threads, mode: 'collaborators', mode_from: 'default', can_read: true, can_post: true, ...extra };
}

/** Serves an item's discussion and records the writes. */
function stub(discussion: CommentPage) {
  const writes: { method: string; path: string; body: unknown; match: string | null }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: URL, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      if (init?.method && init.method !== 'GET') {
        writes.push({
          method: init.method,
          path: url.pathname,
          body: init.body ? JSON.parse(String(init.body)) : undefined,
          match: headers.get('If-Match'),
        });
        return new Response(stringifyJSON(comment('new')), {
          status: init.method === 'POST' && url.pathname.endsWith('/comments') ? 201 : 200,
        });
      }
      if (url.pathname.endsWith('/mentionable'))
        return new Response(
          stringifyJSON({
            value: [
              { subject: 'bob', name: 'Bob', can_see: true },
              { subject: 'dave', name: 'Dave', can_see: false },
            ],
          }),
        );
      return new Response(stringifyJSON(discussion));
    }),
  );
  return writes;
}

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <Discussion itemId="item" />
    </QueryClientProvider>,
  );
}

it('keeps people who share a name apart while editing and saving', () => {
  const people = [
    { subject: 'bob', name: 'Bob' },
    { subject: 'bob2', name: 'Bob' },
    { subject: 'bobby', name: 'Bobby' },
  ];
  const body = 'Hi <@user:bob>, <@user:bob2> and <@user:bobby>';
  const text = fromTokens(body, people);
  expect(text).toBe('Hi @Bob (bob), @Bob (bob2) and @Bobby');
  expect(toTokens(text, labelled(people))).toBe(body);
  // Picking a second Bob labels them apart from the first.
  const chosen = choose(choose([], people[0]), people[1]);
  expect(chosen.map((p) => p.label)).toEqual(['Bob', 'Bob (bob2)']);
  expect(toTokens('@Bob and @Bob (bob2), not @Bobby', chosen)).toBe('<@user:bob> and <@user:bob2>, not @Bobby');
});

it('shows threads with their replies and mentions as names', async () => {
  stub(
    page([
      {
        ...comment('root', { body: 'Signed, <@user:bob>?', mentions: [{ subject: 'bob', name: 'Bob' }] }),
        replies: [
          comment('reply', {
            thread_id: 'root',
            author: { subject: 'bob', name: 'Bob' },
            body: 'Yes',
            can_edit: false,
            can_delete: false,
          }),
        ],
      },
    ]),
  );
  show();
  const threads = await screen.findByRole('list', { name: 'Threads' });
  expect(within(threads).getByText('@Bob')).toBeTruthy();
  expect(within(threads).getByRole('article', { name: 'Comment by Bob' }).textContent).toContain('Yes');
  expect(within(threads).getByRole('button', { name: 'Resolve' })).toBeTruthy();
});

it('mentions a person picked after @ and warns of people who cannot see the item', async () => {
  const writes = stub(page([]));
  show();
  const box = await screen.findByRole('textbox', { name: /Start a thread/ });
  fireEvent.change(box, { target: { value: 'Please check @b', selectionStart: 15 } });
  const people = await screen.findByRole('list', { name: 'People to mention' });
  expect(within(people).getByText("can't see this item")).toBeTruthy();
  fireEvent.click(within(people).getByRole('button', { name: /^Bob/ }));
  expect((box as HTMLTextAreaElement).value).toBe('Please check @Bob ');
  fireEvent.click(screen.getByRole('button', { name: 'Comment' }));
  await waitFor(() =>
    expect(writes).toEqual([
      { method: 'POST', path: '/v1/items/item/comments', body: { body: 'Please check <@user:bob>' }, match: null },
    ]),
  );
});

it('resolves a thread at its version', async () => {
  const writes = stub(page([{ ...comment('root', { version: 4 }), replies: [] }]));
  show();
  fireEvent.click(await screen.findByRole('button', { name: 'Resolve' }));
  await waitFor(() =>
    expect(writes).toEqual([{ method: 'POST', path: '/v1/comments/root/resolve', body: undefined, match: '"4"' }]),
  );
});

it('says when comments are off and when the discussion is for collaborators', async () => {
  stub(page([], { mode: 'off', mode_from: 'organization', can_read: false, can_post: false }));
  const first = show();
  expect(await screen.findByText('Comments are off here')).toBeTruthy();
  expect(screen.getByText('Turned off by the organization.')).toBeTruthy();
  first.unmount();
  stub(page([], { can_read: false, can_post: false }));
  show();
  expect(await screen.findByText('The discussion is for collaborators')).toBeTruthy();
  expect(screen.queryByRole('textbox')).toBeNull();
});

/** Serves an item's discussion over two pages. */
function paged(first: CommentThread[], second: CommentThread[]) {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: URL) => {
      asked.push(url.search);
      const next = url.searchParams.has('$skiptoken');
      return new Response(
        stringifyJSON(
          page(
            next ? second : first,
            next ? {} : { '@odata.nextLink': '/v1/items/item/comments?status=all&$skiptoken=next' },
          ),
        ),
      );
    }),
  );
  return asked;
}

function showFocused(focus?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <Discussion itemId="item" focus={focus} />
    </QueryClientProvider>,
  );
}

it('loads more threads through the next link', async () => {
  paged([{ ...comment('first'), replies: [] }], [{ ...comment('second'), replies: [] }]);
  showFocused();
  fireEvent.click(await screen.findByRole('button', { name: 'Load more threads' }));
  const threads = screen.getByRole('list', { name: 'Threads' });
  await waitFor(() => expect(within(threads).getAllByRole('listitem')).toHaveLength(2));
  expect(screen.queryByRole('button', { name: 'Load more threads' })).toBeNull();
});

it('finds the comment a notification points at, in a resolved thread on a later page', async () => {
  const asked = paged(
    [{ ...comment('open'), replies: [] }],
    [
      {
        ...comment('done', { resolved_at: '2026-10-13T11:00:00Z' }),
        replies: [comment('target', { thread_id: 'done', body: 'Mentioned here' })],
      },
    ],
  );
  showFocused('target');
  const threads = await screen.findByRole('list', { name: 'Threads' });
  await waitFor(() => expect(within(threads).getAllByRole('listitem')).toHaveLength(2));
  const focused = within(threads)
    .getAllByRole('listitem')
    .find((li) => li.getAttribute('aria-current') === 'true');
  expect(focused?.textContent).toContain('Mentioned here');
  expect(screen.getByRole('tab', { name: 'All' }).getAttribute('aria-selected')).toBe('true');
  expect(asked[0]).toContain('status=all');
});

it('never reads a mention it just wrote as someone else’s label', () => {
  const chosen = [
    { subject: 'bob2', name: 'Bob', label: 'Bob (bob2)' },
    { subject: 'u1', name: 'user', label: 'user' },
  ];
  expect(toTokens('@Bob (bob2) and @user', chosen)).toBe('<@user:bob2> and <@user:u1>');
});

it('stops following pages to a notified comment when a page fails', async () => {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: URL) => {
      asked.push(url.search);
      if (url.searchParams.has('$skiptoken')) {
        // A network's latency lets the page show that it is loading.
        await new Promise((resolve) => setTimeout(resolve, 20));
        return new Response(stringifyJSON({ detail: 'Denied' }), { status: 403 });
      }
      return new Response(
        stringifyJSON(
          page([{ ...comment('open'), replies: [] }], {
            '@odata.nextLink': '/v1/items/item/comments?status=all&$skiptoken=next',
          }),
        ),
      );
    }),
  );
  showFocused('elsewhere');
  expect((await screen.findByRole('alert')).textContent).toBe('Denied');
  await new Promise((resolve) => setTimeout(resolve, 300));
  expect(asked.filter((q) => q.includes('skiptoken'))).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Load more threads' }));
  await waitFor(() => expect(asked.filter((q) => q.includes('skiptoken'))).toHaveLength(2));
});
