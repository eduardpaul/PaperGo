export type AuthenticationMode = 'bearer' | 'proxy';

let mode: AuthenticationMode = 'bearer';
const TokenKey = 'papergo.token';

export function authenticationMode(): AuthenticationMode {
  return mode;
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
  mode = config.mode;
  if (mode === 'proxy') clearToken();
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

export function clearToken() {
  try {
    sessionStorage.removeItem(TokenKey);
  } catch {
    /* Storage can be unavailable. */
  }
}

/** Proxy deployments use the browser's HTTP credentials; only bearer deployments add Authorization. */
export function authorizationHeaders(): Record<string, string> {
  const bearer = mode === 'bearer' ? token() : null;
  return bearer ? { Authorization: `Bearer ${bearer}` } : {};
}
