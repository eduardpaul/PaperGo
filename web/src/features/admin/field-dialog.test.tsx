// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { FieldDialog } from './field-dialog';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it('uses the collection ETag for a shared field edit and preserves an exact default', async () => {
  const fetch = vi.fn(async (_url: URL | string, _options?: RequestInit) => new Response('{}'));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <FieldDialog
        collection={{ id: 'collection', workspace_id: 'w', kind: 'list', name: 'List', version: 9 }}
        field={{
          id: 'field',
          key: 'amount',
          label: 'Amount',
          type: 'integer',
          version: 2,
          options: { default_value: 1, description: 'Keep' },
        }}
        onClose={vi.fn()}
      />
    </QueryClientProvider>,
  );
  fireEvent.change(screen.getByLabelText('Default value'), { target: { value: '9007199254740993' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save field' }));
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  expect(String(fetch.mock.calls[0]?.[0])).toContain('/v1/resources/collection/fields/field');
  expect(fetch.mock.calls[0]?.[1]?.headers).toMatchObject({ 'If-Match': '"9"' });
  expect(fetch.mock.calls[0]?.[1]?.body).toContain('"default_value":9007199254740993');
  expect(fetch.mock.calls[0]?.[1]?.body).toContain('"description":"Keep"');
  client.clear();
});

it.each([false, true])('keeps existing choice cardinality immutable (multiple=%s)', (multiple) => {
  const client = new QueryClient();
  render(<QueryClientProvider client={client}><FieldDialog collection={{ id: 'collection', workspace_id: 'w', kind: 'list', name: 'List', version: 9 }} field={{ id: 'field', key: 'status', label: 'Status', type: 'choice', choices: ['open'], version: 1, options: { multiple } }} onClose={vi.fn()} /></QueryClientProvider>);
  const checkbox = screen.getByLabelText<HTMLInputElement>('Allow multiple values');
  expect(checkbox.disabled).toBe(true);
  expect(checkbox.checked).toBe(multiple);
  client.clear();
});
