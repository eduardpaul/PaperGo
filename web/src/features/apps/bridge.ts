// The page's side of an app's bridge. An app runs in a sandboxed frame with
// no origin, network or login of its own; it asks the page for each API
// request, and the page sends it as the viewer, so the app never sees a
// login. The page refuses the routes that administer PaperGo, whoever views,
// so an administrator who opens an app does not let it change who
// administers the organization.

import { authenticationMode, authorizationHeaders } from '@/api/auth';
import type { ItemChange } from '@/lib/live';

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
const responseHeaders = ['content-type', 'etag', 'location', 'retry-after'];

const segment = '[^/?#]+';
// Routes refused whatever the method: administration, credentials, apps
// themselves, connections' targets and credentials, and routes that would
// carry other requests past these rules. Apps use connections by key, under
// their workspace.
const refused = [
  /^\/v1\/connections(\/|$)/,
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
// who has access, installs what a workspace runs, or has the server fetch
// from the network.
const readOnly = [
  new RegExp(`^/v1/workflows/${segment}$`),
  new RegExp(`^/v1/workspaces/${segment}/workflows$`),
  new RegExp(`^/v1/workspaces/${segment}/workflow-builtins(/|$)`),
  new RegExp(`^/v1/workspaces/${segment}/solutions(/|$)`),
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
  // Marks what an app sends, so the server applies an app's limits, such
  // as the smaller payloads it may publish through a connection.
  headers['papergo-app'] = '1';
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

/** Where an app last saw an item of its workspace: its list and its type. */
export interface Place {
  collectionId?: string;
  contentTypeId?: string;
}

/** The live change an app is told of: the item's place now and, when known, where the app saw it before. */
export type AppChange = Omit<ItemChange, 'workspaceId'> & { from?: Place };

/** The places an app frame has seen, oldest first, up to placesKept. */
export type Places = Map<string, Place>;

const placesKept = 100_000;

function remember(places: Places, id: string, place: Place) {
  places.delete(id);
  places.set(id, place);
  if (places.size > placesKept) places.delete(places.keys().next().value as string);
}

/**
 * The live change an app of a workspace is told of, if any. An app hears of
 * its own workspace, whatever else its viewer may see: an item that leaves
 * it for another is told as removed from where the app last saw it, and an
 * item the app never saw there is not told of. A change carries the place
 * the app saw the item in before, so a figure of a list hears of an item
 * leaving it or changing type. A resync may have missed changes anywhere,
 * and names no item.
 */
export function appChange(change: ItemChange, workspaceId: string, places: Places): AppChange | undefined {
  if (change.resync) return { id: '', resync: true };
  const before = places.get(change.id);
  if (change.workspaceId !== workspaceId) {
    if (!before) return undefined;
    places.delete(change.id);
    return { id: change.id, ...before, removed: true };
  }
  const place = { collectionId: change.collectionId, contentTypeId: change.contentTypeId };
  if (change.removed) places.delete(change.id);
  else remember(places, change.id, place);
  return { id: change.id, ...place, removed: change.removed, created: change.created, ...(before ? { from: before } : {}) };
}

/**
 * Remembers where the items an app read are: those of its workspace in a
 * response, an item or a page of them. A collection's query names its
 * collection in the path, as compact items do not.
 */
export function rememberRead(url: string, response: BridgeResponse, workspaceId: string, places: Places) {
  if (response.status >= 300 || !response.body.startsWith('{')) return;
  let body: { id?: unknown; value?: unknown };
  try {
    body = JSON.parse(response.body) as typeof body;
  } catch {
    return;
  }
  const queried = /^\/v1\/resources\/([^/?#]+)\/query(?:\/compact)?(?:[?#]|$)/.exec(new URL(url, 'http://app').pathname)?.[1];
  type Read = { id?: unknown; workspace_id?: unknown; container_id?: unknown; content_type_id?: unknown };
  const items = (Array.isArray(body.value) ? body.value : [body]) as Read[];
  for (const item of items) {
    if (!item || typeof item.id !== 'string' || item.workspace_id !== workspaceId) continue;
    const collectionId = typeof item.container_id === 'string' ? item.container_id : queried && decodeURIComponent(queried);
    remember(places, item.id, { collectionId: collectionId || undefined, contentTypeId: typeof item.content_type_id === 'string' ? item.content_type_id : undefined });
  }
}

/** The frame's document: the runtime, the app's code and the start, under a CSP without network. */
export function appDocument(runtime: string, code: string, init: unknown): string {
  // A script ends at the first </script, whatever its case, unless a <!-- and
  // a <script before it make the parser read on: neither stays as written.
  // </script stands only in strings, templates and comments, where <\/ is
  // the same; <!-- may stand in a regular expression too, where \x3C still
  // means < under the u and v flags, and <\! is an error.
  const script = (text: string) => `<script>${text.replace(/<\/(script)/gi, '<\\/$1').replace(/<!--/g, '\\x3C!--')}</script>`;
  const csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; webrtc 'block'; form-action 'none'; base-uri 'none'";
  const start = `globalThis.__papergoApp.start(${JSON.stringify(init).replace(/</g, '\\u003c')});`;
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>${script(runtime)}${script(code)}${script(start)}</body></html>`;
}
