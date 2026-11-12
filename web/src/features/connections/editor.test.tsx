// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { Connection } from './api';
import { channelUses, ConnectionEditor, settledChannel } from './editor';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function serve() {
  const calls: { method: string; path: string; body: Record<string, unknown>; ifMatch?: string }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: URL | string, init: RequestInit = {}) => {
      const path = new URL(String(input), window.location.href).pathname;
      const method = init.method ?? 'GET';
      const body = init.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body, ifMatch: new Headers(init.headers).get('If-Match') ?? undefined });
      if (path === '/v1/resources/w1/children') return new Response(JSON.stringify({ value: [{ id: 'l1', kind: 'list', name: 'Facilities' }, { id: 'f1', kind: 'folder', name: 'Not a list' }] }));
      if (path.startsWith('/v1/connections')) return new Response(JSON.stringify({ id: 'c1', name: body?.name, kind: 'mqtt', channels: [], status: { state: 'connecting' }, version: 1 }), { status: method === 'POST' ? 201 : 200 });
      return new Response('{}');
    }),
  );
  return calls;
}

const wrap = (node: React.ReactNode) => <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>;

it('says which uses a channel serves', () => {
  expect(channelUses('mqtt', { key: 'a', uses: ['control'] })).toEqual(['control']);
  expect(channelUses('http', { key: 'a', methods: ['GET', 'POST'] })).toEqual(['see', 'control']);
  expect(channelUses('http', { key: 'a', methods: ['HEAD'] })).toEqual(['see']);
});

it('drops what a channel no longer serves before saving', () => {
  const access = { see: { level: 'write' as const, on: 'workspace' }, control: { level: 'write' as const, on: 'l1' } };
  expect(settledChannel('mqtt', { key: 'a', topics: 'home/#', uses: ['see'], retain: true, access })).toEqual({ key: 'a', topics: 'home/#', uses: ['see'], access: { see: access.see } });
  expect(settledChannel('http', { key: 'a', path: '/x', methods: ['POST'], cache: '30s', access })).toEqual({ key: 'a', path: '/x', methods: ['POST'], access: { control: access.control } });
});

it('adds an MQTT connection with a channel whose control is borrowed from a list', async () => {
  const calls = serve();
  const saved = vi.fn();
  render(wrap(<ConnectionEditor workspaceId="w1" onClose={() => undefined} onSaved={saved} />));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Home broker' } });
  fireEvent.change(screen.getByLabelText('Address'), { target: { value: 'mqtt://192.168.1.20:1883' } });
  fireEvent.change(screen.getByLabelText('How it signs in'), { target: { value: 'basic' } });
  fireEvent.change(screen.getByLabelText('User name'), { target: { value: 'papergo' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add channel' }));
  fireEvent.change(screen.getAllByLabelText('Key')[1], { target: { value: 'heating' } });
  fireEvent.change(screen.getByLabelText('Topics'), { target: { value: 'boiler/#' } });
  fireEvent.click(screen.getByLabelText('Control (publish)'));
  // Only the workspace's lists and libraries lend their access.
  await waitFor(() => expect(screen.getAllByRole('option', { name: 'Facilities' })).toHaveLength(2));
  expect(screen.queryByRole('option', { name: 'Not a list' })).toBeNull();
  fireEvent.change(screen.getAllByLabelText('On')[1], { target: { value: 'l1' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  const post = calls.find((c) => c.method === 'POST');
  expect(post?.path).toBe('/v1/connections');
  expect(post?.body).toEqual({
    name: 'Home broker',
    enabled: true,
    target: 'mqtt://192.168.1.20:1883',
    tls: { ca: '' },
    credentials: { type: 'basic', username: 'papergo', password: 'secret' },
    channels: [{ key: 'heating', topics: 'boiler/#', uses: ['see', 'control'], access: { control: { level: 'write', on: 'l1' } } }],
    workspace_id: 'w1',
    kind: 'mqtt',
  });
});

it('keeps the saved credentials when an edit leaves them alone', async () => {
  const calls = serve();
  const connection = { id: 'c1', workspace_id: 'w1', key: 'home', name: 'Home', kind: 'mqtt', enabled: true, target: 'mqtt://10.0.0.2:1883', tls: {}, credentials: { type: 'basic', username: 'papergo' }, channels: [], status: { state: 'connected' }, version: 4 } as Connection;
  render(wrap(<ConnectionEditor workspaceId="w1" connection={connection} onClose={() => undefined} onSaved={() => undefined} />));
  expect(screen.getByText('The saved credentials are kept unless you change them.')).toBeTruthy();
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Home broker' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
  const put = calls.find((c) => c.method === 'PUT');
  expect(put?.path).toBe('/v1/connections/c1');
  expect(put?.ifMatch).toBe('"4"');
  expect(put?.body).not.toHaveProperty('credentials');
  expect(put?.body).not.toHaveProperty('kind');
});

it('starts another kind without the channels and credentials of the first', async () => {
  const calls = serve();
  const saved = vi.fn();
  render(wrap(<ConnectionEditor workspaceId="w1" onClose={() => undefined} onSaved={saved} />));
  fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'http' } });
  fireEvent.change(screen.getByLabelText('How it signs in'), { target: { value: 'bearer' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add channel' }));
  fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'mqtt' } });
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Home broker' } });
  fireEvent.change(screen.getByLabelText('Address'), { target: { value: 'mqtt://192.168.1.20:1883' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(saved).toHaveBeenCalled());
  const post = calls.find((c) => c.method === 'POST');
  expect(post?.body).toMatchObject({ kind: 'mqtt', channels: [], credentials: { type: 'none' } });
});
