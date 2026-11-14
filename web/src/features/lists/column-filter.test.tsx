// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import { keys } from '@/api/queries';
import type { FieldDefinition, ListView, Resource } from '@/api/types';
import { setLanguage } from '@/i18n';
import { CollectionPage, type CollectionSearch } from './collection-page';
import { ColumnFilterBar } from './column-filter';

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a href="#collection">{children}</a>,
  useNavigate: () => vi.fn(),
  useRouter: () => ({ history: { back: vi.fn() } }),
  useCanGoBack: () => false,
}));
vi.mock('@/features/documents/uploads', () => ({ useUploads: () => ({ upload: vi.fn(async () => []) }) }));

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await setLanguage('en');
});

const fields: FieldDefinition[] = [
  { id: 'producto', key: 'producto', label: 'Producto', type: 'text', indexed: true, index_status: 'ready' },
  { id: 'categoria', key: 'categoria', label: 'Categoría', type: 'choice', indexed: true, index_status: 'ready', choices: ['Quesos', 'Fruta', 'Pan'] },
  { id: 'importe', key: 'importe', label: 'Importe', type: 'decimal', scale: 2, indexed: true, index_status: 'ready' },
  { id: 'nota', key: 'nota', label: 'Nota', type: 'note' },
  { id: 'moneda', key: 'moneda', label: 'Moneda', type: 'text' },
];
const view: ListView = {
  id: 'lineas', container_id: 'tiques', name: 'Líneas', version: 3, layout: 'table', is_default: true,
  columns: ['$name', 'producto', 'categoria', 'importe', 'nota', 'moneda'],
  query: { filter: { field: 'importe', op: 'present' } },
};
const row = { id: 'r1', workspace_id: 'w', kind: 'item', name: 'Kiwi', version: 1, parent_id: 'tiques', values: { producto: 'kiwi', categoria: 'Fruta', importe: '2.40' } } as Resource;

function show(initial: CollectionSearch = {}, access = ['read', 'write', 'manage']) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(keys.resource('tiques'), { id: 'tiques', workspace_id: 'w', kind: 'library', name: 'Tiques', version: 1, access } as Resource);
  client.setQueryData(keys.fields('tiques'), fields);
  client.setQueryData(keys.contentTypes('tiques'), []);
  client.setQueryData(keys.views('tiques'), [view]);
  const calls: { path: string; method: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const path = new URL(String(input)).pathname;
    const body = init.body ? (parseJSON(String(init.body)) as Record<string, unknown>) : {};
    calls.push({ path, method: init.method ?? 'GET', body });
    const answer = path.endsWith('/query/groups') ? { value: [{ value: 'kiwi', count: 3 }, { value: 'manzana', count: 1 }, { value: null, count: 2 }] } : path.endsWith('/query') ? { value: [row] } : path.startsWith('/v1/views/') ? { ...view, version: 4 } : { value: [] };
    return new Response(stringifyJSON(answer), { headers: { 'Content-Type': 'application/json' } });
  }));
  const onSearch = vi.fn();
  function Harness() {
    const [search, setSearch] = useState(initial);
    return <CollectionPage workspaceId="w" listId="tiques" search={search} onMoved={vi.fn()} onSearch={(patch, replace) => {
      onSearch(patch, replace);
      setSearch((current) => ({ ...current, ...patch }));
    }} />;
  }
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>);
  return { calls, onSearch };
}

const lastRows = (calls: { path: string; body: Record<string, unknown> }[]) =>
  calls.filter((call) => call.path === '/v1/resources/tiques/query' && (call.body.select as string[] | undefined)?.length).at(-1);

it('filters a choice column from its header, on top of the view’s filter, and shows it', async () => {
  const { calls, onSearch } = show();
  await screen.findByText('Kiwi');
  fireEvent.click(screen.getByRole('button', { name: 'Filter Categoría' }));
  const form = await screen.findByRole('form', { name: 'Filter Categoría' });
  fireEvent.click(within(form).getByRole('checkbox', { name: 'Quesos' }));
  fireEvent.click(within(form).getByRole('checkbox', { name: 'Fruta' }));
  fireEvent.click(within(form).getByRole('button', { name: 'Apply' }));
  expect(onSearch).toHaveBeenLastCalledWith({ filters: { categoria: { op: 'values', values: ['Quesos', 'Fruta'] } } }, true);
  await waitFor(() =>
    expect(lastRows(calls)?.body.query).toEqual({
      filter: { and: [{ field: 'importe', op: 'present' }, { field: 'categoria', op: 'in', value: ['Quesos', 'Fruta'] }] },
    }),
  );
  expect(await screen.findByRole('button', { name: 'Filter Categoría (filtered)' })).toBeTruthy();
  const bar = screen.getByRole('group', { name: 'Column filters' });
  expect(within(bar).getByRole('button', { name: 'Change filter: Categoría: Quesos or Fruta' })).toBeTruthy();
  fireEvent.click(within(bar).getByRole('button', { name: 'Clear filters' }));
  expect(onSearch).toHaveBeenLastCalledWith({ filters: undefined }, true);
});

