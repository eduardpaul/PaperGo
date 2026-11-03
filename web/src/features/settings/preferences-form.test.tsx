// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { setLanguage } from '@/i18n';
import { PreferencesForm } from './preferences-form';

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await setLanguage('en');
  localStorage.clear();
});

it('offers the translated languages by their own names and renders in Spanish', async () => {
  const preferences = {
    language: 'de',
    time_zone: 'UTC',
    date_format: 'yyyy-MM-dd',
    time_format: '24h',
    number_format: 'de',
    theme: 'system',
    document_languages: 'eng',
    inherited: ['language'],
    version: 1,
  };
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(preferences))));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PreferencesForm />
    </QueryClientProvider>,
  );
  const language = await screen.findByLabelText('Language');
  // A locale set elsewhere stays a choice, named in the screen's language.
  const names = (select: HTMLElement) => within(select).getAllByRole('option').map((option) => option.textContent);
  expect(names(language)).toEqual(expect.arrayContaining(['English', 'Español', 'German (de)']));
  await act(() => setLanguage('es'));
  expect(screen.getByRole('button', { name: 'Guardar' })).toBeTruthy();
  expect(screen.getByText('Se usa el valor predeterminado de la organización hasta que lo cambies.')).toBeTruthy();
  const spanish = screen.getByLabelText('Idioma');
  expect(names(spanish)).toEqual(expect.arrayContaining(['English', 'Español', 'alemán (de)']));
  expect(within(screen.getByLabelText('Números')).getByRole('option', { name: 'español (es)' })).toBeTruthy();
});

it.each([false, true])('writes preferences for organization=%s with the fetched version', async (organization) => {
  const path = organization ? '/v1/organization/preferences' : '/v1/identity/me/preferences';
  const preferences = {
    language: 'en',
    time_zone: 'UTC',
    date_format: 'yyyy-MM-dd',
    time_format: '24h',
    number_format: 'en',
    theme: 'system',
    document_languages: 'eng',
    version: 7,
  };
  const fetch = vi.fn(async () => new Response(JSON.stringify(preferences)));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  render(
    <QueryClientProvider client={client}>
      <PreferencesForm organization={organization} />
    </QueryClientProvider>,
  );
  fireEvent.change(await screen.findByLabelText('Document languages'), { target: { value: 'eng+deu' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(fetch.mock.calls).toHaveLength(2));
  const [url, init] = fetch.mock.calls[1] as unknown as [URL, RequestInit];
  expect(url.pathname).toBe(path);
  expect(init.headers).toHaveProperty('If-Match', '"7"');
  expect(init.body).toBe('{"document_languages":"eng+deu"}');
});

it('shows organization defaults without editing controls when manage scope is absent', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            language: 'en',
            time_zone: 'UTC',
            date_format: 'yyyy-MM-dd',
            time_format: '24h',
            number_format: 'en',
            theme: 'system',
            document_languages: 'eng',
            version: 0,
          }),
        ),
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PreferencesForm organization canEdit={false} />
    </QueryClientProvider>,
  );
  await screen.findByLabelText('Language');
  expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
  expect((screen.getByLabelText('Language').closest('fieldset') as HTMLFieldSetElement).disabled).toBe(true);
});
