// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { parseJSON, stringifyJSON } from '@/api/json';
import type { BuiltInState, Workflow } from '@/api/types';
import { CollectionSearchSettings } from './search-settings';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// A list turns search off from its settings: the PUT keeps the built-in's
// other settings and names the version it read.
it('turns search off for a list, keeping the rest of the built-in', async () => {
  const workflow: Workflow = { id: 'w1', collection_id: 'diesel', workspace_id: 'ws', key: 'search-index-diesel', name: 'Index for search', description: '', automatic_enabled: true, manual_enabled: false, state: 'published', automatic_provider: true, current_version: 1, source: '', definition: { triggers: [{ type: 'item.created' }, { type: 'term.changed', disabled: true }] }, version: 4, parameters: {} };
  const builtin: BuiltInState = { key: 'search.index', name: 'Index for search', description: '', role: 'search.index', default: true, system: true, workflows: [workflow] };
  const writes: { match?: string; body: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const url = new URL(String(input), window.location.origin);
      let result: unknown = { value: [] };
      if (init.method === 'PUT') {
        writes.push({ match: (init.headers as Record<string, string>)['If-Match'], body: parseJSON(String(init.body)) as Record<string, unknown> });
        result = { ...workflow, automatic_enabled: false, version: 5 };
      } else if (url.pathname === '/v1/workflow-catalog') result = { triggers: [], builtins: [], roles: [{ key: 'search.index', name: 'Search index' }] };
      else if (url.pathname.endsWith('/workflow-builtins')) result = { value: [builtin] };
      return new Response(stringifyJSON(result), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <CollectionSearchSettings workspaceId="ws" collectionId="diesel" canManage />
    </QueryClientProvider>,
  );
  const picker = (await screen.findByLabelText('Search the items')) as HTMLSelectElement;
  expect(picker.value).toBe('on');
  const save = screen.getByRole('button', { name: 'Save' });
  expect(save).toHaveProperty('disabled', true);
  fireEvent.change(picker, { target: { value: 'off' } });
  expect(screen.getByText(/matches names only/)).toBeTruthy();
  fireEvent.click(save);
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].match).toBe('"4"');
  expect(writes[0].body).toEqual({ collection_id: 'diesel', automatic_enabled: false, manual_enabled: false, state: 'published', disabled_triggers: ['term.changed'], parameters: {} });
  client.clear();
});

// A published copy of the built-in can fill the role: the setting shows it,
// and offers no choice that would turn it off by turning the built-in on.
it('names a workflow that keeps search in place of the built-in', async () => {
  const copy: Workflow = { id: 'w2', collection_id: 'diesel', workspace_id: 'ws', key: 'my-index', name: 'My index', description: '', automatic_enabled: true, manual_enabled: false, state: 'published', automatic_provider: true, current_version: 1, source: '', definition: { triggers: [] }, version: 1, parameters: {} };
  const paused: Workflow = { ...copy, id: 'w1', key: 'search-index-diesel', name: 'Index for search', builtin_key: 'search.index', automatic_enabled: false };
  const builtin: BuiltInState = { key: 'search.index', name: 'Index for search', description: '', role: 'search.index', default: true, system: true, workflows: [paused] };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string) => {
      const url = new URL(String(input), window.location.origin);
      let result: unknown = { value: [] };
      if (url.pathname === '/v1/workflow-catalog') result = { triggers: [], builtins: [], roles: [{ key: 'search.index', name: 'Search index' }] };
      else if (url.pathname.endsWith('/workflow-builtins')) result = { value: [builtin] };
      else if (url.pathname.endsWith('/workflow-roles')) result = { value: [{ role: { key: 'search.index' }, scope: 'collection', automatic_enabled: true, workflow: copy }] };
      return new Response(stringifyJSON(result), { headers: { 'Content-Type': 'application/json' } });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <CollectionSearchSettings workspaceId="ws" collectionId="diesel" canManage />
    </QueryClientProvider>,
  );
  const picker = (await screen.findByLabelText('Search the items')) as HTMLSelectElement;
  expect(picker.value).toBe('on');
  expect(picker.disabled).toBe(true);
  expect(screen.getByText(/"My index" keeps this list's search/)).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
  client.clear();
});
