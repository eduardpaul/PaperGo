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

it.each([['EUR', 'EUR'], ['', 'GBP']])('preselects currency using workspace=%s and organization defaults', async (workspaceCurrency, expected) => {
  const fetch = vi.fn(async (input: URL | string, init?: RequestInit) => {
    const url = new URL(String(input));
    const body = url.pathname === '/v1/resources/w' ? { default_currency: workspaceCurrency } : url.pathname === '/v1/organization/preferences' ? { default_currency: 'GBP' } : {};
    return new Response(JSON.stringify(init?.method === 'POST' ? {} : body));
  });
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><FieldDialog collection={{ id: 'collection', workspace_id: 'w', kind: 'list', name: 'List', version: 9 }} onClose={vi.fn()} /></QueryClientProvider>);
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Price' } });
  fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'currency' } });
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Currency' }).textContent).toContain(expected));
  fireEvent.click(screen.getByRole('button', { name: 'Save field' }));
  await waitFor(() => expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(true));
  const [, init] = fetch.mock.calls.find(([, init]) => init?.method === 'POST')!;
  expect(JSON.parse(String(init?.body)).options.currency_code).toBe(expected);
  client.clear();
});
