// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Workflow } from '@/api/types';
import { LaunchWorkflow } from './workflow-tools';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it('preserves an ordered selection, explicit primary item and exact typed launch inputs', async () => {
  const fetch = vi.fn(
    async (url: URL | string, _options?: RequestInit) =>
      new Response(
        stringifyJSON(
          String(url).endsWith('/runs')
            ? { run_ids: ['run'] }
            : {
                value: [
                  { id: 'first', name: 'First', kind: 'item' },
                  { id: 'second', name: 'Second', kind: 'item' },
                ],
              },
        ),
      ),
  );
  vi.stubGlobal('fetch', fetch);
  const workflow: Workflow = {
    id: 'workflow',
    key: 'workflow',
    workspace_id: 'workspace',
    name: 'Process',
    description: '',
    enabled: true,
    version: 1,
    current_version: 1,
    definition: {
      triggers: [{ type: 'manual', collection_id: 'collection', selection: 'selection' }],
      input_schema: {
        type: 'object',
        required: ['amount'],
        properties: { amount: { type: 'integer', title: 'Amount' } },
      },
      flow: { start: 'end', nodes: { end: { activity: 'end' } } },
    },
  };
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <LaunchWorkflow workflow={workflow} itemIds={['first', 'second']} onClose={vi.fn()} />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText('Amount *'), { target: { value: '9007199254740993' } });
  fireEvent.click(screen.getAllByRole('button', { name: 'Up' })[1]);
  fireEvent.click(screen.getByRole('button', { name: 'Run workflow' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/runs'))).toBe(true));
  const start = fetch.mock.calls.find(([url]) => String(url).endsWith('/runs'));
  const body = parseJSON(String(start?.[1]?.body)) as {
    item_ids: string[];
    primary_item_id: string;
    inputs: { amount: unknown };
  };
  expect(body.item_ids).toEqual(['second', 'first']);
  expect(body.primary_item_id).toBe('first');
  expect(String(body.inputs.amount)).toBe('9007199254740993');
  client.clear();
});

it('shows selection hints and image previews of the ordered items', async () => {
  const fetch = vi.fn(async (url: URL | string) =>
    String(url).includes('/content')
      ? new Response('image bytes', { headers: { 'Content-Type': 'image/png' } })
      : new Response(stringifyJSON({ value: [{ id: 'first', name: 'First', kind: 'item' }, { id: 'second', name: 'Second', kind: 'item' }] })),
  );
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:preview', revokeObjectURL: vi.fn() }));
  const workflow: Workflow = {
    id: 'workflow',
    key: 'photos',
    workspace_id: 'workspace',
    name: 'Photos to PDF',
    description: '',
    enabled: true,
    version: 1,
    current_version: 1,
    definition: {
      triggers: [{ type: 'manual', collection_id: 'collection', selection: 'selection' }],
      input_schema: {
        type: 'object',
        properties: {},
        'x-papergo-selection': { preview: 'image', item_label: 'page', order_label: 'Page order', primary_description: 'Gets the PDF.' },
      },
      flow: { start: 'end', nodes: { end: { activity: 'end' } } },
    },
  };
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <LaunchWorkflow workflow={workflow} itemIds={['first', 'second']} onClose={vi.fn()} />
    </QueryClientProvider>,
  );
  expect(screen.getByLabelText('Primary page')).toBeTruthy();
  expect(screen.getByText('Gets the PDF.')).toBeTruthy();
  const order = screen.getByRole('list', { name: 'Page order' });
  expect(order.textContent).toContain('page 1:');
  expect(order.textContent).toContain('page 2:');
  await waitFor(() => expect(order.querySelectorAll('img[src="blob:preview"]')).toHaveLength(2));
  expect(fetch.mock.calls.some(([url]) => String(url).endsWith('/v1/items/second/content'))).toBe(true);
  client.clear();
});
