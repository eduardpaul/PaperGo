// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { ListView } from '@/api/types';
import { ColumnPicker } from './column-picker';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const available = [
  { key: '$name', label: 'Name' },
  { key: 'amount', label: 'Amount' },
  { key: 'status', label: 'Status' },
];

function open(props: Partial<Parameters<typeof ColumnPicker>[0]> = {}) {
  const client = new QueryClient();
  const onChange = vi.fn();
  const onSaved = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <ColumnPicker
        collectionId="list"
        columns={['$name', 'amount']}
        available={available}
        changed={false}
        canManage
        onChange={onChange}
        onSaved={onSaved}
        {...props}
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: /Columns/ }));
  return { onChange, onSaved, client };
}

it('shows, hides and reorders columns and keeps the name', () => {
  const { onChange } = open();
  expect((screen.getByRole('checkbox', { name: 'Name' }) as HTMLInputElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Status' }));
  expect(onChange).toHaveBeenLastCalledWith(['$name', 'amount', 'status']);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Amount' }));
  expect(onChange).toHaveBeenLastCalledWith(['$name']);
  fireEvent.click(screen.getByRole('button', { name: 'Move Amount left' }));
  expect(onChange).toHaveBeenLastCalledWith(['amount', '$name']);
});

it('saves the columns and the sort into the open table view', async () => {
  let request: { url: string; init: RequestInit } | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      request = { url: new URL(String(input), window.location.origin).pathname, init };
      return new Response(stringifyJSON({ id: 'view' }), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const view: ListView = { id: 'view', container_id: 'list', name: 'Open', columns: ['$name'], layout: 'table', version: 4, query: { filter: { field: 'status', op: 'eq', value: 'Open' } } };
  const { onSaved } = open({ changed: true, view, sort: { field: 'amount', direction: 'desc' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save to “Open”' }));
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith('view'));
  expect(request?.url).toBe('/v1/views/view');
  expect(request?.init.method).toBe('PUT');
  expect(request?.init.headers).toMatchObject({ 'If-Match': '"4"' });
  expect(parseJSON(String(request?.init.body))).toEqual({
    name: 'Open',
    columns: ['$name', 'amount'],
    layout: 'table',
    is_default: false,
    query: { filter: { field: 'status', op: 'eq', value: 'Open' }, sort: [{ field: 'amount', direction: 'desc' }] },
  });
});

it('offers saving to people who manage the list only', () => {
  open({ canManage: false, changed: true });
  expect(screen.getByRole('button', { name: 'Reset' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Save as new view…' })).toBeNull();
});
