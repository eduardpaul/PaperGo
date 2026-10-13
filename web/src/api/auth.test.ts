// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { api } from './http';
import {
  authenticationMode,
  authorizationHeaders,
  clearToken,
  loadAuthentication,
  oidcSignIn,
  setToken,
  token,
} from './auth';
import { safeReturnPath } from './oidc';
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

it('loads provider sign-in for OIDC deployments and restores this tab’s signed-in user', async () => {
  const settings = { issuer: 'https://auth.example.com/application/o/papergo/', client_id: 'papergo', scope: 'openid' };
  const userKey = `oidc.user:${settings.issuer}:${settings.client_id}`;
  const expiresAt = Math.floor(Date.now() / 1000) + 300;
  sessionStorage.setItem(
    userKey,
    JSON.stringify({ access_token: 'provider-access', token_type: 'Bearer', profile: { sub: 'alice' }, expires_at: expiresAt }),
  );
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ mode: 'bearer', oidc: settings })));
  await loadAuthentication();
  expect(oidcSignIn()?.settings).toEqual(settings);
  expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer provider-access' });
  clearToken();
  await vi.waitFor(() => expect(sessionStorage.getItem(userKey)).toBeNull());
  expect(token()).toBeNull();
});

it('drops an expired provider sign-in that cannot be renewed', async () => {
  const settings = { issuer: 'https://auth.example.com/', client_id: 'papergo', scope: 'openid' };
  const userKey = `oidc.user:${settings.issuer}:${settings.client_id}`;
  sessionStorage.setItem(
    userKey,
    JSON.stringify({ access_token: 'expired', token_type: 'Bearer', profile: { sub: 'alice' }, expires_at: 1 }),
  );
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ mode: 'bearer', oidc: settings })));
  await loadAuthentication();
  expect(token()).toBeNull();
  expect(sessionStorage.getItem(userKey)).toBeNull();
});

it('rejects invalid provider settings and ignores them outside bearer mode', async () => {
  for (const oidc of [null, 'issuer', { issuer: 'not a url', client_id: 'papergo', scope: 'openid' }, { issuer: 'https://auth.example.com/', client_id: '', scope: 'openid' }]) {
    vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ mode: 'bearer', oidc })));
    await expect(loadAuthentication()).rejects.toThrow('invalid authentication settings');
  }
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ mode: 'bearer' })));
  await loadAuthentication();
  expect(oidcSignIn()).toBeNull();
});

it('only continues at same-origin application paths after sign-in', () => {
  expect(safeReturnPath('/w/workspace?view=1')).toBe('/w/workspace?view=1');
  for (const value of ['https://attacker.example', '//attacker.example', '/\\attacker.example', undefined, 7]) {
    expect(safeReturnPath(value)).toBe('/');
  }
});
