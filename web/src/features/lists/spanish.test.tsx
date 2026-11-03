// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import type { Resource } from '@/api/types';
import { setLanguage } from '@/i18n';
import { accessLabel } from './access-editor';
import { CollectionGeneral } from './collection-settings';
import { columnLabel } from './schema';

afterEach(async () => {
  cleanup();
  await setLanguage('en');
});

const list = { id: 'list', kind: 'list', name: 'Contratos', version: 1, workspace_id: 'w', allow_folders: true, versioning: 'off' } as Resource;

it('shows list settings and built-in labels in Spanish', async () => {
  await act(() => setLanguage('es'));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <CollectionGeneral collection={list} canManage onDeleted={() => {}} />
    </QueryClientProvider>,
  );
  expect(screen.getByText('Permitir carpetas')).toBeTruthy();
  expect(screen.getByRole('option', { name: 'Conservar versiones' })).toBeTruthy();
  expect(screen.getAllByRole('button', { name: 'Eliminar la lista' })).toHaveLength(1);
  expect(columnLabel('$modified_by', [])).toBe('Modificado por');
  expect(accessLabel('read_draft')).toBe('Leer borradores');
});
