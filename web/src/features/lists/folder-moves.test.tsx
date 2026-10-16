// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import type { Resource } from '@/api/types';
import { readMoveDrag, resourceDragType, useFolderMoves } from './folder-moves';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const collection: Resource = { id: 'list', workspace_id: 'w', name: 'Files', kind: 'library', version: 1, access: ['read', 'write'] };
const item: Resource = { id: 'source', workspace_id: 'w', container_id: 'list', parent_id: 'list', name: 'Report.pdf', kind: 'item', version: 3, access: ['read', 'write'] };
const target: Resource = { ...item, id: 'folder', kind: 'folder', name: 'Archive' };
function Harness() {
  const [selected, setSelected] = useState(new Map<string, Resource>());
  const moves = useFolderMoves(collection, selected, items => setSelected(new Map(items.map(item => [item.id, item]))));
  return <><button {...moves.sourceProps(item)}>Source</button><div {...moves.targetProps(target)}>Target</div>{moves.dialogs}</>;
}
it('offers a conditional replacement only after a conflict and explicit confirmation', async () => {
  const fetch = vi.fn(async (input: URL | string, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    if (path.endsWith('/move-selection')) return new Response(JSON.stringify({ code: 'name_conflict', detail: 'Already exists', source_item_id: 'source', existing_item: { id: 'existing', version: 7 } }), { status: 409 });
    return new Response(JSON.stringify(init?.method === 'POST' ? {} : item));
  });
  vi.stubGlobal('fetch', fetch);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  let data = '';
  const dataTransfer = { types: [resourceDragType], setData: (_type: string, value: string) => { data = value; }, getData: () => data };
  fireEvent.dragStart(screen.getByText('Source'), { dataTransfer });
  fireEvent.drop(screen.getByText('Target'), { dataTransfer });
  expect(await screen.findByText('Name already in use')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Replace…' }));
  expect(fetch.mock.calls.some(([input]) => String(input).endsWith('/bulk'))).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: 'Replace and move' }));
  await waitFor(() => expect(fetch.mock.calls.some(([input]) => String(input).endsWith('/bulk'))).toBe(true));
  const [, init] = fetch.mock.calls.find(([input]) => String(input).endsWith('/bulk'))!;
  expect(JSON.parse(String(init?.body))).toEqual({ operations: [
    { action: 'delete', id: 'existing', version: 7 },
    { action: 'update', id: 'source', version: 3, update: { parent_id: 'folder' } },
  ] });
});
it('rejects foreign, duplicate and malformed drag data', () => {
  expect(() => readMoveDrag(JSON.stringify({ collection_id: 'other', items: [item] }), collection)).toThrow();
  expect(() => readMoveDrag(JSON.stringify({ collection_id: 'list', items: [item, item] }), collection)).toThrow();
  expect(() => readMoveDrag(JSON.stringify({ collection_id: 'list', items: [{ ...item, version: 0 }] }), collection)).toThrow();
  expect(readMoveDrag(JSON.stringify({ collection_id: 'list', items: [item] }), collection)[0].id).toBe('source');
});
