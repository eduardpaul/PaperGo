import { stringifyJSON } from './json';
import type { FilterExpr, QuerySpec } from './types';

/** Shapes of `/v1` calls the screens share. Tests assert these paths and bodies. */

export interface ApiCall {
  path: string;
  method: 'GET' | 'POST' | 'PUT';
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  ifMatch?: number;
  headers?: Record<string, string>;
}

export interface ListingSort {
  field: string;
  direction: 'asc' | 'desc';
}

export interface ListingOptions {
  folder?: string;
  view?: { query?: QuerySpec; columns?: string[] | null };
  q?: string;
  sort?: ListingSort;
  /** Columns chosen on the page instead of the view's. */
  columns?: string[];
}

/**
 * The rows of a list or library. Plain browsing reads the open folder's children. A saved view or a column sort runs
 * a collection query: the view's query with the open folder, the search text and the chosen sort on top.
 */
export function listingRequest(collectionId: string, options: ListingOptions): ApiCall {
  const { folder, view, q, sort } = options;
  if (!view && !sort) {
    return { path: `/v1/resources/${folder || collectionId}/children`, method: 'GET', query: { q, $top: 50 } };
  }
  const base = view?.query ?? {};
  const query: QuerySpec = { ...base };
  const parent = folder ?? base.parent_id ?? (view ? undefined : collectionId);
  if (parent) query.parent_id = parent;
  if (q) query.search = q;
  if (sort) query.sort = [sort];
  // A view's rows carry only its columns; the page asks for the columns it shows.
  const columns = view ? ((options.columns ?? view.columns)?.filter(Boolean) ?? []) : [];
  const select = columns.length
    ? [...new Set(['$name', ...columns, ...(base.group_by ? [base.group_by] : [])])]
    : undefined;
  return {
    path: `/v1/resources/${collectionId}/query`,
    method: 'POST',
    query: { $top: 50 },
    body: select ? { include_total: true, query, select } : { include_total: true, query },
  };
}

/** Both conditions; either may be absent. */
export function andFilter(base: FilterExpr | undefined, extra: FilterExpr | undefined): FilterExpr | undefined {
  if (!base) return extra;
  if (!extra) return base;
  return { and: [base, extra] };
}

type ViewLike = { query?: QuerySpec; columns?: string[] | null };

function viewSelect(view: ViewLike): string[] | undefined {
  const columns = view.columns?.filter(Boolean) ?? [];
  return columns.length ? [...new Set(['$name', ...columns, ...(view.query?.group_by ? [view.query.group_by] : [])])] : undefined;
}

/**
 * Rows of a view narrowed by one more condition (a board column, a calendar month). Boards and calendars ask for
 * their own rows so they show the whole collection, not the rows a table happened to load.
 */
export function viewRowsRequest(collectionId: string, view: ViewLike, extra: FilterExpr | undefined, top = 50): ApiCall {
  const { group_by: _group, ...base } = view.query ?? {};
  const query: QuerySpec = { ...base };
  const filter = andFilter(base.filter, extra);
  if (filter) query.filter = filter;
  const select = viewSelect(view);
  return {
    path: `/v1/resources/${collectionId}/query`,
    method: 'POST',
    query: { $top: top },
    body: select ? { query, select } : { query },
  };
}

/** How many items of a view fall in each value of its grouping field (board column counts). */
export function viewGroupsRequest(collectionId: string, view: ViewLike): ApiCall {
  const { sort: _sort, ...query } = view.query ?? {};
  return { path: `/v1/resources/${collectionId}/query/groups`, method: 'POST', query: { $top: 500 }, body: { query } };
}

/** Items of a looked-up list whose name contains the typed text (any case), by name. */
export function lookupOptionsRequest(containerId: string, text: string): ApiCall {
  const query: QuerySpec = { sort: [{ field: '$name', direction: 'asc' }] };
  if (text.trim()) query.filter = { field: '$name', op: 'contains', value: text.trim() };
  return { path: `/v1/resources/${containerId}/query`, method: 'POST', query: { $top: 20 }, body: { query, select: [] } };
}

/** The API takes at most 50 values per `in` and 32 filter nodes, so larger selections go in several requests. */
export const bulkIdsPerRequest = 31 * 50;

/**
 * Sets fields on many items in the background: on the selected ids (in chunks the API accepts) or on everything
 * a query matches.
 */
export function bulkUpdateRequests(
  collectionId: string,
  target: { ids: string[] } | { query: QuerySpec },
  values: Record<string, unknown>,
): ApiCall[] {
  const call = (query: QuerySpec): ApiCall => ({
    path: `/v1/resources/${collectionId}/bulk-update`,
    method: 'POST',
    body: { query, values },
  });
  if ('query' in target) {
    const { sort: _sort, group_by: _group, ...query } = target.query;
    return [call(query)];
  }
  const calls: ApiCall[] = [];
  for (let start = 0; start < target.ids.length; start += bulkIdsPerRequest) {
    const ids = target.ids.slice(start, start + bulkIdsPerRequest);
    const parts: FilterExpr[] = [];
    for (let at = 0; at < ids.length; at += 50) parts.push({ field: '$id', op: 'in', value: ids.slice(at, at + 50) });
    calls.push(call({ filter: parts.length === 1 ? parts[0] : { or: parts } }));
  }
  return calls;
}

export function searchRequest(q: string, workspaceId: string): ApiCall {
  return { path: '/v1/resources', method: 'GET', query: { q, workspace_id: workspaceId, $top: 50 } };
}

export function smartFolderQueryRequest(id: string, path: (string | null)[]): ApiCall {
  return { path: `/v1/smart-folders/${id}/query`, method: 'POST', query: { $top: 50 }, body: { include_total: true, path } };
}

export interface UploadPart {
  name: 'file' | 'metadata';
  filename?: string;
  value: 'file' | string;
}

export function documentUploadRequest(url: string, fileName: string, metadata?: object): { url: string; method: 'POST'; parts: UploadPart[] } {
  const parts: UploadPart[] = [{ name: 'file', filename: fileName, value: 'file' }];
  if (metadata && Object.values(metadata).some((value) => value !== undefined)) {
    parts.push({ name: 'metadata', value: stringifyJSON(metadata)! });
  }
  return { url, method: 'POST', parts };
}

export function replaceContentRequest(itemId: string, version: number, file: { name: string; type: string }): ApiCall {
  return {
    path: `/v1/items/${itemId}/content`,
    method: 'PUT',
    ifMatch: version,
    headers: {
      'Content-Type': file.type || 'application/octet-stream',
      'X-Filename': file.name,
    },
  };
}

/** The current file, or an earlier version by its blob id (draft readers only). */
export function downloadContentPath(itemId: string, blobId?: string): string {
  return `/v1/items/${itemId}/content${blobId ? `?blob_id=${encodeURIComponent(blobId)}` : ''}`;
}
