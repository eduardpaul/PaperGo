// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { FilePreview } from './preview';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('shows a non-blocking pending state, renders a lightweight preview and never fetches original bytes', async () => {
  const create = vi.fn(() => 'blob:preview'), revoke = vi.fn();
  vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = revoke; });
  let ready = false;
  const fetch = vi.fn(async (_input: string) => ready ? new Response('webp', { headers: { 'Content-Type': 'image/webp' } }) : new Response(JSON.stringify({ status: 'pending' }), { status: 202, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><FilePreview itemId="item" version={1} alt="Document preview" /></QueryClientProvider>);
  expect((await screen.findByRole('status')).textContent).toContain('Generating preview');
  ready = true;
  await client.invalidateQueries({ queryKey: ['resources', 'item', 'preview'] });
  expect(await screen.findByRole('img', { name: 'Document preview' })).toBeTruthy();
  expect(create).toHaveBeenCalled();
  expect(fetch.mock.calls.every(call => call[0] === '/v1/items/item/preview')).toBe(true);
  view.unmount();
  expect(revoke).toHaveBeenCalledWith('blob:preview');
  client.clear();
});
it('uses a fresh request when the resource changes and displays unavailable without downloading the source', async () => {
  const fetch = vi.fn(async () => new Response(JSON.stringify({ status: 'unavailable' }), { headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><FilePreview itemId="item" version={1} alt="Preview" /></QueryClientProvider>);
  expect(await screen.findByText('Preview unavailable')).toBeTruthy();
  view.rerender(<QueryClientProvider client={client}><FilePreview itemId="item" version={2} alt="Preview" /></QueryClientProvider>);
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  client.clear();
});
