import { parseJSON, stringifyJSON } from './json';
import { authenticationMode, authorizationHeaders } from './auth';
export { clearToken, setToken, token } from './auth';

export interface ProblemDetails {
  code?: string;
  detail?: string;
  title?: string;
  existing_item?: { id: string; version: number };
  source_item_id?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, detail: string, readonly problem?: ProblemDetails) {
    super(detail || code);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

export function isApiError(error: unknown, status?: number): error is ApiError {
  return error instanceof ApiError && (status === undefined || error.status === status);
}

interface RequestOptions {
  signal?: AbortSignal;
  method?: string;
  body?: unknown;
  raw?: BodyInit;
  ifMatch?: number;
  query?: Record<string, string | number | undefined | null>;
  headers?: Record<string, string>;
}

export interface ApiResult<T> {
  data: T;
  etag: number | null;
  status: number;
}

function etagOf(response: Response): number | null {
  const raw = response.headers.get('ETag');
  if (!raw) return null;
  const value = Number(raw.replace(/^W\//, '').replaceAll('"', ''));
  return Number.isFinite(value) ? value : null;
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<ApiResult<T>> {
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers };
  if (authenticationMode() === 'proxy') {
    for (const key of Object.keys(headers)) if (key.toLowerCase() === 'authorization') delete headers[key];
  }
  Object.assign(headers, authorizationHeaders());
  if (options.ifMatch !== undefined) headers['If-Match'] = `"${options.ifMatch}"`;

  let body: BodyInit | undefined;
  if (options.raw !== undefined) body = options.raw;
  else if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = stringifyJSON(options.body);
  }

  const url = path.startsWith('http') ? new URL(path) : new URL(path, window.location.origin);
  for (const [key, value] of Object.entries(options.query ?? {})) {
    if (value !== undefined && value !== null && value !== '') url.searchParams.set(key, String(value));
  }

  const response = await fetch(url, { method: options.method ?? 'GET', headers, body, signal: options.signal, credentials: 'same-origin' });
  if (response.status === 204) return { data: undefined as T, etag: etagOf(response), status: 204 };

  const text = await response.text();
  const payload = text ? parseJSON(text) : undefined;
  if (!response.ok) {
    const problem = payload as ProblemDetails | undefined;
    throw new ApiError(
      response.status,
      problem?.code ?? 'error',
      problem?.detail ?? problem?.title ?? response.statusText,
      problem,
    );
  }
  return { data: payload as T, etag: etagOf(response), status: response.status };
}

export function listOf<T>(page: { value?: T[] | null } | T[] | null | undefined): T[] {
  if (Array.isArray(page)) return page;
  return page?.value ?? [];
}

/** `@odata.nextLink` is an absolute API URL. Keep the path so the dev proxy can carry the bearer token. */
export function continuation(link: string | null | undefined): string | null {
  if (!link) return null;
  try {
    const url = new URL(link, window.location.origin);
    return `${url.pathname}${url.search}`;
  } catch {
    return link;
  }
}
