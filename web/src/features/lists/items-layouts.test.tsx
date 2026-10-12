// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { createFormatter } from '@/lib/format';
import { ItemsBoard, calendarDay } from './items-layouts';
import type { FieldDefinition, Resource } from '@/api/types';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it('moves a board card with its version and clears only the grouping field', async () => {
  const fetch = vi.fn(async (_url: URL | string, _init?: RequestInit) => new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient();
  const item: Resource = {
    id: 'item',
    workspace_id: 'workspace',
    kind: 'item',
    name: 'Review',
    version: 7,
    values: { status: 'Open', title: 'Keep' },
  };
  const group: FieldDefinition = {
    id: 'status',
    key: 'status',
    label: 'Status',
    type: 'choice',
    choices: ['Open', 'Done'],
  };
  render(
    <QueryClientProvider client={client}>
      <ItemsBoard items={[item]} fields={[]} groupBy={group} onOpen={vi.fn()} />
    </QueryClientProvider>,
  );
  fireEvent.drop(screen.getByRole('region', { name: 'No value' }), { dataTransfer: { getData: () => 'item' } });
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  expect(fetch.mock.calls[0]?.[1]).toMatchObject({
    method: 'PATCH',
    headers: { 'If-Match': '"7"' },
    body: '{"values":{"status":null}}',
  });
  client.clear();
});

it('keeps date-only fields on their day and groups moments in the preferred time zone', () => {
  const item: Resource = {
    id: 'item',
    workspace_id: 'w',
    kind: 'item',
    name: 'x',
    version: 1,
    values: { due: '2026-10-12', meeting: '2026-10-12T00:30:00Z' },
  };
  const format = createFormatter({ timeZone: 'America/New_York' });
  expect(calendarDay(item, { id: 'due', key: 'due', label: 'Due', type: 'date' }, format.dayKey)).toBe('2026-10-12');
  expect(calendarDay(item, { id: 'meeting', key: 'meeting', label: 'Meeting', type: 'datetime' }, format.dayKey)).toBe(
    '2026-10-11',
  );
});
