// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { SmartFolderDialog, smartQueryFields } from './editor-dialog';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('builds typed rules from labels, previews matches and saves workspace scope and mode', async () => {
  const requests: { path: string; method: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const path = new URL(String(input)).pathname;
    const body = init.body ? JSON.parse(String(init.body)) : {};
    requests.push({ path, method: init.method ?? 'GET', body });
    let result: unknown = { value: [] };
    if (path === '/v1/identity/me') result = { subject: 'alice', scopes: ['list.read', 'list.write'] };
    if (path === '/v1/workspaces') result = { value: [{ id: 'w', name: 'Finance', kind: 'workspace' }] };
    if (path === '/v1/resources/w') result = { id: 'w', access: ['read', 'manage'] };
    if (path === '/v1/search/fields') result = { value: [{ key: 'status_key', label: 'Approval status', type: 'choice', choices: ['Open', 'Closed'], queryable: true }, { key: 'private_key', label: 'Unindexed field', type: 'text', queryable: false }] };
    if (path === '/v1/search/content-types' || path === '/v1/collection-templates') result = [];
    if (path === '/v1/smart-folders/preview') result = { total: body.definition?.filter?.value === 'Closed' ? 1 : 2, value: [{ item: { id: 'i', name: 'Matched invoice' }, collection_name: 'Invoices' }] };
    if (path === '/v1/smart-folders') result = { ...body, id: 'saved', owner_id: 'alice', version: 1 };
    return new Response(JSON.stringify(result));
  }));
  const saved = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><SmartFolderDialog open onClose={vi.fn()} onSaved={saved} /></QueryClientProvider>);
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Closed invoices' } });
  await screen.findByRole('option', { name: 'Finance' }, { timeout: 8_000 });
  fireEvent.change(screen.getByLabelText('Search in'), { target: { value: 'w' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add filter' }));
  await screen.findByRole('option', { name: 'Approval status' }, { timeout: 8_000 });
  expect(screen.queryByRole('option', { name: 'Unindexed field' })).toBeNull();
  expect(screen.queryByLabelText('Value type')).toBeNull();
  fireEvent.change(screen.getByLabelText('Filter field'), { target: { value: 'status_key' } });
  expect(screen.getByRole('option', { name: 'is' })).toBeTruthy();
  fireEvent.change(screen.getByLabelText(/^Filter value/), { target: { value: 'Closed' } });
  expect(await screen.findByText('1 matching items', {}, { timeout: 8_000 })).toBeTruthy();
  expect(screen.getByText('Matched invoice')).toBeTruthy();
  await waitFor(() => expect((screen.getByRole('button', { name: 'Create' }) as HTMLButtonElement).disabled).toBe(false), { timeout: 8_000 });
  fireEvent.click(screen.getByRole('button', { name: 'Create' }));
  await waitFor(() => expect(saved).toHaveBeenCalled(), { timeout: 8_000 });
  const create = requests.find(request => request.path === '/v1/smart-folders' && request.method === 'POST')!;
  expect(create.body).toMatchObject({ personal: true, workspace_id: 'w', definition: { mode: 'query', filter: { field: 'status_key', op: 'eq', value: 'Closed' } } });
  expect(requests.filter(request => request.path === '/v1/smart-folders')).toHaveLength(1);
  client.clear();
}, 20_000);
it('omits fields with incompatible shapes while keeping built-in rules', () => {
  const fields = smartQueryFields([{ key: 'status', label: 'Status', type: 'choice', queryable: true }, { key: 'status', label: 'Status', type: 'integer', queryable: true }]);
  expect(fields.some(field => field.key === 'status')).toBe(false);
  expect(fields.some(field => field.key === '$modified_at')).toBe(true);
  expect(fields.some(field => field.key === '$file_type')).toBe(true);
});
