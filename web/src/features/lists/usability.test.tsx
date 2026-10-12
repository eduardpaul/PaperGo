// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { FieldDefinition, Resource } from '@/api/types';
import { keyFromLabel } from '@/features/admin/field-dialog';
import { ItemSheet } from './item-sheet';
import { ItemsTable } from './items-table';

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
});

function renderWithClient(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

const row = (id: string, name: string, kind: Resource['kind'] = 'item'): Resource => ({
  id,
  workspace_id: 'workspace',
  kind,
  name,
  version: 1,
  values: {},
});

describe('items table', () => {
  it('opens a row on click, selects with its checkbox and selects all from the header', () => {
    const onOpen = vi.fn();
    const onToggle = vi.fn();
    const onToggleAll = vi.fn();
    renderWithClient(
      <ItemsTable
        rows={[row('a', 'Alpha'), row('b', 'Folder', 'folder')]}
        columns={['$name', '$modified_by']}
        fields={[]}
        publishing={false}
        names={new Map()}
        selected={new Map()}
        onToggle={onToggle}
        onToggleAll={onToggleAll}
        onOpen={onOpen}
      />,
    );
    fireEvent.click(screen.getByRole('row', { name: /Alpha/ }));
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'a' }));
    fireEvent.click(screen.getByLabelText('Select Folder'));
    expect(onToggle).toHaveBeenCalledWith(expect.objectContaining({ id: 'b' }), true);
    expect(onOpen).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByLabelText('Select all'));
    expect(onToggleAll).toHaveBeenCalledWith(true);
  });
});

describe('item panel', () => {
  const fields: FieldDefinition[] = [{ id: 'note', key: 'note', label: 'Note', type: 'text' }];
  const collection: Resource = { id: 'list', workspace_id: 'workspace', kind: 'list', name: 'Records', version: 1 };

  function mockItem() {
    const item: Resource = { ...row('item', 'Original'), container_id: 'list', values: { note: 'a' } };
    const calls: { method: string; path: string }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: URL | string, init: RequestInit = {}) => {
        const url = new URL(String(input), window.location.origin);
        const method = init.method ?? 'GET';
        calls.push({ method, path: url.pathname });
        const body = method !== 'GET' ? {} : url.pathname === '/v1/resources/item' ? item : { value: [] };
        return new Response(stringifyJSON(body), { headers: { 'Content-Type': 'application/json' } });
      }),
    );
    return calls;
  }

  it('enables Save only after a change and asks before discarding it', async () => {
    mockItem();
    const onClose = vi.fn();
    renderWithClient(
      <ItemSheet itemId="item" collection={collection} fields={fields} contentTypes={[]} tab="details" onTab={vi.fn()} onClose={onClose} onMoved={vi.fn()} />,
    );
    const save = await screen.findByRole('button', { name: 'Save' });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Note'), { target: { value: 'b' } });
    expect((save as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(await screen.findByText('Discard unsaved changes?')).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('deletes only after confirmation', async () => {
    const calls = mockItem();
    renderWithClient(
      <ItemSheet itemId="item" collection={collection} fields={fields} contentTypes={[]} tab="details" onTab={vi.fn()} onClose={vi.fn()} onMoved={vi.fn()} />,
    );
    fireEvent.pointerDown(await screen.findByRole('button', { name: 'Item actions' }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }));
    expect(await screen.findByText('Delete “Original”?')).toBeTruthy();
    expect(calls.some((call) => call.method === 'DELETE')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(calls).toContainEqual({ method: 'DELETE', path: '/v1/resources/item' }));
  });
});

it.each([
  ['Cost center', 'cost_center'],
  ['  Due date (UTC) ', 'due_date_utc'],
  ['Café owner', 'cafe_owner'],
  ['2nd reviewer', 'field_2nd_reviewer'],
  ['!!!', ''],
])('derives the key of %j as %j', (label, key) => {
  expect(keyFromLabel(label)).toBe(key);
});
