// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Principals } from './principals';
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each(['groups/team/members', 'roles/reader/assignments'])('uses principal-keyed writes for %s', async (endpoint) => {
  const path = `/v1/identity/${endpoint}`;
  const fetch = vi.fn(async (url: URL, init: RequestInit = {}) => {
    if (init.method === 'PUT') return new Response(null, { status: 204 });
    return new Response(
      JSON.stringify(
        url.pathname === '/v1/identity/me' ? { subject: 'admin', scopes: ['user.read', 'group.read'] } : [],
      ),
    );
  });
  vi.stubGlobal('fetch', fetch);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <Principals path={path} canEdit />
    </QueryClientProvider>,
  );
  fireEvent.change(await screen.findByLabelText('Principal identifier'), { target: { value: 'issuer:user/name' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add people or groups' }));
  await waitFor(() =>
    expect(
      fetch.mock.calls.some(
        ([url, init]) => url.pathname === `${path}/user/issuer%3Auser%2Fname` && init?.method === 'PUT',
      ),
    ).toBe(true),
  );
});
