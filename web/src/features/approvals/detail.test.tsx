// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Approval } from '@/api/types';
import { ApprovalDetail } from './detail';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const approval: Approval = {
  id: 'request',
  workspace_id: 'workspace',
  workflow_id: 'workflow',
  run_id: 'run',
  node: 'review',
  title: 'Review smaller file for Receipt',
  assignees: ['alice'],
  input_schema: { type: 'object', properties: { note: { type: 'string', title: 'Note' } } },
  review: { type: 'file.compare', staged_file_id: 'staged' },
  status: 'open',
  created_at: '2026-10-13T10:00:00Z',
  created_by: 'alice',
};

it('shows a stale review and sends the decision with its form', async () => {
  const fetch = vi.fn(async (url: URL | string, _options?: RequestInit) => {
    const path = String(url);
    if (path.includes('/content')) return new Response('image', { headers: { 'Content-Type': 'image/webp' } });
    if (path.endsWith('/decision')) return new Response(stringifyJSON({ ...approval, status: 'approved' }));
    if (path.endsWith('/v1/staged-files/staged'))
      return new Response(
        stringifyJSON({
          id: 'staged',
          item_id: 'item',
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
    return new Response('{}', { status: 404 });
  });
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:preview', revokeObjectURL: () => {} }));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ApprovalDetail approval={approval} />
    </QueryClientProvider>,
  );
  expect(await screen.findByText(/can no longer be approved/)).toBeTruthy();
  expect(await screen.findByText('75 %')).toBeTruthy();
  await waitFor(() => expect(screen.getAllByRole('img')).toHaveLength(2));
  // The original is requested at the file version the candidate was made from.
  expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/v1/items/item/content?blob_id=blob'))).toBe(true);
  fireEvent.change(screen.getByLabelText('Note'), { target: { value: 'looks fine' } });
  fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/decision'))).toBe(true));
  const call = fetch.mock.calls.find(([url]) => String(url).endsWith('/decision'));
  expect(parseJSON(String(call?.[1]?.body))).toEqual({ decision: 'approve', input: { note: 'looks fine' } });
});
