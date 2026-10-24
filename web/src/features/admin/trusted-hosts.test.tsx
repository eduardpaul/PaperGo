// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { matches, TrustedLink, trusts } from '@/features/files/trust';
import { hostEntry, TrustedHostsPage } from './trusted-hosts';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('stores entries in one form and refuses what cannot be trusted', () => {
  expect(hostEntry(' Docs.Example.COM. ')).toEqual({ entry: 'docs.example.com' });
  expect(hostEntry('*.SharePoint.com')).toEqual({ entry: '*.sharepoint.com' });
  expect(hostEntry('*')).toEqual({ entry: '*' });
  expect(hostEntry('bücher.example').entry).toBe('xn--bcher-kva.example');
  for (const bad of ['https://example.com', 'example.com:8443', '10.0.0.1', 'a.*.example.com', 'localhost']) expect(hostEntry(bad).problem).toBeTruthy();
});

it('trusts listed hosts, subdomains of wildcards and this app, and nothing else', () => {
  const hosts = ['docs.example.com', '*.sharepoint.com'];
  expect(matches(hosts, 'docs.example.com')).toBe(true);
  expect(matches(hosts, 'contoso.sharepoint.com')).toBe(true);
  expect(matches(hosts, 'sharepoint.com')).toBe(false);
  expect(matches(hosts, 'evilsharepoint.com')).toBe(false);
  expect(matches(['*'], '127.0.0.1')).toBe(false);
  expect(trusts(hosts, 'https://docs.example.com/a')).toBe(true);
  expect(trusts(hosts, 'javascript:alert(1)')).toBe(false);
  expect(trusts([], `${window.location.origin}/w/1`)).toBe(true);
  expect(trusts(undefined, 'https://docs.example.com/a')).toBe(false);
});

function serve(hosts: string[], scopes: string[] = []) {
  const calls: { method: string; path: string; body: unknown; ifMatch?: string }[] = [];
  let current = { hosts, version: 3 };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const path = new URL(String(input), window.location.href).pathname;
      const method = init.method ?? 'GET';
      const body = init.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body, ifMatch: new Headers(init.headers).get('If-Match') ?? undefined });
      let data: unknown = {};
      if (path === '/v1/identity/me') data = { subject: 'admin', scopes };
      if (path === '/v1/organization/settings/trusted-hosts' && method === 'PUT') current = { hosts: body.hosts, version: current.version + 1 };
      if (path === '/v1/organization/settings/trusted-hosts') data = current;
      if (path === '/v1/organization/settings/trusted-hosts/usage') data = { value: [{ host: 'calendar.example.com', integrations: 2 }] };
      return new Response(JSON.stringify(data));
    }),
  );
  return calls;
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

it('asks before a link leaves for an untrusted host', async () => {
  serve(['docs.example.com']);
  render(
    <QueryClientProvider client={client()}>
      <TrustedLink href="https://elsewhere.example/page">elsewhere</TrustedLink>
      <TrustedLink href="https://docs.example.com/page">docs</TrustedLink>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText('elsewhere'));
  expect(await screen.findByText("You're leaving PaperGo for elsewhere.example")).toBeTruthy();
  expect(screen.getByText('https://elsewhere.example/page')).toBeTruthy();
  expect(screen.queryByRole('button', { name: 'Trust elsewhere.example' })).toBeNull();
});

it('adds and removes entries with the list version, naming the integrations a removal stops', async () => {
  const calls = serve(['*', 'calendar.example.com'], ['organization.manage']);
  render(
    <QueryClientProvider client={client()}>
      <TrustedHostsPage />
    </QueryClientProvider>,
  );
  expect(await screen.findByText('trusts every public host.', { exact: false })).toBeTruthy();
  expect(await screen.findAllByText('Used by 2 integrations')).toHaveLength(2);
  fireEvent.change(screen.getByLabelText('Host to trust'), { target: { value: 'Docs.Example.com' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(calls.find((call) => call.method === 'PUT')?.body).toEqual({ hosts: ['*', 'calendar.example.com', 'docs.example.com'] }));
  expect(calls.find((call) => call.method === 'PUT')?.ifMatch).toBe('"3"');
  // * still trusts the calendar host, so removing it stops nothing.
  fireEvent.click(await screen.findByRole('button', { name: 'Remove *' }));
  await waitFor(() => expect(calls.filter((call) => call.method === 'PUT')).toHaveLength(2));
  fireEvent.click(await screen.findByRole('button', { name: 'Remove calendar.example.com' }));
  expect(await screen.findByText('2 integrations on calendar.example.com will stop refreshing', { exact: false })).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
  await waitFor(() => expect(calls.filter((call) => call.method === 'PUT').at(-1)?.body).toEqual({ hosts: ['docs.example.com'] }));
});
