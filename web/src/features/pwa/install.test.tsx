// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { appManifestUrl, InstallButton, useAppManifest, watchInstallOffer } from './install';
import { installedApp, rememberInstalledApp } from './installed';

const app = { id: '9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f', workspace_id: '0b6f2a5e-1c3d-4e8f-9a7b-2d4c6e8f0a1b', name: 'Diesel & fuel', icon: 'fuel', color: '#b45309' };

function standaloneWindow(on: boolean) {
  window.matchMedia = ((query: string) => ({ matches: on && query === '(display-mode: standalone)' })) as unknown as typeof window.matchMedia;
}

beforeEach(() => {
  document.head.innerHTML =
    '<link rel="manifest" href="/pwa/papergo.webmanifest"><link rel="apple-touch-icon" href="/pwa/icon-180.png"><meta name="apple-mobile-web-app-title" content="PaperGo">';
  document.title = 'PaperGo';
  sessionStorage.clear();
  standaloneWindow(false);
});

afterEach(() => {
  cleanup();
  window.history.replaceState(null, '', '/');
});

const linked = () => ({
  manifest: document.querySelector('link[rel="manifest"]')?.getAttribute('href'),
  icon: document.querySelector('link[rel="apple-touch-icon"]')?.getAttribute('href'),
  title: document.querySelector('meta[name="apple-mobile-web-app-title"]')?.getAttribute('content'),
});

// Behind a proxy that authenticates every request, a browser fetches the
// manifest and its icons with the proxy's credentials only when the link asks.
it('has the browser send the proxy sign-in with the manifest', () => {
  const page = readFileSync('index.html', 'utf8');
  expect(page).toMatch(/<link rel="manifest" href="\/pwa\/papergo\.webmanifest" crossorigin="use-credentials" \/>/);
});

it("names an app's manifest by its page, name, glyph and color", () => {
  const url = new URL(appManifestUrl(app), 'https://papergo.example');
  expect(url.pathname).toBe('/pwa/app.webmanifest');
  expect(Object.fromEntries(url.searchParams)).toEqual({ name: 'Diesel & fuel', icon: 'fuel', color: '#b45309', workspace: app.workspace_id, app: app.id });
  // An app that chose neither leaves them to the server.
  expect(appManifestUrl({ ...app, icon: '', color: '' })).not.toMatch(/icon=|color=/);
});

it("offers the app on its page and PaperGo again after it", () => {
  function Page({ shown }: { shown?: typeof app }) {
    useAppManifest(shown);
    return null;
  }
  const page = render(<Page />);
  expect(linked().manifest).toBe('/pwa/papergo.webmanifest');
  page.rerender(<Page shown={app} />);
  expect(linked()).toEqual({ manifest: appManifestUrl(app), icon: expect.stringMatching(/^\/pwa\/icon-180\.png\?name=Diesel/), title: 'Diesel & fuel' });
  expect(document.title).toBe('Diesel & fuel');
  page.unmount();
  expect(linked()).toEqual({ manifest: '/pwa/papergo.webmanifest', icon: '/pwa/icon-180.png', title: 'PaperGo' });
  expect(document.title).toBe('PaperGo');
});

it("shows Install only where the browser offers it, and asks the browser", async () => {
  watchInstallOffer();
  render(<InstallButton name="PaperGo" />);
  expect(screen.queryByRole('button')).toBeNull();
  const prompt = vi.fn(() => Promise.resolve());
  const offer = Object.assign(new Event('beforeinstallprompt', { cancelable: true }), { prompt });
  act(() => void window.dispatchEvent(offer));
  expect(offer.defaultPrevented).toBe(true);
  await act(async () => screen.getByRole('button').click());
  expect(prompt).toHaveBeenCalledOnce();
  // An offer is good for one answer.
  expect(screen.queryByRole('button')).toBeNull();
});

it("shows no Install in an installed window", () => {
  standaloneWindow(true);
  watchInstallOffer();
  render(<InstallButton name="PaperGo" />);
  act(() => void window.dispatchEvent(Object.assign(new Event('beforeinstallprompt'), { prompt: () => Promise.resolve() })));
  expect(screen.queryByRole('button')).toBeNull();
});

it("remembers the app a window was installed for, from its first page only", () => {
  // A browser tab on an app's page is PaperGo's window.
  window.history.replaceState(null, '', `/w/${app.workspace_id}/a/${app.id}`);
  rememberInstalledApp();
  expect(installedApp()).toBeNull();

  // An installed window that starts on an app's page is that app's, also after it loads another page.
  sessionStorage.clear();
  standaloneWindow(true);
  rememberInstalledApp();
  expect(installedApp()).toBe(app.id);
  window.history.replaceState(null, '', '/login/callback');
  rememberInstalledApp();
  expect(installedApp()).toBe(app.id);

  // PaperGo's installed window starts at the launcher and stays PaperGo's on an app's page.
  sessionStorage.clear();
  window.history.replaceState(null, '', '/launcher');
  rememberInstalledApp();
  window.history.replaceState(null, '', `/w/${app.workspace_id}/a/${app.id}`);
  rememberInstalledApp();
  expect(installedApp()).toBeNull();
});
