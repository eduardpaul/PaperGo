// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Resource, Workflow } from '@/api/types';
import { applicableWorkflows, RunWorkflowAction } from './item-workflows';

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals(); });
const flow = (id: string, trigger: object, enabled = true): Workflow => ({
  id, key: id, name: id, workspace_id: 'workspace', description: '', version: 1, current_version: 1, enabled,
  definition: { triggers: [trigger], flow: { start: 'end', nodes: { end: { activity: 'end' } } } },
});
const entries = [
  flow('single', { type: 'manual', collection_id: 'collection' }),
  flow('any-single', { type: 'manual', selection: 'per_item' }),
  flow('multiple', { type: 'manual', collection_id: 'collection', selection: 'selection' }),
  flow('other', { type: 'manual', collection_id: 'other' }),
  flow('standalone', { type: 'manual' }),
  flow('automatic', { type: 'file.updated' }),
  flow('disabled', { type: 'manual', collection_id: 'collection' }, false),
];
it('filters manual workflows by collection, selection size and enabled state', () => {
  expect(applicableWorkflows(entries, 'collection', 1).map((entry) => entry.id)).toEqual(['single', 'any-single', 'multiple']);
  expect(applicableWorkflows(entries, 'collection', 2).map((entry) => entry.id)).toEqual(['multiple']);
  expect(applicableWorkflows(entries, 'collection', 0)).toEqual([]);
  expect(applicableWorkflows(entries, 'collection', 101)).toEqual([]);
});

it('launches the selected items with typed inputs and displays a denied launch inline', async () => {
  const base = entries.find((entry) => entry.id === 'multiple')!;
  const multiple = { ...base, definition: { ...base.definition as object,
    input_schema: { type: 'object', required: ['reason'], properties: { reason: { type: 'string', title: 'Reason' } } },
  } };
  let denied = true;
  const fetch = vi.fn(async (url: URL | string, _init?: RequestInit) => {
    if (String(url).endsWith('/runs')) return new Response(stringifyJSON(denied ? { title: 'Forbidden', detail: 'You cannot write this item' } : { run_ids: ['run'] }), { status: denied ? 403 : 202 });
    return new Response(stringifyJSON({ value: [multiple] }));
  });
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const items = ['a', 'b'].map((id) => ({ id, name: `Item ${id}`, kind: 'item', version: 1, workspace_id: 'workspace' }) as Resource);
  render(<QueryClientProvider client={client}><RunWorkflowAction workspaceId="workspace" collectionId="collection" items={items} /></QueryClientProvider>);
  fireEvent.pointerDown(screen.getByRole('button', { name: 'Run workflow' }), { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByRole('menuitem', { name: 'multiple' }));
  expect(await screen.findByText('2 selected: Item a, Item b')).toBeTruthy();
  expect(screen.queryByRole('combobox', { name: 'Items' })).toBeNull();
  fireEvent.change(screen.getByLabelText('Reason *'), { target: { value: 'Review invoices' } });
  fireEvent.click(screen.getByRole('button', { name: 'Run workflow' }));
  expect(await screen.findByRole('alert')).toBeTruthy();
  denied = false;
  fireEvent.click(screen.getByRole('button', { name: 'Run workflow' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  const start = fetch.mock.calls.find(([url]) => String(url).endsWith('/runs'));
  expect(parseJSON(String(start?.[1]?.body))).toEqual({ item_ids: ['a', 'b'], primary_item_id: 'a', inputs: { reason: 'Review invoices' } });
  // The fixed selection does not fetch an unrelated item picker.
  expect(fetch.mock.calls.every(([url]) => /workflows/.test(String(url)))).toBe(true);
});
