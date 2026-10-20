// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { Workflow } from '@/api/types';
import { WorkflowEditor } from './workflow-editor';

// Monaco needs a browser; the editor's text area stands in.
vi.mock('./monaco', () => new Promise(() => {}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const draft: Workflow = {
  id: 'workflow', key: 'draft', workspace_id: 'workspace', name: 'Draft', description: '',
  automatic_enabled: false, manual_enabled: true, state: 'draft', automatic_provider: false,
  version: 1, current_version: 1, parameters: {},
  source: 'export default workflow({ run() {} });',
  definition: { triggers: [{ type: 'item.created', collection_id: 'list' }] },
};

it('saves a changed script before running it as a test, and shows the run', async () => {
  const fetch = vi.fn(async (url: URL | string, options?: RequestInit) => {
    const path = new URL(String(url)).pathname;
    if (path === '/v1/workflows/workflow' && options?.method === 'PUT') return new Response(stringifyJSON({ ...draft, ...parseJSON(String(options.body)) as object, version: 2, current_version: 2 }));
    if (path === '/v1/workflows/workflow/test-runs') return new Response(stringifyJSON({ run_id: 'run' }), { status: 202 });
    if (path === '/v1/workflow-runs/run') return new Response(stringifyJSON({ id: 'run', workflow_id: 'workflow', status: 'completed', event_type: 'test', created_at: '2026-10-20T10:00:00Z', result: { ok: true },
      steps: [{ seq: 0, kind: 'request', name: 'GET /v1/resources/first', status: 'completed' }], logs: [{ level: 'info', message: 'tested', at: '2026-10-20T10:00:00Z' }] }));
    return new Response(stringifyJSON({ value: [{ id: 'first', name: 'First', kind: 'item' }] }));
  });
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><WorkflowEditor workspaceId="workspace" workflow={draft} canManage onClose={vi.fn()} /></QueryClientProvider>);
  expect(screen.getByRole('button', { name: 'Run test' })).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Script'), { target: { value: 'export default workflow({ run() { return { ok: true }; } });' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save and run test' }));
  expect(await screen.findByText(/GET \/v1\/resources\/first/)).toBeTruthy();
  expect(screen.getByText(/tested/)).toBeTruthy();
  const calls = fetch.mock.calls.map(([url, options]) => `${options?.method ?? 'GET'} ${new URL(String(url)).pathname}`);
  expect(calls.indexOf('PUT /v1/workflows/workflow')).toBeLessThan(calls.indexOf('POST /v1/workflows/workflow/test-runs'));
  const put = fetch.mock.calls.find(([, options]) => options?.method === 'PUT');
  expect(parseJSON(String(put?.[1]?.body))).toMatchObject({ source: 'export default workflow({ run() { return { ok: true }; } });', state: 'draft' });
  expect(screen.getByRole('button', { name: 'Run test' })).toBeTruthy();
  client.clear();
});
