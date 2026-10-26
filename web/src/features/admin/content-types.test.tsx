// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { ContentType, FieldDefinition, Resource } from '@/api/types';
import { CollectionContentTypes, ContentTypeCatalog } from './content-types';

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  vi.unstubAllGlobals();
});

function renderPage(children: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  clients.push(client);
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

function mockAPI(handle: (url: URL, init: RequestInit) => unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const result = handle(new URL(String(input), window.location.origin), init);
      return new Response(stringifyJSON(result), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
}

const fields: FieldDefinition[] = [
  { id: 'code', key: 'code', label: 'Code', type: 'text', required: true },
  { id: 'note', key: 'note', label: 'Note', type: 'text' },
];
const effective = (field_ids: string[], hidden_field_ids: string[] = []) => ({ field_ids, hidden_field_ids, rules: [] });
const item: ContentType = { id: 'item', key: 'item', name: 'Item', field_ids: [], effective: effective([]), version: 1 };
const record: ContentType = { id: 'record', key: 'record', name: 'Record', parent_id: 'item', ancestor_ids: ['item'], field_ids: ['code', 'note'], effective: effective(['code', 'note']), version: 1 };
const collection: Resource = { id: 'collection', workspace_id: 'workspace', kind: 'list', name: 'Records', version: 1, max_versions: 50 };

function catalogAPI(scopes: string[], listType: ContentType, onWrite?: (path: string, body: Record<string, unknown>) => void) {
  mockAPI((url, init) => {
    if (init.method === 'PUT' || init.method === 'POST') {
      onWrite?.(url.pathname, parseJSON(String(init.body)) as Record<string, unknown>);
      return listType;
    }
    if (url.pathname === '/v1/identity/me') return { subject: 'alice', scopes };
    if (url.pathname === '/v1/content-types') return [item, record];
    if (url.pathname === '/v1/fields') return fields;
    if (url.pathname === '/v1/resources/collection/content-types') return [listType];
    return { value: [] };
  });
}

describe('content type administration', () => {
  it('lets a list show a hidden field again once it became required', async () => {
    let saved: Record<string, unknown> | undefined;
    const listType: ContentType = { id: 'list-record', key: 'record', name: 'Record', parent_id: 'record', collection_id: 'collection', is_default: true, field_ids: [], hidden_field_ids: ['code'], effective: effective(['code', 'note'], ['code']), version: 3 };
    catalogAPI(['contentType.read', 'contentType.manage'], listType, (_, body) => (saved = body));
    renderPage(<CollectionContentTypes collection={collection} canManage />);
    fireEvent.click(await screen.findByRole('button', { name: 'Customize' }));
    const code = (await screen.findByLabelText('Show Code in forms')) as HTMLInputElement;
    expect(code.checked).toBe(false);
    expect(code.disabled).toBe(false);
    fireEvent.click(code);
    // Shown again, the required field cannot be hidden from this dialog.
    expect(code.checked).toBe(true);
    expect(code.disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(saved).toBeDefined());
    expect(saved!.hidden_field_ids).toEqual([]);
  });

  it('shows list types read-only without contentType.manage', async () => {
    const listType: ContentType = { id: 'list-record', key: 'record', name: 'Record', parent_id: 'record', collection_id: 'collection', is_default: true, field_ids: [], effective: effective(['code', 'note']), version: 1 };
    catalogAPI(['contentType.read'], listType);
    renderPage(<CollectionContentTypes collection={collection} canManage />);
    expect(screen.queryByRole('button', { name: 'Customize' })).toBeNull();
    fireEvent.click(await screen.findByRole('button', { name: 'View type' }));
    await screen.findByLabelText('Show Note in forms');
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('drops hidden and own fields a new parent no longer fits', async () => {
    let created: Record<string, unknown> | undefined;
    catalogAPI(['contentType.read', 'contentType.manage'], record, (path, body) => path === '/v1/content-types' && (created = body));
    renderPage(<ContentTypeCatalog />);
    fireEvent.click(await screen.findByRole('button', { name: 'New content type' }));
    fireEvent.change(screen.getByLabelText('Key'), { target: { value: 'invoice' } });
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Invoice' } });
    fireEvent.change(screen.getByLabelText('Derives from'), { target: { value: 'record' } });
    fireEvent.click(await screen.findByLabelText('Show Note in forms'));
    // Back on Item, Note is no longer inherited: it is neither hidden nor kept as owned.
    fireEvent.change(screen.getByLabelText('Derives from'), { target: { value: 'item' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save content type' }));
    await waitFor(() => expect(created).toBeDefined());
    expect(created).toMatchObject({ parent_id: 'item', field_ids: [], hidden_field_ids: [] });
  });
});
