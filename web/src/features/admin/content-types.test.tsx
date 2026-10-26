// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

function catalogAPI(scopes: string[], listType: ContentType, onWrite?: (path: string, body: Record<string, unknown>) => void, catalog: ContentType[] = [item, record], listTypes: ContentType[] = []) {
  mockAPI((url, init) => {
    if (init.method === 'PUT' || init.method === 'POST' || init.method === 'DELETE') {
      onWrite?.(url.pathname, init.body ? (parseJSON(String(init.body)) as Record<string, unknown>) : { method: init.method, ifMatch: new Headers(init.headers).get('If-Match') });
      return listType;
    }
    if (url.pathname === '/v1/identity/me') return { subject: 'alice', scopes };
    if (url.pathname === '/v1/content-types') return catalog;
    if (url.pathname === '/v1/fields') return fields;
    if (url.pathname === '/v1/resources/collection/content-types') return [listType, ...listTypes];
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

  it('offers the deepest legal parent and shows an existing type\'s ancestry', async () => {
    // Level n has n ancestors; catalog types have at most 7.
    const levels: ContentType[] = [item];
    for (let n = 1; n <= 7; n++) {
      const parent = levels[n - 1];
      levels.push({ id: `level-${n}`, key: `level-${n}`, name: `Level ${n}`, parent_id: parent.id, ancestor_ids: [parent.id, ...(parent.ancestor_ids ?? [])], field_ids: [], effective: effective([]), version: 1 });
    }
    catalogAPI(['contentType.read', 'contentType.manage'], record, undefined, levels);
    renderPage(<ContentTypeCatalog />);
    fireEvent.click(await screen.findByRole('button', { name: 'New content type' }));
    const offered = [...(screen.getByLabelText('Derives from') as HTMLSelectElement).options].map((option) => option.value);
    expect(offered).toContain('level-6');
    expect(offered).not.toContain('level-7');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[7]);
    const parent = (await screen.findByLabelText('Derives from')) as HTMLSelectElement;
    expect(parent.value).toBe('level-6');
    expect(parent.selectedOptions[0].textContent).toBe('Item › Level 1 › Level 2 › Level 3 › Level 4 › Level 5 › Level 6');
  });

  it('deletes a catalog type at its version', async () => {
    let deleted: Record<string, unknown> | undefined;
    catalogAPI(['contentType.read', 'contentType.manage'], record, (path, body) => path === '/v1/content-types/record' && (deleted = body));
    renderPage(<ContentTypeCatalog />);
    const rows = within((await screen.findByRole('heading', { name: 'Content types' })).closest('[aria-labelledby]') as HTMLElement);
    await rows.findByText('Record');
    // The root Item type offers no delete.
    expect(rows.getAllByRole('button', { name: 'Delete' })).toHaveLength(1);
    fireEvent.click(rows.getByRole('button', { name: 'Delete' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete content type' }));
    await waitFor(() => expect(deleted).toEqual({ method: 'DELETE', ifMatch: '"1"' }));
  });

  it('files types under groups and filters the catalog by group', async () => {
    let created: Record<string, unknown> | undefined;
    const memo: ContentType = { ...record, id: 'memo', key: 'memo', name: 'Memo' };
    catalogAPI(['contentType.read', 'contentType.manage'], record, (path, body) => path === '/v1/content-types' && (created = body), [item, { ...record, group: 'Finance' }, memo]);
    renderPage(<ContentTypeCatalog />);
    fireEvent.change(await screen.findByLabelText('Group'), { target: { value: 'Finance' } });
    expect(screen.getByText('Record')).toBeTruthy();
    expect(screen.queryByText('Memo')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'New content type' }));
    const dialog = within(await screen.findByRole('dialog'));
    fireEvent.change(dialog.getByLabelText('Key'), { target: { value: 'quote' } });
    fireEvent.change(dialog.getByLabelText('Name'), { target: { value: 'Quote' } });
    fireEvent.change(dialog.getByLabelText('Group'), { target: { value: ' Finance ' } });
    fireEvent.click(dialog.getByRole('button', { name: 'Save content type' }));
    await waitFor(() => expect(created).toMatchObject({ key: 'quote', group: 'Finance' }));
  });

  it('chooses and orders the types the New menu offers', async () => {
    const writes: Record<string, unknown>[] = [];
    const recordType: ContentType = { id: 'list-record', key: 'record', name: 'Record', parent_id: 'record', collection_id: 'collection', is_default: true, in_new_menu: true, field_ids: [], effective: effective([]), version: 1 };
    const memo: ContentType = { ...recordType, id: 'list-memo', key: 'memo', name: 'Memo', parent_id: 'memo', is_default: false };
    const note: ContentType = { ...memo, id: 'list-note', key: 'note', name: 'Note', parent_id: 'note', in_new_menu: false };
    catalogAPI(['contentType.read', 'contentType.manage'], recordType, (path, body) => path.endsWith('/content-type-menu') && writes.push(body), [item, record], [memo, note]);
    renderPage(<CollectionContentTypes collection={collection} canManage />);
    const offerRecord = (await screen.findByLabelText('Offer Record in the New menu')) as HTMLInputElement;
    // The default is always offered.
    expect(offerRecord.disabled).toBe(true);
    expect(screen.getByText('Not in New menu')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Offer Note in the New menu'));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ content_type_ids: ['list-record', 'list-memo', 'list-note'] });
    fireEvent.click(screen.getByRole('button', { name: 'Move Memo up' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toEqual({ content_type_ids: ['list-memo', 'list-record'] });
  });
});
