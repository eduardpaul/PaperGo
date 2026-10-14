// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { FieldDefinition, ListView, Resource } from '@/api/types';
import { FieldEditor } from '@/features/fields/editor';
import { createFormatter } from '@/lib/format';
import { PreferencesProvider } from '@/lib/preferences';
import { ItemsBoard, ItemsCalendar, calendarDay, calendarRangeFilter } from './items-layouts';

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function renderWithClient(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  clients.push(client);
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

type Call = { path: string; init: RequestInit; body: Record<string, unknown> };
function mockAPI(handle: (call: Call) => unknown) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const url = new URL(String(input), window.location.origin);
      const call = { path: url.pathname, init, body: (init.body ? parseJSON(String(init.body)) : {}) as Record<string, unknown> };
      calls.push(call);
      return new Response(stringifyJSON(handle(call) ?? {}), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

const item = (id: string, name: string, values: Record<string, unknown> = {}): Resource => ({
  id,
  workspace_id: 'w',
  kind: 'item',
  name,
  version: 7,
  values,
});
const status: FieldDefinition = { id: 'status', key: 'status', label: 'Status', type: 'choice', choices: ['Open', 'Done'] };
const view = (query: ListView['query'], layout: string): ListView => ({
  id: 'v',
  container_id: 'list',
  name: 'Board',
  columns: ['$name', 'status'],
  layout,
  version: 1,
  query,
});

it('counts board columns over the whole list and loads each column from the API', async () => {
  const calls = mockAPI(({ path, init, body }) => {
    if (path === '/v1/resources/list/query/groups') return { value: [{ value: 'Open', count: 1200 }, { value: null, count: 3 }] };
    if (path === '/v1/resources/list/query') {
      const filter = (body.query as { filter: { and: { value?: string; op: string }[] } }).filter.and[1];
      return { value: filter.value === 'Open' ? [item('a', 'Review', { status: 'Open' })] : [], '@odata.nextLink': null };
    }
    if (init.method === 'PATCH') return item('a', 'Review');
  });
  renderWithClient(
    <ItemsBoard
      collectionId="list"
      view={view({ group_by: 'status', filter: { field: 'amount', op: 'gt', value: 5 } }, 'board')}
      fields={[status]}
      groupBy={status}
      onOpen={vi.fn()}
    />,
  );
  const open = screen.getByRole('region', { name: 'Open' });
  expect(await within(open).findByText('1,200')).toBeTruthy();
  expect(await within(open).findByText('Review')).toBeTruthy();
  expect(within(screen.getByRole('region', { name: 'No value' })).getByText('3')).toBeTruthy();
  const missing = calls.find((call) => call.path.endsWith('/query') && stringifyJSON(call.body)!.includes('"missing"'));
  expect(missing?.body.query).toEqual({
    filter: { and: [{ field: 'amount', op: 'gt', value: 5 }, { field: 'status', op: 'missing' }] },
  });
  fireEvent.drop(screen.getByRole('region', { name: 'No value' }), {
    dataTransfer: { getData: () => stringifyJSON({ id: 'a', version: 7, from: 'Open' }) },
  });
  await waitFor(() => expect(calls.some((call) => call.init.method === 'PATCH')).toBe(true));
  const patch = calls.find((call) => call.init.method === 'PATCH');
  expect(patch?.init).toMatchObject({ headers: { 'If-Match': '"7"' }, body: '{"values":{"status":null}}' });
});

it('asks the calendar for the visible weeks only and folds full days', async () => {
  vi.useFakeTimers({ toFake: ['Date'], now: new Date('2026-03-10T12:00:00Z') });
  const due: FieldDefinition = { id: 'due', key: 'due', label: 'Due', type: 'date' };
  const calls = mockAPI(({ path, body }) => {
    if (path !== '/v1/resources/list/query') return {};
    const filter = (body.query as { filter: { op?: string } }).filter;
    if (filter.op === 'missing') return { value: [item('u', 'Undated')] };
    return { value: Array.from({ length: 6 }, (_, i) => item(`d${i}`, `Due ${i}`, { due: '2026-03-12' })) };
  });
  renderWithClient(<ItemsCalendar collectionId="list" view={view({ group_by: 'due' }, 'calendar')} dateField={due} onOpen={vi.fn()} />);
  expect(await screen.findByText('6 items this month')).toBeTruthy();
  expect(screen.getByRole('button', { name: '2 more' })).toBeTruthy();
  expect(screen.queryByText('Due 5')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: '2 more' }));
  expect(screen.getByText('Due 5')).toBeTruthy();
  expect(screen.getByText('Undated')).toBeTruthy();
  const ranged = calls.find((call) => stringifyJSON(call.body)!.includes('"gte"'));
  expect(ranged?.body.query).toEqual({
    sort: [{ field: 'due', direction: 'asc' }],
    filter: { and: [{ field: 'due', op: 'gte', value: '2026-02-23' }, { field: 'due', op: 'lte', value: '2026-04-05' }] },
  });
  vi.useRealTimers();
});

