// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { AddToSmartFolder } from './classification';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it('classifies using both the item version and selected smart folder version', async () => {
  const fetch = vi.fn(
    async (_url: URL | string, options?: RequestInit) =>
      new Response(
        JSON.stringify(
          options?.method === 'POST'
            ? {}
            : {
                value: [
                  { id: 'folder', name: 'Invoices', workspace_id: 'w', version: 8 },
                  { id: 'foreign', name: 'Other workspace', workspace_id: 'other', version: 1 },
                ],
              },
        ),
      ),
  );
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <AddToSmartFolder
        item={{ id: 'item', workspace_id: 'w', container_id: 'library', kind: 'item', name: 'Invoice', version: 4 }}
        onClose={vi.fn()}
      />
    </QueryClientProvider>,
  );
  await screen.findByRole('option', { name: 'Invoices' });
  expect(screen.queryByRole('option', { name: 'Other workspace' })).toBeNull();
  fireEvent.change(screen.getByLabelText('Smart folder'), { target: { value: 'folder' } });
  fireEvent.click(screen.getByRole('button', { name: 'Classify item' }));
  await waitFor(() => expect(fetch.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(true));
  const post = fetch.mock.calls.find(([, options]) => options?.method === 'POST');
  expect(String(post?.[0])).toContain('/v1/smart-folders/folder/items');
  expect(JSON.parse(String(post?.[1]?.body))).toEqual({
    folder_version: 8,
    collection_id: 'library',
    item_id: 'item',
    version: 4,
  });
  client.clear();
});
