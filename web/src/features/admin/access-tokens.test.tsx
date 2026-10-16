// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { AccessTokens } from './access-tokens';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it('issues a scoped service credential, shows its secret once and revokes it', async () => {
  let issued = false, revoked = false;
  const calls: { method: string; path: string; body: unknown }[] = [];
  const token = { id: 'token', name: 'Calendar sync', subject: 'service:calendar', integration: true, created_by: 'admin', prefix: 'pgo_short', scopes: ['list.read'], created_at: '2026-10-16T00:00:00Z', expires_at: null };
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string, init: RequestInit = {}) => {
    const path = new URL(String(input)).pathname, method = init.method ?? 'GET';
    calls.push({ method, path, body: init.body ? JSON.parse(String(init.body)) : undefined });
    let data: unknown = [];
    if (path === '/v1/identity/me') data = { subject: 'admin', scopes: ['organization.manage'] };
    if (path === '/v1/identity/scopes') data = [{ name: 'list.read' }, { name: 'list.write' }];
    if (path === '/v1/identity/me/scopes') data = ['list.read'];
    if (path === '/v1/identity/tokens' && method === 'POST') { issued = true; data = { token, secret: 'pgo_one_time_secret' }; }
    if (path === '/v1/identity/tokens' && method === 'GET') data = issued ? [{ ...token, revoked_at: revoked ? '2026-10-16T12:00:00Z' : undefined }] : [];
    if (method === 'DELETE') { revoked = true; return new Response(null, { status: 204 }); }
    return new Response(JSON.stringify(data));
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><AccessTokens /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'New integration token' }));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Calendar sync' } });
  fireEvent.change(screen.getByLabelText('Expiration'), { target: { value: '0' } });
  fireEvent.click(await screen.findByLabelText('list.read'));
  expect(screen.queryByLabelText('list.write')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
  expect(await screen.findByDisplayValue('pgo_one_time_secret')).toBeTruthy();
  expect(calls.find(call => call.method === 'POST')?.body).toEqual({ name: 'Calendar sync', scopes: ['list.read'], expires_in_days: 0 });
  fireEvent.click(screen.getByRole('button', { name: 'Done' }));
  expect(screen.queryByDisplayValue('pgo_one_time_secret')).toBeNull();
  expect(await screen.findByText('Never expires', { exact: false })).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
  fireEvent.click(screen.getByRole('button', { name: 'Revoke token' }));
  await waitFor(() => expect(calls.some(call => call.method === 'DELETE' && call.path.endsWith('/token'))).toBe(true));
  expect(await screen.findByText('Revoked')).toBeTruthy();
  client.clear();
});
it('keeps token APIs unavailable in the administration screen for non-managers', async () => {
  const fetch = vi.fn(async (_input: URL | string) => new Response(JSON.stringify({ subject: 'reader', scopes: ['list.read'] })));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><AccessTokens /></QueryClientProvider>);
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  expect(screen.getByText('Organization management permission is required.')).toBeTruthy();
  expect(fetch.mock.calls.every(call => !String(call[0]).includes('/identity/tokens'))).toBe(true);
  client.clear();
});
