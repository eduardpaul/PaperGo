// @vitest-environment jsdom
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Suspense } from 'react';
import type { ComponentType, ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import { resourceQuery } from '@/api/queries';
import type { BuiltInState, FieldDefinition, Resource, Workflow } from '@/api/types';
import { Route as WorkflowsRoute } from '@/routes/_app/w/$workspaceId/settings/workflows';
import { FieldEditor } from './fields/editor';
import { CollectionColumns } from './admin/content-types';
import { CollectionGeneral } from './lists/collection-settings';
import { ItemSheet } from './lists/item-sheet';
import { CreateSmartFolderDialog } from './smart-folders/create-dialog';

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...await importOriginal<typeof Router>(),
  createFileRoute: () => (options: unknown) => ({ options, useParams: () => ({ workspaceId: 'workspace' }) }),
}));

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

function collection(): Resource {
  return { id: 'collection', workspace_id: 'workspace', kind: 'list', name: 'Records', version: 1, max_versions: 50 };
}

function mockAPI(handle: (url: URL, init: RequestInit) => unknown) {
  const fetch = vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const result = handle(new URL(String(input), window.location.origin), init);
    return new Response(stringifyJSON(result), { headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

describe('reviewed frontend flows', () => {
  it('saves exact item fields without offering or sending a description', async () => {
    let item: Resource = { id: 'item', workspace_id: 'workspace', container_id: 'collection', kind: 'item', name: 'Original', version: 1, values: {}, access: ['read', 'read_draft', 'write'] };
    let patch: Record<string, unknown> | undefined;
    const fields: FieldDefinition[] = [{ id: 'amount', key: 'amount', label: 'Amount', type: 'currency' }];
    mockAPI((url, init) => {
      if (init.method === 'PATCH') {
        patch = parseJSON(String(init.body)) as Record<string, unknown>;
        item = { ...item, name: String(patch.name), values: patch.values as Resource['values'], version: 2 };
        return item;
      }
      if (url.pathname === '/v1/resources/item') return item;
      return { value: [] };
    });
    renderPage(<ItemSheet itemId="item" collection={collection()} fields={fields} contentTypes={[]} tab="details" onTab={vi.fn()} onClose={vi.fn()} onMoved={vi.fn()} />);
    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Renamed' } });
    expect(screen.queryByLabelText('Description')).toBeNull();
    fireEvent.change(screen.getByLabelText('Amount'), { target: { value: '12.' } });
    expect((screen.getByLabelText('Amount') as HTMLInputElement).value).toBe('12.');
    fireEvent.change(screen.getByLabelText('Amount'), { target: { value: '9007199254740993' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(patch).toBeDefined());
    expect(stringifyJSON(patch)).toBe('{"name":"Renamed","values":{"amount":9007199254740993}}');
    await waitFor(() => expect((screen.getByLabelText('Amount') as HTMLInputElement).value).toBe('9007199254740993'));
  });

  it.each([false, true])('writes a keywords field with multiple=%s using its declared shape', async (multiple) => {
    let submitted: unknown;
    mockAPI(() => ({ value: [{ id: 'keyword', name: 'Finance' }] }));
    renderPage(<FieldEditor field={{ id: 'tags', key: 'tags', label: 'Keywords', type: 'keywords', options: { multiple } }} value={undefined} onChange={(value) => { submitted = value; }} />);
    fireEvent.change(screen.getByLabelText('Keywords'), { target: { value: 'fin' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Finance' }));
    expect(submitted).toEqual(multiple ? ['keyword'] : 'keyword');
  });

  it('uses the collection version after adding a column when saving its settings', async () => {
    const record = collection();
    const fields: FieldDefinition[] = [{ id: 'old', key: 'old', label: 'Old field', type: 'text' }];
    let added: Record<string, unknown> | undefined;
    let savedVersion: string | undefined;
    mockAPI((url, init) => {
      if (url.pathname.endsWith('/fields') && init.method === 'POST') {
        added = parseJSON(String(init.body)) as Record<string, unknown>;
        const field: FieldDefinition = { id: 'added', key: 'added_field', label: 'Added field', type: 'text' };
        fields.push(field);
        record.version++;
        return field;
      }
      if (url.pathname.endsWith('/fields')) return { value: fields };
      if (init.method === 'PATCH') {
        savedVersion = (init.headers as Record<string, string>)['If-Match'];
        return { ...record, name: 'Renamed', version: record.version + 1 };
      }
      if (url.pathname === '/v1/resources/collection') return record;
      return { value: [] };
    });
    function Page() {
      const current = useQuery(resourceQuery('collection'));
      if (!current.data) return null;
      return <>
        <CollectionColumns collection={current.data} canManage />
        <CollectionGeneral key={current.data.version} collection={current.data} canManage onDeleted={vi.fn()} />
      </>;
    }
    renderPage(<Page />);
    fireEvent.click(await screen.findByRole('button', { name: 'Add column' }));
    fireEvent.change(screen.getByLabelText('Name', { selector: '#catalog-field-label' }), { target: { value: 'Added field' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save field' }));
    await screen.findByText('Added field');
    expect(added).toMatchObject({ key: 'added_field', label: 'Added field', type: 'text' });
    fireEvent.change(screen.getByLabelText('Name', { selector: '#collection-name' }), { target: { value: 'Renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(savedVersion).toBe('"2"'));
  });

  it('uses the selected built-in workflow version and resets parameters when switching collections', async () => {
    const workflow = (id: string, version: number, field: string): Workflow => ({ id, collection_id: id, workspace_id: 'workspace', key: id, name: id, description: '', enabled: true, current_version: 1, definition: {}, version, parameters: { field } });
    const builtin: BuiltInState = { key: 'items.expire', name: 'Expire', description: '', per_collection: true, parameters_schema: { properties: { field: { title: 'Expiry field', default: 'default' } } }, workflows: [workflow('a', 2, 'expiry_a'), workflow('b', 9, 'expiry_b')] };
    const writes: { version?: string; body: { collection_id: string; parameters: { field: string } } }[] = [];
    mockAPI((url, init) => {
      if (init.method === 'PUT') {
        const body = parseJSON(String(init.body)) as (typeof writes)[number]['body'];
        writes.push({ version: (init.headers as Record<string, string>)['If-Match'], body });
        const previous = builtin.workflows?.find((entry) => entry.collection_id === body.collection_id);
        const next = workflow(body.collection_id, (previous?.version ?? 0) + 1, body.parameters.field);
        builtin.workflows = [...(builtin.workflows ?? []).filter((entry) => entry.id !== next.id), next];
        return next;
      }
      if (url.pathname.endsWith('/workflow-builtins')) return { value: [builtin] };
      if (url.pathname === '/v1/resources/workspace') return { id: 'workspace', workspace_id: 'workspace', kind: 'workspace', name: 'Workspace', version: 1, access: ['read', 'read_draft', 'write', 'publish', 'manage'] };
      if (url.pathname.endsWith('/children')) return { value: ['a', 'b', 'c'].map((id) => ({ ...collection(), id, name: id })) };
      return { value: [] };
    });
    const Page = WorkflowsRoute.options.component as ComponentType;
    await act(async () => { renderPage(<Suspense fallback="Loading workflows"><Page /></Suspense>); });
    const picker = await screen.findByLabelText('Collection');
    await screen.findByRole('option', { name: 'c' });
    fireEvent.change(picker, { target: { value: 'b' } });
    expect((screen.getByLabelText('Expiry field') as HTMLInputElement).value).toBe('expiry_b');
    fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({ version: '"9"', body: { collection_id: 'b', enabled: true, parameters: { field: 'expiry_b' } } });
    await waitFor(() => expect((screen.getByRole('button', { name: 'Enable' }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole('button', { name: 'Disable' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1].version).toBe('"10"');
    fireEvent.change(picker, { target: { value: 'c' } });
    expect((screen.getByLabelText('Expiry field') as HTMLInputElement).value).toBe('default');
    fireEvent.click(screen.getByRole('button', { name: 'Enable' }));
    await waitFor(() => expect(writes).toHaveLength(3));
    expect(writes[2].version).toBeUndefined();
    expect(writes[2].body.collection_id).toBe('c');
  });

  it.each([
    ['text', 'open', '"open"'],
    ['number', '5', '5'],
    ['number', '9007199254740993', '9007199254740993'],
    ['boolean', 'false', 'false'],
  ])('creates a smart-folder comparison with a %s literal', async (type, input, literal) => {
    let body: Record<string, unknown> | undefined;
    mockAPI((_url, init) => {
      if (init.method === 'POST') {
        body = parseJSON(String(init.body)) as Record<string, unknown>;
        return { id: 'folder', name: 'Filtered', version: 1 };
      }
      return { value: [] };
    });
    renderPage(<CreateSmartFolderDialog open onClose={vi.fn()} onCreated={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Filtered' } });
    fireEvent.change(screen.getByLabelText('Filter field'), { target: { value: 'status' } });
    fireEvent.change(screen.getByLabelText('Value type'), { target: { value: type } });
    fireEvent.change(screen.getByLabelText('Filter value'), { target: { value: input } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(body).toBeDefined());
    const definition = body!.definition as { filter: { value: unknown } };
    expect(stringifyJSON(definition.filter.value)).toBe(literal);
  });
});
