// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { meQuery, preferencesQuery, resourceQuery } from '@/api/queries';
import { AppFrame } from './app-frame';
import type { App } from './api';

const { listeners, navigate } = vi.hoisted(() => ({ listeners: new Set<(change: unknown) => void>(), navigate: () => undefined }));
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }));
vi.mock('@/lib/live', () => ({
  onItemChange: (listener: (change: unknown) => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
}));
vi.mock('@sdk/app-runtime.js?raw', () => ({ default: 'runtime' }));

const app = { id: 'a1', workspace_id: 'w1', key: 'k', name: 'Tracker', current_version: 2, parameters: {} } as unknown as App;

afterEach(() => {
  cleanup();
  listeners.clear();
  document.documentElement.classList.remove('dark');
});

// Starts the frame and connects to it as its runtime would, and returns what
// the page says over the port.
async function started() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(meQuery.queryKey, { subject: 'bob', display_name: 'Bob' } as never);
  client.setQueryData(preferencesQuery.queryKey, { language: 'en', time_zone: 'UTC', date_format: 'yyyy-MM-dd', time_format: '24h', number_format: 'en' } as never);
  client.setQueryData(resourceQuery('w1').queryKey, { id: 'w1', name: 'Farm', capabilities: [] } as never);
  client.setQueryData(['app-bundle', 'a1', 2], 'bundle');
  const { container } = render(
    <QueryClientProvider client={client}>
      <AppFrame app={app} />
    </QueryClientProvider>,
  );
  const frame = container.querySelector('iframe') as HTMLIFrameElement;
  await waitFor(() => expect(frame.srcdoc).toContain('bundle'));
  const frameWindow = frame.contentWindow as Window;
  const port = new Promise<MessagePort>((resolve) => {
    vi.spyOn(frameWindow, 'postMessage').mockImplementation(((_message: unknown, _origin: string, transfer: MessagePort[]) => resolve(transfer[0] as MessagePort)) as never);
  });
  window.dispatchEvent(new MessageEvent('message', { source: frameWindow, data: { type: 'papergo:hello' } }));
  const messages: { type: string; theme?: string; changes?: { id: string }[] }[] = [];
  const inner = await port;
  inner.onmessage = (event) => messages.push(event.data);
  return { frame, messages, client };
}

it('follows the theme over the port, without restarting the app', async () => {
  const { frame, messages, client } = await started();
  const start = frame.srcdoc;
  expect(start).toContain('"theme":"light"');
  await waitFor(() => expect(messages).toContainEqual({ type: 'papergo:theme', theme: 'light' }));
  document.documentElement.classList.add('dark');
  await waitFor(() => expect(messages).toContainEqual({ type: 'papergo:theme', theme: 'dark' }));
  // A later render, such as a refetch's, finds the same start.
  client.setQueryData(resourceQuery('w1').queryKey, { id: 'w1', name: 'Farm', capabilities: [] } as never);
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(frame.srcdoc).toBe(start);
  client.clear();
});

it('tells the app of changes of its own workspace, and not of others', async () => {
  const { messages, client } = await started();
  for (const listener of listeners) {
    listener({ id: 'elsewhere', workspaceId: 'w2', collectionId: 'l9', removed: false });
    listener({ id: 'here', workspaceId: 'w1', collectionId: 'l1', removed: false });
  }
  await waitFor(() => expect(messages.some((m) => m.type === 'papergo:change')).toBe(true));
  const told = messages.filter((m) => m.type === 'papergo:change').flatMap((m) => m.changes ?? []);
  expect(told.map((c) => c.id)).toEqual(['here']);
  client.clear();
});
