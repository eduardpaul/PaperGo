// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import type { SearchContentType, Workflow } from '@/api/types';
import { automaticSummary, triggerLabel } from './presentation';
import { SchemaFields } from './schema-form';

const types: SearchContentType[] = [
  { id: 'record', key: 'record', name: 'Record' },
  { id: 'invoice', key: 'invoice', name: 'Invoice', parent_id: 'record' },
];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('names the content type a trigger is limited to', () => {
  expect(triggerLabel({ type: 'item.created', content_type_id: 'invoice' }, types)).toBe('When an item is created (Invoice or a subtype)');
  expect(triggerLabel({ type: 'item.created', content_type_id: 'gone' }, types)).toBe('When an item is created (selected content type)');
  expect(triggerLabel({ type: 'item.created' }, types)).toBe('When an item is created');
  const workflow = {
    state: 'published',
    automatic_enabled: true,
    definition: { triggers: [{ type: 'item.created', content_type_id: 'record' }] },
  } as unknown as Workflow;
  expect(automaticSummary(workflow, [], types)).toBe('On · When an item is created (Record or a subtype)');
});

it('picks a content type of the workspace by its place in the tree', async () => {
  const fetch = vi.fn(async (url: URL | string) => {
    const path = new URL(String(url), window.location.origin);
    return new Response(stringifyJSON(path.pathname === '/v1/search/content-types' && path.searchParams.get('workspace_id') === 'workspace' ? types : []));
  });
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  Element.prototype.scrollIntoView ??= () => {};
  let data: Record<string, unknown> = {};
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <SchemaFields
        schema={{ type: 'object', properties: { content_type: { type: 'string', title: 'Content type', 'x-papergo': { kind: 'content_type' } } } }}
        data={data}
        onChange={(next) => (data = next)}
        workspaceId="workspace"
      />
    </QueryClientProvider>,
  );
  fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
  fireEvent.change(await screen.findByPlaceholderText('Search…'), { target: { value: 'inv' } });
  fireEvent.click(await screen.findByRole('option', { name: 'Record › Invoice' }));
  expect(data).toEqual({ content_type: 'invoice' });
  client.clear();
});
