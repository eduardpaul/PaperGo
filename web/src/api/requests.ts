import { stringifyJSON } from './json';

/** Shapes of `/v1` calls the screens share. Tests assert these paths and bodies. */

export interface ApiCall {
  path: string;
  method: 'GET' | 'POST' | 'PUT';
  query?: Record<string, string | number | undefined>;
  body?: unknown;
  ifMatch?: number;
  headers?: Record<string, string>;
}

export function listingRequest(collectionId: string, folder: string | undefined, viewId: string | undefined, q: string | undefined): ApiCall {
  if (viewId && !folder && !q) {
    return { path: `/v1/views/${viewId}/query`, method: 'POST', query: { $top: 50 }, body: { include_total: true } };
  }
  return { path: `/v1/resources/${folder || collectionId}/children`, method: 'GET', query: { q, $top: 50 } };
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

export function downloadContentPath(itemId: string): string {
  return `/v1/items/${itemId}/content`;
}
