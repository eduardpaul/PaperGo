// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { api } from './http';
import { authenticationMode, authorizationHeaders, clearToken, loadAuthentication, setToken, token } from './auth';
import { downloadFile } from './files';

afterEach(async () => {
  vi.useRealTimers();
  vi.stubGlobal('fetch', async () => new Response('{"mode":"bearer"}'));
  await loadAuthentication();
  clearToken();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('discovers proxy mode without a stale bearer and uses browser credentials for JSON, uploads and downloads', async () => {
  setToken('stale-token');
  const fetch = vi.fn(
    async (url: URL | string) => new Response(String(url) === '/auth/config' ? '{"mode":"proxy"}' : '{}'),
  );
  vi.stubGlobal('fetch', fetch);
  await loadAuthentication();
  expect(fetch.mock.calls[0]?.[0]).toBe('/auth/config');
  expect(authenticationMode()).toBe('proxy');
  expect(token()).toBeNull();
  // Even a subsequently written token cannot override proxy-managed identity.
  setToken('another-stale-token');
  await api('/v1/workspaces', { method: 'POST', body: { name: 'Proxy' }, headers: { Authorization: 'Bearer bypass' } });
  await api('/v1/resources/library/documents', { method: 'POST', raw: new FormData() });
  const NativeURL = URL;
  vi.stubGlobal(
    'URL',
    class extends NativeURL {
      static createObjectURL() {
        return 'blob:test';
      }
      static revokeObjectURL() {}
    },
  );
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
  vi.useFakeTimers();
  await downloadFile('/v1/items/item/content', 'file.pdf');
  vi.runOnlyPendingTimers();
  vi.useRealTimers();
  for (const [, options] of fetch.mock.calls.slice(1) as unknown as [unknown, RequestInit][]) {
    expect(options.credentials).toBe('same-origin');
    expect(new Headers(options.headers).has('Authorization')).toBe(false);
  }
  expect(authorizationHeaders()).toEqual({});
});

it('retains explicit bearer authentication for bearer deployments', async () => {
  vi.stubGlobal('fetch', async () => new Response('{"mode":"bearer"}'));
  await loadAuthentication();
  setToken('development-token');
  expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer development-token' });
});

it('rejects invalid bootstrap configuration', async () => {
  vi.stubGlobal('fetch', async () => new Response('{"mode":"unknown"}'));
  await expect(loadAuthentication()).rejects.toThrow('invalid authentication settings');
  vi.stubGlobal('fetch', async () => new Response('{"mode":["proxy"]}'));
  await expect(loadAuthentication()).rejects.toThrow('invalid authentication settings');
  vi.stubGlobal('fetch', async () => new Response('Authentication required', { status: 401 }));
  await expect(loadAuthentication()).rejects.toThrow('Unable to load authentication settings');
});
