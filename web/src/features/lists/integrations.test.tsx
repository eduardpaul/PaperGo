// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ListIntegrations } from './integrations';
import { parseJSON } from '@/api/json';

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals(); });
it('adds HTTPS iCalendar configuration to the existing list and keeps replacement URLs blank', async () => {
  const calls: { path: string; method: string; body: Record<string, unknown> }[] = [];
  const source = { id: 'source', kind: 'ical_https', name: 'Work calendar', paused: false, version: 1 };
  let saved = false;
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL, init: RequestInit = {}) => {
    const url = new URL(String(input));
    calls.push({ path: url.pathname, method: init.method ?? 'GET', body: init.body ? parseJSON(String(init.body)) as Record<string, unknown> : {} });
    if (init.method === 'POST') { saved = true; return new Response(JSON.stringify(source), { status: 201 }); }
    return new Response(JSON.stringify(saved ? [source] : []));
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); clients.push(client);
  render(<QueryClientProvider client={client}><ListIntegrations listId="existing-list" canManage /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole('button', { name: 'Add integration' }));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Work calendar' } });
  fireEvent.change(screen.getByLabelText('HTTPS iCalendar URL'), { target: { value: 'https://example.com/private-feed' } });
  fireEvent.submit(screen.getByLabelText('Name').closest('form')!);
  await screen.findByText('Work calendar');
  expect(calls.find((call) => call.method === 'POST')).toEqual({ path: '/v1/resources/existing-list/integrations', method: 'POST', body: { kind: 'ical_https', name: 'Work calendar', configuration: { url: 'https://example.com/private-feed' } } });
  fireEvent.click(screen.getByRole('button', { name: 'Configure' }));
  expect((screen.getByLabelText('Replace URL (optional)') as HTMLInputElement).value).toBe('');
  await waitFor(() => expect(screen.queryByDisplayValue('https://example.com/private-feed')).toBeNull());
});
