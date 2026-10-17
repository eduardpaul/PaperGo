// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { stringifyJSON } from '@/api/json';
import { KeywordInput } from './keyword-input';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function Harness({ initial, onChange }: { initial: string[]; onChange: (keywords: string[]) => void }) {
  const [keywords, setKeywords] = useState(initial);
  return (
    <KeywordInput
      id="keywords"
      value={keywords}
      onChange={(next) => {
        setKeywords(next);
        onChange(next);
      }}
    />
  );
}

it('adds keywords with Enter, commas and suggestions and removes them with Backspace', async () => {
  const fetch = vi.fn(async (input: URL | string) => {
    const url = new URL(String(input), window.location.origin);
    if (url.pathname === '/v1/terms') return new Response(stringifyJSON({ value: [{ id: '2026', name: '2026' }] }));
    expect(url.pathname).toBe('/v1/keywords');
    return new Response(stringifyJSON({ value: [{ id: 'invoice', name: 'invoice' }, { id: 'urgent', name: 'urgent' }] }), {
      headers: { 'Content-Type': 'application/json' },
    });
  });
  vi.stubGlobal('fetch', fetch);
  const onChange = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <Harness initial={['2026']} onChange={onChange} />
    </QueryClientProvider>,
  );
  const input = screen.getByRole('combobox');
  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: 'finance' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(onChange).toHaveBeenLastCalledWith(['2026', 'finance']);
  fireEvent.change(input, { target: { value: 'q3,' } });
  expect(onChange).toHaveBeenLastCalledWith(['2026', 'finance', 'q3']);
  fireEvent.click(await screen.findByRole('option', { name: 'invoice' }));
  expect(onChange).toHaveBeenLastCalledWith(['2026', 'finance', 'q3', 'invoice']);
  fireEvent.keyDown(input, { key: 'Backspace' });
  expect(onChange).toHaveBeenLastCalledWith(['2026', 'finance', 'q3']);
  fireEvent.click(screen.getByRole('button', { name: 'Remove keyword 2026' }));
  expect(onChange).toHaveBeenLastCalledWith(['finance', 'q3']);
  fireEvent.change(input, { target: { value: 'finance' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(onChange).toHaveBeenCalledTimes(5);
  client.clear();
});
