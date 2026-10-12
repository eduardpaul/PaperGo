import { stringifyJSON } from './json';
import type { QuerySpec } from './types';

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
