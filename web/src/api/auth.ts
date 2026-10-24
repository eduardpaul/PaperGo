import type { OidcSettings, OidcSignIn } from './oidc';

export type AuthenticationMode = 'bearer' | 'proxy' | 'local';

let mode: AuthenticationMode = 'bearer';
let oidc: OidcSignIn | null = null;
let mcp: string | null = null;
let comments = false;
let types: FileTypeInfo[] = [];
let editMax = 2 * 1024 * 1024;
const TokenKey = 'papergo.token';

/** A kind of file the server names, and what it does with such files. */
export interface FileTypeInfo {
  media_type: string;
  label: string;
  family: string;
  extensions: string[];
  preview: boolean;
  text: boolean;
}

export function authenticationMode(): AuthenticationMode {
  return mode;
}

/** Where AI assistants reach this deployment's MCP server; null when it runs none. */
export function mcpEndpoint(): string | null {
  return mcp;
}

/** Whether the deployment runs discussions on items. */
export function commentsAvailable(): boolean {
  return comments;
}

/** The kinds of file the server names, in its matching order. */
export function fileTypes(): FileTypeInfo[] {
  return types;
}

/** The largest file the browser edits (FILE_EDIT_MAX_BYTES). */
export function fileEditMax(): number {
  return editMax;
}

function fileTypeList(value: unknown): FileTypeInfo[] {
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is FileTypeInfo => !!entry && typeof entry === 'object' && typeof (entry as FileTypeInfo).media_type === 'string' && typeof (entry as FileTypeInfo).label === 'string');
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

function mcpSetting(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.toString() : null;
  } catch {
    return null;
  }
}

/** Initialize before mounting the router; never render the app with unknown server capabilities. */
export async function loadAuthentication() {
  const response = await fetch('/auth/config', { credentials: 'same-origin', cache: 'no-store' });
  if (!response.ok) throw new Error('Unable to load authentication settings.');
  const config: unknown = await response.json();
  const reported = config && typeof config === 'object' && 'mode' in config ? (config as { mode?: unknown; oidc?: unknown }).mode : undefined;
  if (reported !== 'bearer' && reported !== 'proxy' && reported !== 'local') {
    throw new Error('The server returned invalid authentication settings.');
  }
  const settings = reported === 'bearer' ? oidcSettings((config as { oidc?: unknown }).oidc) : null;
  mode = reported;
  oidc = null;
  const features = (config as { features?: { comments?: unknown; mcp?: { url?: unknown }; file_types?: unknown; file_edit_max_bytes?: unknown } }).features;
  mcp = mcpSetting(features?.mcp?.url);
  comments = features?.comments === true;
  types = fileTypeList(features?.file_types);
  if (typeof features?.file_edit_max_bytes === 'number' && features.file_edit_max_bytes > 0) editMax = features.file_edit_max_bytes;
  if (mode === 'proxy' || mode === 'local') clearToken();
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

/** Proxy and local deployments use browser credentials; only bearer deployments add Authorization. */
export function authorizationHeaders(): Record<string, string> {
  const bearer = mode === 'bearer' ? token() : null;
  return bearer ? { Authorization: `Bearer ${bearer}` } : {};
}

/** Local deployments exchange a username and password for an HttpOnly session cookie. */
export async function signInLocal(username: string, password: string) {
  const response = await fetch('/auth/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!response.ok) throw new Error('Those credentials were not accepted.');
}

export async function signOutLocal() {
  await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin' });
}
