// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { setLanguage } from '@/i18n';
import { PlaceContextMenu } from './place-context-menu';

const navigate = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }));
vi.mock('./new-collection-dialog', () => ({ NewCollectionDialog: () => null }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); navigate.mockReset(); });

it('routes collection shortcuts to the same permission-checked creation flow', async () => {
  const collection = { id: 'lib', workspace_id: 'w', name: 'Archive', kind: 'library' as const, version: 2 };
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string) => new Response(JSON.stringify(new URL(String(input)).pathname === '/v1/resources/lib' ? { ...collection, access: ['read', 'write'] } : {}))));
  render(<QueryClientProvider client={new QueryClient()}><PlaceContextMenu resource={collection}><button>Archive</button></PlaceContextMenu></QueryClientProvider>);
  fireEvent.contextMenu(screen.getByRole('button', { name: 'Archive' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'New folder…' }));
  expect(navigate).toHaveBeenCalledWith({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: 'w', listId: 'lib' }, search: { create: 'folder' } });
  expect(screen.queryByRole('menuitem', { name: 'Delete…' })).toBeNull();
});

it('offers the shortcuts in Spanish', async () => {
  const collection = { id: 'lib', workspace_id: 'w', name: 'Facturas', kind: 'library' as const, version: 2 };
  vi.stubGlobal('fetch', vi.fn(async (input: URL | string) => new Response(JSON.stringify(new URL(String(input)).pathname === '/v1/resources/lib' ? { ...collection, access: ['read', 'write', 'manage'] } : {}))));
  await act(() => setLanguage('es'));
  try {
    render(<QueryClientProvider client={new QueryClient()}><PlaceContextMenu resource={collection}><button>Facturas</button></PlaceContextMenu></QueryClientProvider>);
    fireEvent.contextMenu(screen.getByRole('button', { name: 'Facturas' }));
    expect(await screen.findByRole('menuitem', { name: 'Nueva carpeta…' })).toBeTruthy();
    expect(screen.getByRole('menuitem', { name: 'Subir…' })).toBeTruthy();
    expect(screen.getByRole('menuitem', { name: 'Eliminar…' })).toBeTruthy();
  } finally {
    await act(() => setLanguage('en'));
  }
});
