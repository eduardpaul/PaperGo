// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { setLanguage } from '@/i18n';
import { hostEntry, TrustedHostsPage } from './trusted-hosts';

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await setLanguage('en');
  localStorage.clear();
});

it('shows administration in Spanish, plurals and markup included', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string) => {
      const path = new URL(String(input), window.location.href).pathname;
      if (path === '/v1/identity/me') return new Response(JSON.stringify({ subject: 'admin', scopes: ['organization.manage'] }));
      if (path === '/v1/organization/settings/trusted-hosts/usage')
        return new Response(JSON.stringify({ value: [{ host: 'calendar.example.com', integrations: 1 }] }));
      return new Response(JSON.stringify({ hosts: ['*', 'calendar.example.com'], version: 1 }));
    }),
  );
  await act(() => setLanguage('es'));
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TrustedHostsPage />
    </QueryClientProvider>,
  );
  expect(await screen.findByText('Hosts de confianza')).toBeTruthy();
  expect(await screen.findAllByText('Lo usa 1 integración')).toHaveLength(2);
  expect(screen.getByText(/confía en todos los hosts públicos/)).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Quitar calendar.example.com' })).toBeTruthy();
  expect(hostEntry('10.0.0.1').problem).toMatch(/No se pueden añadir direcciones IP/);
});
