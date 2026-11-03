// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { FilePreview } from '@/features/documents/preview';
import { setLanguage } from '@/i18n';
import { FileAction } from './file-action';
import { fileTypeLabel } from './file-types';

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await setLanguage('en');
});

it('shows files in Spanish', async () => {
  await act(() => setLanguage('es'));
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ status: 'unavailable' }), { headers: { 'Content-Type': 'application/json' } })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const item = { id: 'item', workspace_id: 'w', container_id: 'lib', name: 'export', file: { blob_id: 'b', content_type: 'application/zip', filename: 'export.zip', size: 10, version: 1 } };
  render(
    <QueryClientProvider client={client}>
      <FilePreview itemId="item" version={1} alt="" />
      <FileAction item={item as never} variant="button" />
    </QueryClientProvider>,
  );
  expect(await screen.findByText('Vista previa no disponible')).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Descargar export.zip' }).textContent).toContain('Descargar');
  expect(fileTypeLabel(undefined)).toBe('Archivo');
  client.clear();
});
