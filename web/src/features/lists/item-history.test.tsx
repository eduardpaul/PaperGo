// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { ItemSchemaView } from './item-history';

vi.mock('@tanstack/react-router', () => ({ Link: ({ children }: { children: ReactNode }) => <a>{children}</a> }));
vi.mock('@/features/fields/people', () => ({ usePeopleNames: () => new Map([['alice', 'Alice']]) }));
vi.mock('@/features/fields/names', () => ({ useTermNames: () => new Map([['term', 'Finance']]) }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

it('shows intrinsic properties and keywords without custom fields and follows the selected surface', async () => {
  const fetch = vi.fn(async (input: URL | string) => {
    const url = new URL(String(input));
    const published = url.searchParams.get('surface') === 'published';
    return new Response(JSON.stringify({ revision_number: 1, item: { id: 'a', name: published ? 'Published invoice' : 'Draft invoice', keywords: ['Review'], created_by: 'alice' }, file: { filename: 'invoice.pdf', size: 2048, content_type: 'application/pdf' }, definition: { fields: [] } }));
  });
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><ItemSchemaView itemId="a" /></QueryClientProvider>);
  expect(await screen.findByText('Draft invoice')).toBeTruthy();
  expect(screen.getByText('System properties')).toBeTruthy();
  expect(screen.getByText('Custom fields')).toBeTruthy();
  expect(screen.getByText('No custom fields defined.')).toBeTruthy();
  expect(screen.getByText('invoice.pdf')).toBeTruthy();
  expect(screen.getByText('Alice')).toBeTruthy();
  expect(screen.getByText('Review')).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Schema surface'), { target: { value: 'published' } });
  await waitFor(() => expect(screen.getByText('Published invoice')).toBeTruthy());
  expect(screen.queryByText('Draft invoice')).toBeNull();
  client.clear();
});
