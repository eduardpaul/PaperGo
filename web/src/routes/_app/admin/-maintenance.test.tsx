// @vitest-environment jsdom
import type * as Router from '@tanstack/react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PortabilityPage, Route } from './maintenance';

const navigate = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', async (original) => ({
  ...await original<typeof Router>(),
  useNavigate: () => navigate,
}));
vi.mock('@/features/admin/common', () => ({ useHasScope: () => true }));
vi.mock('@/features/admin/operation-progress', () => ({ OperationProgress: () => null }));
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); navigate.mockClear(); });

it('exports the selected workspace without data and sends import validation and name options', async () => {
  vi.spyOn(Route, 'useSearch').mockReturnValue({ workspace: 'finance' });
  const fetch = vi.fn(async (url: URL, options: RequestInit) => {
    if (url.pathname === '/v1/workspaces') return new Response(JSON.stringify({ value: [
      { id: 'finance', name: 'Finance' },
      { id: 'home', name: 'Personal Home', personal_owner_id: 'alice' },
    ] }));
    return new Response(JSON.stringify({ id: options.method === 'POST' ? 'operation' : 'unexpected' }));
  });
  vi.stubGlobal('fetch', fetch);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}><PortabilityPage /></QueryClientProvider>);
  await screen.findByRole('option', { name: 'Finance' });
  expect(screen.queryByRole('option', { name: 'Personal Home' })).toBeNull();
  fireEvent.click(screen.getByRole('checkbox'));
  fireEvent.click(screen.getByRole('button', { name: 'Export workspace' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => url.pathname === '/v1/portability/exports')).toBe(true));
  const exported = fetch.mock.calls.find(([url]) => url.pathname === '/v1/portability/exports')!;
  expect(JSON.parse(exported[1].body as string)).toEqual({ workspace_id: 'finance', include_data: false });
  const file = new File(['package'], 'workspace.zip', { type: 'application/zip' });
  fireEvent.change(screen.getByLabelText('Import a package'), { target: { files: [file] } });
  fireEvent.change(screen.getByLabelText('New workspace name (optional, workspace packages only)'), { target: { value: 'Finance copy' } });
  fireEvent.click(screen.getByRole('button', { name: 'Try it' }));
  await waitFor(() => expect(fetch.mock.calls.some(([url]) => url.pathname === '/v1/portability/imports')).toBe(true));
  const imported = fetch.mock.calls.find(([url]) => url.pathname === '/v1/portability/imports')!;
  expect(imported[0].searchParams.get('dry_run')).toBe('true');
  expect(imported[0].searchParams.get('workspace_name')).toBe('Finance copy');
  expect(imported[1].body).toBe(file);
  expect(imported[1].headers).toMatchObject({ 'Content-Type': 'application/zip' });
});
