// The page's side of an app's bridge. An app runs in a sandboxed frame with
// no origin, network or login of its own; it asks the page for each API
// request, and the page sends it as the viewer, so the app never sees a
// login. The page refuses the routes that administer PaperGo, whoever views,
// so an administrator who opens an app does not let it change who
// administers the organization.

import { authenticationMode, authorizationHeaders } from '@/api/auth';

/** A response as the app's runtime reads it. */
export interface BridgeResponse {
  status: number;
  headers: Record<string, string>;
  /** The body as text; empty when it is binary. */
  body: string;
  bytes?: ArrayBuffer;
}

export interface BridgeRequest {
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string | ArrayBuffer;
}

const methods = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE']);

// The request headers an app may set. Credentials, cookies and the like are
// the page's.
const requestHeaders = new Set(['accept', 'content-type', 'if-match', 'if-none-match', 'idempotency-key', 'prefer']);

// The response headers an app reads.
const responseHeaders = ['content-type', 'etag', 'location', 'retry-after', 'papergo-app-version'];

const segment = '[^/?#]+';
// Routes refused whatever the method: administration, credentials, apps
// themselves, and routes that would carry other requests past these rules.
const refused = [
  /^\/v1\/identity(\/|$)/,
  /^\/v1\/organization(\/|$)/,
  /^\/v1\/portability(\/|$)/,
  /^\/v1\/audit(\/|$)/,
  /^\/v1\/storage(\/|$)/,
  /^\/v1\/events(\/|$)/,
  /^\/v1\/mcp(\/|$)/,
  /^\/v1\/\$batch$/,
  /^\/v1\/apps(\/|$)/,
  new RegExp(`^/v1/workspaces/${segment}/apps(/|$)`),
];
// Routes an app may read but not change: what runs as its author, decides
// who has access, or has the server fetch from the network.
const readOnly = [
  new RegExp(`^/v1/workflows/${segment}$`),
  new RegExp(`^/v1/workspaces/${segment}/workflows$`),
  new RegExp(`^/v1/workspaces/${segment}/workflow-builtins(/|$)`),
  new RegExp(`^/v1/workspaces/${segment}/members(/|$)`),
  new RegExp(`^/v1/resources/${segment}/permissions$`),
  new RegExp(`^/v1/resources/${segment}/integrations(/|$)`),
];

/** Why the page refuses an app's request, or undefined when it sends it. */
export function refusal(method: string, url: string): string | undefined {
  if (!methods.has(method)) return `apps cannot send ${method} requests`;
  if (!url.startsWith('/v1/') || url.startsWith('//')) return 'apps call only the PaperGo API under /v1';
  let path: string;
  try {
    path = decodeURIComponent(new URL(url, 'http://app.invalid').pathname);
  } catch {
    return 'the path is not valid';
  }
  if (path.includes('/../') || path.endsWith('/..')) return 'the path is not valid';
  if (refused.some((pattern) => pattern.test(path))) return 'apps cannot call the routes that administer PaperGo';
  if (method !== 'GET' && method !== 'HEAD' && readOnly.some((pattern) => pattern.test(path))) return 'apps cannot change workflows, members, permissions or integrations';
  return undefined;
}

function problem(status: number, code: string, detail: string): BridgeResponse {
  return { status, headers: { 'content-type': 'application/problem+json' }, body: JSON.stringify({ type: 'about:blank', status, code, detail }) };
}

const textType = /^(application\/([\w.+-]*\+)?json|text\/[\w.+-]+|application\/x-www-form-urlencoded)/i;

/** Sends an app's request as the viewer, or answers 403 app_refused. */
export async function send(request: BridgeRequest, fetcher: typeof fetch = fetch): Promise<BridgeResponse> {
  const method = request.method.toUpperCase();
  const reason = refusal(method, request.url);
  if (reason) return problem(403, 'app_refused', reason);
  const headers: Record<string, string> = {};
  for (const [name, value] of Object.entries(request.headers ?? {})) {
    if (requestHeaders.has(name.toLowerCase()) && typeof value === 'string') headers[name.toLowerCase()] = value;
  }
  if (authenticationMode() !== 'proxy') Object.assign(headers, authorizationHeaders());
  try {
    const response = await fetcher(new URL(request.url, window.location.origin), {
      method,
      headers,
      body: method === 'GET' || method === 'HEAD' ? undefined : request.body,
      credentials: 'same-origin',
      redirect: 'error',
    });
    const out: BridgeResponse = { status: response.status, headers: {}, body: '' };
    for (const name of responseHeaders) {
      const value = response.headers.get(name);
      if (value !== null) out.headers[name] = value;
    }
    if (textType.test(out.headers['content-type'] ?? '') || response.status === 204) out.body = await response.text();
    else out.bytes = await response.arrayBuffer();
    return out;
  } catch {
    return problem(503, 'network', 'PaperGo could not be reached');
  }
}

/** The frame's document: the runtime, the app's code and the start, under a CSP without network. */
export function appDocument(runtime: string, code: string, init: unknown): string {
  // A script ends at the first </script, whatever its case; nothing else in
  // a script's text is special to the HTML parser.
  const script = (text: string) => `<script>${text.replace(/<\/(script)/gi, '<\\/$1')}</script>`;
  const csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'";
  const start = `globalThis.__papergoApp.start(${JSON.stringify(init).replace(/</g, '\\u003c')});`;
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>${script(runtime)}${script(code)}${script(start)}</body></html>`;
}