it('ranges a number column with the decimal separator people type', async () => {
  const { calls } = show();
  await screen.findByText('Kiwi');
  fireEvent.click(screen.getByRole('button', { name: 'Filter Importe' }));
  const form = await screen.findByRole('form', { name: 'Filter Importe' });
  fireEvent.change(within(form).getByLabelText('From'), { target: { value: '5' } });
  fireEvent.change(within(form).getByLabelText('To'), { target: { value: 'twenty' } });
  fireEvent.click(within(form).getByRole('button', { name: 'Apply' }));
  expect(within(form).getByRole('alert')).toBeTruthy();
  fireEvent.change(within(form).getByLabelText('To'), { target: { value: '20.5' } });
  fireEvent.click(within(form).getByRole('button', { name: 'Apply' }));
  await waitFor(() =>
    expect((lastRows(calls)?.body.query as { filter: unknown }).filter).toEqual({
      and: [{ field: 'importe', op: 'present' }, { field: 'importe', op: 'gte', value: '5' }, { field: 'importe', op: 'lte', value: '20.5' }],
    }),
  );
});

it('lists a text column’s values among what the view shows, and says why other columns cannot filter', async () => {
  const { calls } = show({ filters: { categoria: { op: 'values', values: ['Fruta'] } } });
  await screen.findByText('Kiwi');
  fireEvent.click(screen.getByRole('button', { name: 'Filter Producto' }));
  const form = await screen.findByRole('form', { name: 'Filter Producto' });
  fireEvent.change(within(form).getByRole('combobox', { name: 'Condition' }), { target: { value: 'values' } });
  fireEvent.click(await within(form).findByRole('checkbox', { name: /kiwi/ }));
  const groups = calls.find((call) => call.path.endsWith('/query/groups'));
  expect(groups?.body.query).toEqual({ group_by: 'producto', filter: { and: [{ field: 'importe', op: 'present' }, { field: 'categoria', op: 'eq', value: 'Fruta' }] } });
  fireEvent.click(within(form).getByRole('checkbox', { name: 'No value' }));
  fireEvent.click(within(form).getByRole('button', { name: 'Apply' }));
  await waitFor(() =>
    expect((lastRows(calls)?.body.query as { filter: { and: unknown[] } }).filter.and.at(-1)).toEqual({
      or: [{ field: 'producto', op: 'eq', value: 'kiwi' }, { field: 'producto', op: 'missing' }],
    }),
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Filter Nota' }));
  expect(await screen.findByText('Notes are long text and can’t be filtered. Search finds words in them.')).toBeTruthy();
  fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
  fireEvent.click(screen.getByRole('button', { name: 'Filter Moneda' }));
  expect(await screen.findByText('To filter by this column, make it filterable in the list’s column settings.')).toBeTruthy();
});

it('saves the filters into the view for people who may edit views', async () => {
  const { calls, onSearch } = show({ filters: { producto: { op: 'contains', text: 'kiwi' } } });
  await screen.findByText('Kiwi');
  fireEvent.click(screen.getByRole('button', { name: 'Save filters to “Líneas”' }));
  await waitFor(() => expect(onSearch).toHaveBeenLastCalledWith({ filters: undefined }, true));
  const put = calls.find((call) => call.method === 'PUT');
  expect(put?.path).toBe('/v1/views/lineas');
  expect(put?.body).toMatchObject({ columns: view.columns, query: { filter: { and: [{ field: 'importe', op: 'present' }, { field: 'producto', op: 'contains', value: 'kiwi' }] } } });
});

it('lets people who cannot edit views filter for themselves', async () => {
  show({ filters: { producto: { op: 'contains', text: 'kiwi' } } }, ['read']);
  await screen.findByText('Kiwi');
  expect(screen.getByRole('button', { name: 'Change filter: Producto contains “kiwi”' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: /Save filters/ })).toBeNull();
});

it('describes filters in Spanish', async () => {
  await act(() => setLanguage('es'));
  vi.stubGlobal('fetch', vi.fn(async () => new Response('[]', { headers: { 'Content-Type': 'application/json' } })));
  const filtering = { collectionId: 'tiques', fields, filters: {}, apply: () => true, valuesQuery: () => ({}) };
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ColumnFilterBar
        filtering={filtering}
        active={[
          ['categoria', { op: 'values', values: ['Quesos', 'Fruta'] }],
          ['importe', { op: 'range', from: '5', to: '20' }],
          ['producto', { op: 'empty' }],
        ]}
        onClear={vi.fn()}
      />
    </QueryClientProvider>,
  );
  expect(screen.getByText('Categoría: Quesos o Fruta')).toBeTruthy();
  expect(screen.getByText('Importe: de 5 a 20')).toBeTruthy();
  expect(screen.getByText('Producto está vacío')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Quitar filtros' })).toBeTruthy();
});
