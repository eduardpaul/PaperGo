// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ComponentType } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { Route } from './webdav';

vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options }),
}));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each([
  ['list.read', 'document.read'],
  ['list.read', 'list.write', 'document.read', 'document.write'],
])('issues a WebDAV token capped to account scopes %j', async (...granted) => {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: URL, init: RequestInit = {}) => {
    calls.push({ method: init.method ?? 'GET', path: input.pathname, body: init.body ? JSON.parse(String(init.body)) : undefined });
    const data = input.pathname.endsWith('/scopes') ? [...granted, 'workspace.manage']
      : init.method === 'POST' ? { token: { id: 'token' }, secret: 'pgo_webdav_secret' } : [];
    return new Response(JSON.stringify(data));
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const Page = Route.options.component as ComponentType;
  render(<QueryClientProvider client={client}><Page /></QueryClientProvider>);
  fireEvent.change(screen.getByLabelText('Label'), { target: { value: 'Explorer' } });
  const create = screen.getByRole('button', { name: 'Create token' }) as HTMLButtonElement;
  await waitFor(() => expect(create.disabled).toBe(false));
  fireEvent.change(screen.getByLabelText('Expires in days'), { target: { value: '0' } });
  expect(create.disabled).toBe(true);
  fireEvent.change(screen.getByLabelText('Expires in days'), { target: { value: '30' } });
  fireEvent.click(create);
  expect(await screen.findByDisplayValue('pgo_webdav_secret')).toBeTruthy();
  expect(calls.find(call => call.method === 'POST')).toEqual({
    method: 'POST', path: '/v1/identity/me/tokens',
    body: { name: 'Explorer', scopes: granted, expires_in_days: 30 },
  });
  client.clear();
});
