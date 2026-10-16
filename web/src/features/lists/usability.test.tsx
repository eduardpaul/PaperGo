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
  vi.restoreAllMocks();
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
  it('renders only the rows near the screen in long tables', () => {
    const rows = Array.from({ length: 1000 }, (_, index) => row(`r${index}`, `Row ${index}`));
    // jsdom has no layout: give rows a height so they can be measured.
    vi.spyOn(HTMLTableRowElement.prototype, 'offsetHeight', 'get').mockReturnValue(41);
    renderWithClient(
      <ItemsTable
        rows={rows}
        columns={['$name']}
        fields={[]}
        publishing={false}
        names={new Map()}
        selectable={false}
        selected={new Map()}
        sortable={new Set()}
        onSort={vi.fn()}
        onToggle={vi.fn()}
        onToggleAll={vi.fn()}
        onOpen={vi.fn()}
      />,
    );
    const rendered = screen.getAllByRole('row').filter((element) => element.getAttribute('aria-rowindex'));
    // jsdom has no scrolling, so only check that rows are left out and still numbered for assistive technology.
    expect(rendered.length).toBeGreaterThan(10);
    expect(rendered.length).toBeLessThan(rows.length);
    expect(Number(rendered[0].getAttribute('aria-rowindex'))).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('table').getAttribute('aria-rowcount')).toBe('1001');
  });

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
        selectable
        selected={new Map()}
        sortable={new Set(['$name'])}
        onSort={vi.fn()}
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

  function mockItem(access: Resource['access'] = ['read', 'read_draft', 'write']) {
    const item: Resource = { ...row('item', 'Original'), container_id: 'list', values: { note: 'a' }, access };
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

  it('explains disabled version history for a library', async () => {
    mockItem();
    renderWithClient(
      <ItemSheet itemId="item" collection={{ ...collection, kind: 'library', versioning: 'off' }} fields={[]} contentTypes={[]} tab="versions" onTab={vi.fn()} onClose={vi.fn()} onMoved={vi.fn()} />,
    );
    expect(await screen.findByText(/Version history is off for this library/)).toBeTruthy();
  });

  it('offers no edits to people who can only read', async () => {
    mockItem(['read']);
    renderWithClient(
      <ItemSheet itemId="item" collection={collection} fields={fields} contentTypes={[]} tab="details" onTab={vi.fn()} onClose={vi.fn()} onMoved={vi.fn()} />,
    );
    expect(await screen.findByText('You can read this item but not change it.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
    expect(screen.getByLabelText('Note').matches(':disabled')).toBe(true);
    expect(screen.queryByRole('tab', { name: 'Permissions' })).toBeNull();
    fireEvent.pointerDown(screen.getByRole('button', { name: 'Item actions' }), { button: 0, ctrlKey: false });
    expect(await screen.findByRole('menuitem', { name: 'Field definitions' })).toBeTruthy();
    expect(screen.queryByRole('menuitem', { name: 'Delete' })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: 'Move…' })).toBeNull();
  });
});

describe('table sorting', () => {
  function renderSorted(sort: { field: string; direction: 'asc' | 'desc' } | undefined, onSort = vi.fn()) {
    renderWithClient(
      <ItemsTable
        rows={[row('a', 'Alpha')]}
        columns={['$name', 'note']}
        fields={[{ id: 'note', key: 'note', label: 'Note', type: 'note' }]}
        publishing={false}
        names={new Map()}
        selectable={false}
        selected={new Map()}
        sort={sort}
        sortable={new Set(['$name'])}
        onSort={onSort}
        onToggle={vi.fn()}
        onToggleAll={vi.fn()}
        onOpen={vi.fn()}
      />,
    );
    return onSort;
  }

  it('cycles a sortable column through ascending, descending and none', () => {
    const onSort = renderSorted(undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Name' }));
    expect(onSort).toHaveBeenLastCalledWith({ field: '$name', direction: 'asc' });
    cleanup();
    const descending = renderSorted({ field: '$name', direction: 'asc' });
    expect(screen.getByRole('columnheader', { name: 'Name' }).getAttribute('aria-sort')).toBe('ascending');
    fireEvent.click(screen.getByRole('button', { name: 'Name' }));
    expect(descending).toHaveBeenLastCalledWith({ field: '$name', direction: 'desc' });
    cleanup();
    const none = renderSorted({ field: '$name', direction: 'desc' });
    fireEvent.click(screen.getByRole('button', { name: 'Name' }));
    expect(none).toHaveBeenLastCalledWith(undefined);
  });

  it('offers no sort on columns the server cannot sort by', () => {
    renderSorted(undefined);
    expect(screen.queryByRole('button', { name: 'Note' })).toBeNull();
    expect(screen.getByRole('columnheader', { name: 'Note' })).toBeTruthy();
  });
});

describe('table keyboard', () => {
  it('moves between rows with the arrow keys, opens with Enter and selects with x', () => {
    const onOpen = vi.fn();
    const onToggle = vi.fn();
    renderWithClient(
      <ItemsTable
        rows={[row('a', 'Alpha'), row('b', 'Beta'), row('c', 'Gamma')]}
        columns={['$name']}
        fields={[]}
        publishing={false}
        names={new Map()}
        selectable
        selected={new Map()}
        sortable={new Set()}
        onSort={vi.fn()}
        onToggle={onToggle}
        onToggleAll={vi.fn()}
        onOpen={onOpen}
      />,
    );
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows.map((element) => element.tabIndex)).toEqual([0, -1, -1]);
    rows[0].focus();
    fireEvent.keyDown(rows[0], { key: 'ArrowDown' });
    expect(document.activeElement).toBe(rows[1]);
    expect(rows.map((element) => element.tabIndex)).toEqual([-1, 0, -1]);
    fireEvent.keyDown(rows[1], { key: 'End' });
    expect(document.activeElement).toBe(rows[2]);
    fireEvent.keyDown(rows[2], { key: 'x' });
    expect(onToggle).toHaveBeenCalledWith(expect.objectContaining({ id: 'c' }), true);
    fireEvent.keyDown(rows[2], { key: 'Enter' });
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'c' }));
    fireEvent.keyDown(rows[2], { key: 'Home' });
    expect(document.activeElement).toBe(rows[0]);
  });
});

describe('table selection', () => {
  it('has no checkboxes without write access', () => {
    renderWithClient(
      <ItemsTable
        rows={[row('a', 'Alpha')]}
        columns={['$name']}
        fields={[]}
        publishing={false}
        names={new Map()}
        selectable={false}
        selected={new Map()}
        sortable={new Set(['$name'])}
        onSort={vi.fn()}
        onToggle={vi.fn()}
        onToggleAll={vi.fn()}
        onOpen={vi.fn()}
      />,
    );
    expect(screen.queryByLabelText('Select all')).toBeNull();
    expect(screen.queryByLabelText('Select Alpha')).toBeNull();
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
