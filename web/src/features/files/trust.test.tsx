// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type * as Auth from '@/api/auth';
import { RemoteImage, TrustedLink } from './trust';

vi.mock('@/api/auth', async (actual) => ({ ...(await actual<typeof Auth>()), authorizationHeaders: () => ({ Authorization: 'Bearer reader-token' }) }));

const requests: { path: string; authorization: string | null }[] = [];
beforeEach(() => {
  requests.length = 0;
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: () => 'blob:stored-image', revokeObjectURL: () => undefined }));
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = new URL(String(input), window.location.href).pathname;
      requests.push({ path, authorization: new Headers(init.headers).get('Authorization') });
      if (path === '/v1/organization/settings/trusted-hosts') return new Response(JSON.stringify({ hosts: ['docs.example.com'], version: 1 }));
      if (path === '/v1/identity/me') return new Response(JSON.stringify({ subject: 'reader', scopes: [] }));
      return new Response('png', { headers: { 'Content-Type': 'image/png' } });
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const wrap = (children: React.ReactNode) => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>);

it('gives an untrusted link no address of its own, so every way of opening it asks', async () => {
  wrap(
    <>
      <TrustedLink href="https://elsewhere.example/page">elsewhere</TrustedLink>
      <TrustedLink href="https://docs.example.com/page">docs</TrustedLink>
    </>,
  );
  await waitFor(() => expect(screen.getByText('docs').closest('a')?.getAttribute('href')).toBe('https://docs.example.com/page'));
  const untrusted = screen.getByText('elsewhere');
  expect(untrusted.closest('a')?.hasAttribute('href')).toBe(false);
  for (const open of [() => fireEvent.click(untrusted), () => fireEvent(untrusted, new MouseEvent('auxclick', { bubbles: true, button: 1 })), () => fireEvent.keyDown(untrusted, { key: 'Enter' })]) {
    open();
    expect(await screen.findByText("You're leaving PaperGo for elsewhere.example")).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByText("You're leaving PaperGo for elsewhere.example")).toBeNull());
  }
});

it('fetches images stored in PaperGo with the reader’s credentials', async () => {
  wrap(<RemoteImage src="/v1/items/i1/content" alt="Diagram" shown={false} onShow={() => undefined} />);
  expect(((await screen.findByAltText('Diagram')) as HTMLImageElement).src).toBe('blob:stored-image');
  expect(requests.find((request) => request.path === '/v1/items/i1/content')?.authorization).toBe('Bearer reader-token');
});
