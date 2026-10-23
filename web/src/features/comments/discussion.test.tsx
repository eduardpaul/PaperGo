// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { Comment, CommentPage, CommentThread } from '@/api/types';
import { Discussion, fromTokens, toTokens } from '@/features/comments/discussion';

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

it('writes mentions as names to edit and back as mentions to save', () => {
  const people = [
    { subject: 'bob', name: 'Bob' },
    { subject: 'bobby', name: 'Bob Smith' },
  ];
  expect(fromTokens('Hi <@user:bob> and <@user:bobby>', people)).toBe('Hi @Bob and @Bob Smith');
  expect(toTokens('Hi @Bob and @Bob Smith', people)).toBe('Hi <@user:bob> and <@user:bobby>');
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
