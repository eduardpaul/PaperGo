// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { useState, type ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { CollectionPage, type CollectionSearch } from './collection-page';

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a href="#collection">{children}</a>,
}));
const uploads = vi.hoisted(() => ({ upload: vi.fn(async () => []) }));
vi.mock('@/features/documents/uploads', () => ({ useUploads: () => uploads }));

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  uploads.upload.mockClear();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function show(initial: CollectionSearch = {}, overrides: Partial<Resource> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  const collection: Resource = {
    id: 'library', workspace_id: 'workspace', kind: 'library', name: 'Documents', version: 1, access: ['read', 'write'], ...overrides,
  };
  client.setQueryData(keys.resource('library'), collection);
  client.setQueryData(keys.fields('library'), []);
  client.setQueryData(keys.contentTypes('library'), []);
  client.setQueryData(keys.views('library'), []);
  const fetch = vi.fn(async () => new Response(stringifyJSON({ value: [] }), { headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetch);
  const onSearch = vi.fn();
  let navigate: (search: CollectionSearch) => void;
  function Harness() {
    const [search, setSearch] = useState(initial);
    navigate = setSearch;
    return <CollectionPage workspaceId="workspace" listId="library" search={search} onMoved={vi.fn()} onSearch={(patch, replace) => {
      onSearch(patch, replace);
      setSearch((current) => ({ ...current, ...patch }));
    }} />;
  }
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>);
  return { onSearch, fetch, navigate: (search: CollectionSearch) => act(() => navigate(search)) };
}

it('keeps the same focused input across debounced query updates and clears immediately', async () => {
  const { onSearch } = show();
  const input = screen.getByRole('textbox', { name: 'Search this library' }) as HTMLInputElement;
  vi.useFakeTimers();
  input.focus();
  for (const query of ['a', 'annual', 'annual report']) {
    fireEvent.change(input, { target: { value: query } });
    await act(() => vi.advanceTimersByTimeAsync(350));
    expect(onSearch).toHaveBeenLastCalledWith({ q: query }, true);
    expect(screen.getByRole('textbox', { name: 'Search this library' })).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe(query);
  }
  const clear = within(screen.getByRole('search')).getByRole('button', { name: 'Clear search' });
  clear.focus();
  fireEvent.click(clear);
  expect(onSearch).toHaveBeenLastCalledWith({ q: undefined }, true);
  expect(input.value).toBe('');
  expect(document.activeElement).toBe(input);
  await act(() => vi.advanceTimersByTimeAsync(350));
  expect(onSearch).toHaveBeenCalledTimes(4);
});

it('follows URL navigation and cancels a pending local search without losing focus', async () => {
  const { onSearch, navigate } = show({ q: 'first' });
  const input = screen.getByRole('textbox', { name: 'Search this library' }) as HTMLInputElement;
  vi.useFakeTimers();
  input.focus();
  fireEvent.change(input, { target: { value: 'pending' } });
  navigate({ q: 'from history' });
  expect(input.value).toBe('from history');
  expect(document.activeElement).toBe(input);
  await act(() => vi.advanceTimersByTimeAsync(350));
  expect(onSearch).not.toHaveBeenCalled();
  navigate({});
  expect(input.value).toBe('');
  fireEvent.change(input, { target: { value: 'clear with escape' } });
  fireEvent.keyDown(input, { key: 'Escape' });
  expect(onSearch).toHaveBeenLastCalledWith({ q: undefined }, true);
  expect(input.value).toBe('');
  expect(document.activeElement).toBe(input);
});


it.each(['Files', 'files', 'FILES', 'application/x-moz-file', 'APPLICATION/X-MOZ-FILE', 'file-items', 'file-list'])('accepts %s file drags and enqueues all dropped files', async (type) => {
  show();
  const contents = screen.getByRole('region', { name: 'Library contents' });
  const files = [new File(['a'], 'a.txt'), new File(['b'], 'b.txt')];
  const transfer = {
    types: type.startsWith('file-') ? [] : [type],
    items: type === 'file-items' ? [{ kind: 'file' }] : [],
    files: type === 'file-list' ? files : [],
    dropEffect: 'none',
  };
  expect(fireEvent.dragOver(contents, { dataTransfer: transfer })).toBe(false);
  expect(transfer.dropEffect).toBe('copy');
  expect(screen.getByText('Drop to upload to Documents')).toBeTruthy();
  await act(async () => {
    expect(fireEvent.drop(contents, { dataTransfer: { ...transfer, files } })).toBe(false);
  });
  expect(uploads.upload).toHaveBeenCalledWith(files, expect.objectContaining({ url: '/v1/resources/library/documents', label: 'Documents' }));
  expect(screen.queryByText('Drop to upload to Documents')).toBeNull();
});

it.each([{ kind: 'list' as const }, { access: ['read'] as Resource['access'] }, {}])('does not upload non-file or unauthorized drags (%j)', async (overrides) => {
  show({}, overrides);
  const contents = screen.getByRole('region', { name: overrides.kind === 'list' ? 'List contents' : 'Library contents' });
  const transfer = Object.keys(overrides).length
    ? { types: ['Files'], files: [new File(['a'], 'a.txt')] }
    : { types: ['text/plain', 'application/x-papergo-item'], files: [] };
  expect(fireEvent.dragOver(contents, { dataTransfer: transfer })).toBe(true);
  await act(async () => { fireEvent.drop(contents, { dataTransfer: transfer }); });
  expect(uploads.upload).not.toHaveBeenCalled();
  expect(screen.queryByText('Drop to upload to Documents')).toBeNull();
});
