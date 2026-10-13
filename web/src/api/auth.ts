import type { OidcSettings, OidcSignIn } from './oidc';

export type AuthenticationMode = 'bearer' | 'proxy';

let mode: AuthenticationMode = 'bearer';
let oidc: OidcSignIn | null = null;
const TokenKey = 'papergo.token';

export function authenticationMode(): AuthenticationMode {
  return mode;
}

/** The provider sign-in of OIDC deployments that advertise a browser client; null otherwise. */
export function oidcSignIn(): OidcSignIn | null {
  return oidc;
}

function oidcSettings(value: unknown): OidcSettings | null {
  if (value === undefined) return null;
  if (!value || typeof value !== 'object') throw new Error('The server returned invalid authentication settings.');
  const { issuer, client_id, scope } = value as Record<string, unknown>;
  if (typeof issuer !== 'string' || typeof client_id !== 'string' || typeof scope !== 'string' || !client_id) {
    throw new Error('The server returned invalid authentication settings.');
  }
  try {
    new URL(issuer);
  } catch {
    throw new Error('The server returned invalid authentication settings.');
  }
  return { issuer, client_id, scope };
}

/** Initialize before mounting the router; never render the app with unknown server capabilities. */
export async function loadAuthentication() {
  const response = await fetch('/auth/config', { credentials: 'same-origin', cache: 'no-store' });
  if (!response.ok) throw new Error('Unable to load authentication settings.');
  const config: unknown = await response.json();
  if (
    !config ||
    typeof config !== 'object' ||
    !('mode' in config) ||
    (config.mode !== 'bearer' && config.mode !== 'proxy')
  ) {
    throw new Error('The server returned invalid authentication settings.');
  }
  const settings = config.mode === 'bearer' ? oidcSettings((config as { oidc?: unknown }).oidc) : null;
  mode = config.mode;
  oidc = null;
  if (mode === 'proxy') clearToken();
  if (settings) {
    // Loaded only by deployments that sign in at an identity provider.
    const { OidcSignIn } = await import('./oidc');
    oidc = new OidcSignIn(settings, storeToken);
    await oidc.restore();
  }
}

export function token(): string | null {
  try {
    return sessionStorage.getItem(TokenKey);
  } catch {
    return null;
  }
}

export function setToken(value: string) {
  sessionStorage.setItem(TokenKey, value);
}

function storeToken(value: string | null) {
  if (value) setToken(value);
  else
    try {
      sessionStorage.removeItem(TokenKey);
    } catch {
      /* Storage can be unavailable. */
    }
}

/** Forgets the bearer token and any provider sign-in of this tab. */
export function clearToken() {
  storeToken(null);
  oidc?.forget();
}

/** Proxy deployments use the browser's HTTP credentials; only bearer deployments add Authorization. */
export function authorizationHeaders(): Record<string, string> {
  const bearer = mode === 'bearer' ? token() : null;
  return bearer ? { Authorization: `Bearer ${bearer}` } : {};
}