it('widens moment ranges by the largest time zone offsets', () => {
  expect(calendarRangeFilter({ id: 'm', key: 'meeting', label: 'Meeting', type: 'datetime' }, '2026-02-23', '2026-04-05')).toEqual({
    and: [
      { field: 'meeting', op: 'gte', value: '2026-02-22T10:00:00.000Z' },
      { field: 'meeting', op: 'lt', value: '2026-04-06T14:00:00.000Z' },
    ],
  });
});

it.each(['event_start', 'event_start_date'])('keeps all-day occurrences on their dates when grouped by %s', async (key) => {
  vi.useFakeTimers({ toFake: ['Date'], now: new Date('2026-10-14T12:00:00Z') });
  mockAPI(({ path }) => {
    if (path === '/v1/identity/me/preferences') return { time_zone: 'America/New_York' };
    if (path.endsWith('/calendar/occurrences')) return [
      { key: 'holiday', name: 'Holiday', start: '2026-10-14T00:00:00Z', end: '2026-10-15T00:00:00Z', all_day: true, start_date: '2026-10-14', item: item('holiday', 'Holiday', { event_all_day: true }) },
      { key: 'meeting', name: 'Meeting', start: '2026-10-14T00:30:00Z', end: '2026-10-14T01:30:00Z', all_day: false, item: item('meeting', 'Meeting') },
    ];
    return { value: [] };
  });
  renderWithClient(
    <PreferencesProvider><ItemsCalendar collectionId="list" view={view({ group_by: key }, 'calendar')} dateField={{ id: key, key, label: 'Start', type: key === 'event_start' ? 'datetime' : 'date' }} onOpen={vi.fn()} /></PreferencesProvider>,
  );
  await waitFor(() => {
    const dayOf = (name: string) => within(screen.getByRole('button', { name }).closest('ul')!.parentElement!);
    expect(dayOf('Holiday').getByText('14')).toBeTruthy();
    expect(dayOf('Meeting').getByText('13')).toBeTruthy();
  });
});

it('keeps date-only fields on their day and groups moments in the preferred time zone', () => {
  const entry = item('item', 'x', { due: '2026-10-12', meeting: '2026-10-12T00:30:00Z' });
  const format = createFormatter({ timeZone: 'America/New_York' });
  expect(calendarDay(entry, { id: 'due', key: 'due', label: 'Due', type: 'date' }, format.dayKey)).toBe('2026-10-12');
  expect(calendarDay(entry, { id: 'meeting', key: 'meeting', label: 'Meeting', type: 'datetime' }, format.dayKey)).toBe(
    '2026-10-11',
  );
});

it('searches a lookup list by name as people type and shows the current value by id', async () => {
  const calls = mockAPI(({ path, body }) => {
    if (path === '/v1/$batch') return { responses: [{ id: 'far', status: 200, body: { id: 'far', name: 'Umbrella 1555' } }] };
    if (path === '/v1/resources/suppliers/query') {
      const text = (body.query as { filter?: { value: string } }).filter?.value;
      return { value: text ? [item('acme', 'Acme 12')] : [item('first', 'Aardvark 1')], '@odata.nextLink': text ? null : '/next' };
    }
  });
  // cmdk measures its list; jsdom has no layout.
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  Element.prototype.scrollIntoView ??= () => {};
  const onChange = vi.fn();
  renderWithClient(
    <FieldEditor
      field={{ id: 's', key: 'supplier', label: 'Supplier', type: 'lookup', options: { lookup_container_id: 'suppliers' } }}
      value="far"
      onChange={onChange}
    />,
  );
  const picker = screen.getByRole('combobox', { name: 'Supplier' });
  await waitFor(() => expect(picker.textContent).toContain('Umbrella 1555'));
  fireEvent.click(picker);
  expect(await screen.findByText('More items match. Type to narrow the list.')).toBeTruthy();
  fireEvent.change(screen.getByPlaceholderText('Search…'), { target: { value: 'acm' } });
  fireEvent.click(await screen.findByRole('option', { name: /Acme 12/ }));
  expect(onChange).toHaveBeenCalledWith('acme');
  expect(calls.find((call) => stringifyJSON(call.body)!.includes('acm'))?.body).toEqual({
    query: { sort: [{ field: '$name', direction: 'asc' }], filter: { field: '$name', op: 'contains', value: 'acm' } },
    select: [],
  });
});
