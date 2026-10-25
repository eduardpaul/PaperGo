// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Task } from '@/api/types';
import { TaskDetail } from './detail';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const base: Task = {
  id: 'task',
  workspace_id: 'workspace',
  kind: 'task',
  title: 'Check the invoice data',
  assignees: ['alice'],
  completion: 'any',
  outcomes: [{ key: 'done', label: 'Done', style: 'primary' }],
  priority: 'normal',
  source: 'workflow',
  status: 'open',
  created_by: 'alice',
  created_at: '2026-10-13T10:00:00Z',
  updated_at: '2026-10-13T10:00:00Z',
  version: 1,
  can_respond: true,
};

function stub(handler: (path: string, init?: RequestInit) => Response | undefined) {
  const fetch = vi.fn(async (url: URL | string, init?: RequestInit) => handler(String(url), init) ?? new Response('{}', { status: 404 }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

function show(task: Task) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <TaskDetail task={task} />
    </QueryClientProvider>,
  );
}

it('answers a custom task with its prefilled form, one button per outcome and a required comment', async () => {
  const task: Task = {
    ...base,
    outcomes: [
      { key: 'correct', label: 'Correct', style: 'primary' },
      { key: 'fix', label: 'Needs a fix', comment: 'required', skip_form: true },
    ],
    input_schema: { type: 'object', properties: { total: { type: 'number', title: 'Total' } } },
    values: { total: 120 },
  };
  const fetch = stub((path) => (path.endsWith('/responses') ? new Response(stringifyJSON({ ...task, status: 'completed' })) : undefined));
  show(task);
  expect((screen.getByLabelText('Total') as HTMLInputElement).value).toBe('120');
  fireEvent.click(screen.getByRole('button', { name: 'Needs a fix' }));
  expect(await screen.findByText('Needs a fix needs a comment.')).toBeTruthy();
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'VAT missing' } });
  fireEvent.click(screen.getByRole('button', { name: 'Needs a fix' }));
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  // An outcome that skips the form sends no values.
  expect(parseJSON(String(fetch.mock.calls[0][1]?.body))).toEqual({ outcome: 'fix', comment: 'VAT missing' });
  fireEvent.change(screen.getByLabelText('Total'), { target: { value: '125' } });
  fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(parseJSON(String(fetch.mock.calls[1][1]?.body))).toEqual({ outcome: 'correct', comment: 'VAT missing', input: { total: 125 } });
});

it('shows a stale review of an approval and the answers given, read only', async () => {
  const task: Task = {
    ...base,
    kind: 'approval',
    title: 'Review smaller file for Receipt',
    completion: 'all',
    outcomes: [
      { key: 'approved', label: 'Approve', style: 'primary' },
      { key: 'rejected', label: 'Reject', style: 'danger', comment: 'required', allow_stale: true, decisive: true },
    ],
    input_schema: { type: 'object', properties: { note: { type: 'string', title: 'Note' } } },
    review: { type: 'file.compare', staged_file_id: 'staged' },
    can_respond: false,
    answered: true,
    responses: [{ subject: 'carol', assignee: 'group:legal', outcome: 'approved', comment: 'fine', input: { note: 'readable' }, created_at: '2026-10-13T11:00:00Z' }],
  };
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:preview', revokeObjectURL: () => {} }));
  const fetch = stub((path) => {
    if (path.includes('/content')) return new Response('image', { headers: { 'Content-Type': 'image/webp' } });
    if (path.endsWith('/v1/staged-files/staged'))
      return new Response(
        stringifyJSON({
          id: 'staged',
          item_id: 'item',
          artifact_id: 'artifact',
          run_id: 'run',
          filename: 'receipt.webp',
          content_type: 'image/webp',
          size: 1000,
          sha256: 'x',
          metrics: { source_bytes: 4000, bytes: 1000, savings_percent: 75 },
          sources: [{ item_id: 'item', blob_id: 'blob' }],
          state: 'pending',
          fresh: false,
          created_at: '2026-10-13T10:00:00Z',
        }),
      );
    return undefined;
  });
  show(task);
  expect(await screen.findByText(/can no longer be accepted/)).toBeTruthy();
  expect(await screen.findByText('75 %')).toBeTruthy();
  await waitFor(() => expect(screen.getAllByRole('img')).toHaveLength(2));
  // The original is requested at the file version the candidate was made from.
  expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/v1/items/item/content?blob_id=blob'))).toBe(true);
  // Answered: no buttons, the answers with their form values.
  expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull();
  expect(screen.getByText(/waits for the other assignees/)).toBeTruthy();
  const answers = screen.getByRole('region', { name: 'Answers' });
  expect(answers.textContent).toContain('carol for group:legal: Approve');
  expect(answers.textContent).toContain('readable');
});

it('lets an owner drop an assignee who can no longer answer', async () => {
  const task: Task = { ...base, assignees: ['bob', 'carol'], completion: 'all', can_respond: false, can_reassign: true };
  const fetch = stub((path, init) =>
    path.endsWith('/reassign') ? new Response(stringifyJSON({ ...task, ...(parseJSON(String(init?.body)) as object) })) : new Response('[]'),
  );
  show(task);
  fireEvent.click(screen.getByRole('button', { name: 'Reassign' }));
  fireEvent.click(screen.getByLabelText('carol'));
  fireEvent.click(screen.getByRole('button', { name: 'Save assignees' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/reassign'))).toBe(true));
  const [, init] = fetch.mock.calls.find(([url]) => String(url).endsWith('/reassign'))!;
  expect(parseJSON(String(init?.body))).toEqual({ assignees: ['bob'] });
});

it('sends no comment with an outcome that takes none', async () => {
  const task: Task = {
    ...base,
    outcomes: [
      { key: 'ok', label: 'OK', style: 'primary', comment: 'none' },
      { key: 'fix', label: 'Fix', comment: 'required' },
    ],
  };
  const fetch = stub((path) => (path.endsWith('/responses') ? new Response(stringifyJSON({ ...task, status: 'completed' })) : undefined));
  show(task);
  fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'maybe a fix' } });
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/responses'))).toBe(true));
  const [, init] = fetch.mock.calls.find(([url]) => String(url).endsWith('/responses'))!;
  expect(parseJSON(String(init?.body))).toEqual({ outcome: 'ok' });
});

it('shows a staged document with the viewer handler for its type', async () => {
  const task: Task = { ...base, kind: 'approval', title: 'Review photos as PDF', review: { type: 'file.preview', staged_file_id: 'staged' } };
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:staged', revokeObjectURL: () => {} }));
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} });
  const fetch = stub((path) => {
    if (path.endsWith('/v1/staged-files/staged/content')) return new Response('image', { headers: { 'Content-Type': 'image/png' } });
    if (path.endsWith('/v1/staged-files/staged'))
      return new Response(
        stringifyJSON({ id: 'staged', item_id: 'item', artifact_id: 'artifact', filename: 'photo.png', content_type: 'image/png', size: 10, sha256: 'x', metrics: {}, sources: [], state: 'pending', fresh: true, created_at: '2026-10-13T10:00:00Z' }),
      );
    return undefined;
  });
  show(task);
  // The image handler draws it, from the staged file only; it shows once the browser has loaded it.
  await screen.findByLabelText('Image photo.png');
  await waitFor(() => expect(document.querySelector('img[alt="photo.png"]')?.getAttribute('src')).toBe('blob:staged'));
  expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/v1/staged-files/staged/content'))).toBe(true);
  expect(fetch.mock.calls.every(([url]) => !String(url).includes('/v1/items/'))).toBe(true);
});
