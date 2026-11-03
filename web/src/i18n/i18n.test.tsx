// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ApiError } from '@/api/http';
import { FormDialog } from '@/components/form-dialog';
import { problemMessage } from '@/lib/errors';
import { browserLanguage, i18n, setLanguage } from '.';
import { uiLanguage, uiLocale } from './languages';
import { pseudo } from './pseudo';
import { namespaces } from './resources';

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await setLanguage('en');
  localStorage.clear();
});

it('uses a regional locale’s base language and English for languages without a catalog', () => {
  expect(uiLanguage('es').code).toBe('es');
  expect(uiLanguage('es-MX').code).toBe('es');
  expect(uiLanguage('de-AT').code).toBe('en');
  expect(uiLanguage(undefined).code).toBe('en');
  expect(uiLocale('es-MX')).toBe('es-MX');
  expect(uiLocale('de')).toBe('en');
});

it('follows the browser before sign-in, then the language used last', () => {
  vi.stubGlobal('navigator', { languages: ['de-DE', 'es-AR', 'en'], language: 'de-DE' });
  expect(browserLanguage()).toBe('es-AR');
  vi.stubGlobal('navigator', { languages: ['en-US', 'es'], language: 'en-US' });
  expect(browserLanguage()).toBe('en-US');
  vi.stubGlobal('navigator', { languages: ['de'], language: 'de' });
  expect(browserLanguage()).toBeUndefined();
  localStorage.setItem('papergo.language', 'es');
  expect(browserLanguage()).toBe('es');
});

it('loads Spanish and renders screens in it', async () => {
  render(<FormDialog open title="Lista" submitLabel="Crear" onClose={() => {}} onSubmit={() => {}}>{null}</FormDialog>);
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
  await act(() => setLanguage('es-MX'));
  expect(screen.getByRole('button', { name: 'Cancelar' })).toBeTruthy();
  expect(screen.getByRole('button', { name: 'Cerrar' })).toBeTruthy();
  expect(document.documentElement.lang).toBe('es');
  expect(localStorage.getItem('papergo.language')).toBe('es');
});

it('shows the language asked for last, even when an earlier one is still loading', async () => {
  for (const namespace of namespaces) i18n.removeResourceBundle('es', namespace);
  const spanish = setLanguage('es');
  await setLanguage('en');
  await spanish;
  expect(i18n.language).toBe('en');
  expect(document.documentElement.lang).toBe('en');
  expect(localStorage.getItem('papergo.language')).toBe('en');
});

it('loads only the namespaces a language is missing, so a failed chunk is retried', async () => {
  await setLanguage('es');
  i18n.removeResourceBundle('es', 'lists');
  await setLanguage('en');
  await setLanguage('es');
  expect(i18n.hasResourceBundle('es', 'lists')).toBe(true);
  expect(i18n.t('lists:matching.selectAllUncounted')).toBe('Seleccionar todos los elementos coincidentes');
});

it('translates problems with a fixed meaning and keeps the server’s detail otherwise', async () => {
  await setLanguage('es');
  expect(problemMessage(new ApiError(409, 'last_owner', 'A workspace needs at least one owner.'))).toMatch(/al menos un propietario/);
  expect(problemMessage(new ApiError(422, 'validation_failed', 'name is required'))).toBe('name is required');
  expect(problemMessage('nothing useful')).toBe(i18n.t('errors:generic'));
});

it('pseudo-translates text but keeps placeholders and tags', () => {
  expect(pseudo('Remove {{name}} <b>now</b>')).toMatch(/^\[Ŕéɱöṽé \{\{name\}\} <b>ñöŵ<\/b> ·+\]$/);
});
