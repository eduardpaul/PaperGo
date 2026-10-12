// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type * as Router from '@tanstack/react-router';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Suspense } from 'react';
import type { ComponentType } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { Route } from '@/routes/_app/admin/users';

vi.mock('@tanstack/react-router', async (original) => ({
  ...(await original<typeof Router>()),
  createFileRoute: () => (options: unknown) => ({ options }),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('finds a disabled account by encoded subject and reactivates it through the status API', async () => {
  const subject = 'issuer:user/name';
  const user = { subject, display_name: 'Disabled user', email: '', active: false };
  const fetch = vi.fn(async (input: URL, init: RequestInit = {}) => {
    if (input.pathname === '/v1/identity/me')
      return new Response(JSON.stringify({ subject: 'admin', scopes: ['user.read', 'user.manage'] }));
    if (input.pathname === '/v1/identity/users') return new Response(JSON.stringify(user.active ? [user] : []));
    if (init.method === 'PUT') {
      user.active = true;
      return new Response(JSON.stringify({ subject, active: true }));
    }
    return new Response(JSON.stringify(user));
  });
  vi.stubGlobal('fetch', fetch);
  const Page = Route.options.component as ComponentType;
  await act(async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
        <Suspense>
          <Page />
        </Suspense>
      </QueryClientProvider>,
    );
  });
  fireEvent.change(await screen.findByLabelText('Account subject'), { target: { value: subject } });
  fireEvent.click(screen.getByRole('button', { name: /^Find$/ }));
  await screen.findByRole('button', { name: 'Activate account' });
  expect(fetch.mock.calls.some(([url]) => url.pathname === '/v1/identity/users/issuer%3Auser%2Fname')).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Activate account' }));
  fireEvent.click(await screen.findByRole('button', { name: /^Activate$/ }));
  await waitFor(() =>
    expect(
      fetch.mock.calls.some(([url, init]) => url.pathname.endsWith('/status') && init?.body === '{"active":true}'),
    ).toBe(true),
  );
});
