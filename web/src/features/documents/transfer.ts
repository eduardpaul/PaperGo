import { api, ApiError } from '@/api/http';
import { authorizationHeaders } from '@/api/auth';
import { parseJSON } from '@/api/json';
import { downloadFile } from '@/api/files';
import { documentUploadRequest, downloadContentPath, replaceContentRequest } from '@/api/requests';
import type { DocumentUpload } from '@/api/types';

export interface DocumentMetadata {
  name?: string;
  content_type_id?: string;
  folder_id?: string;
  tags?: string[];
  values?: Record<string, unknown>;
  languages?: string;
}

export interface UploadOptions {
  /** Bytes sent so far and in total. */
  onProgress?: (loaded: number, total: number) => void;
  signal?: AbortSignal;
}

/** Thrown when an upload is cancelled. */
export class UploadCancelled extends Error {
  constructor() {
    super('Upload cancelled');
    this.name = 'UploadCancelled';
  }
}

/**
 * Creates a library document with a multipart request. It uses XMLHttpRequest because fetch cannot report upload
 * progress.
 */
export function uploadDocument(
  url: string,
  file: File,
  metadata?: DocumentMetadata,
  options: UploadOptions = {},
): Promise<DocumentUpload> {
  const request = documentUploadRequest(url, file.name, metadata);
  const body = new FormData();
  for (const part of request.parts) {
    if (part.value === 'file') body.append(part.name, file, part.filename);
    else body.append(part.name, part.value);
  }
  return new Promise((resolve, reject) => {
    if (options.signal?.aborted) {
      reject(new UploadCancelled());
      return;
    }
    const xhr = new XMLHttpRequest();
    xhr.open(request.method, new URL(request.url, window.location.origin));
    xhr.setRequestHeader('Accept', 'application/json');
    for (const [key, value] of Object.entries(authorizationHeaders())) xhr.setRequestHeader(key, value);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded, event.total);
    };
    xhr.onload = () => {
      let payload: unknown;
      try {
        payload = xhr.responseText ? parseJSON(xhr.responseText) : undefined;
      } catch {
        payload = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(payload as DocumentUpload);
        return;
      }
      const problem = payload as { code?: string; detail?: string; title?: string } | undefined;
      reject(new ApiError(xhr.status, problem?.code ?? 'error', problem?.detail ?? problem?.title ?? xhr.statusText));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network', 'The upload was interrupted. Check the connection and try again.'));
    xhr.onabort = () => reject(new UploadCancelled());
    options.signal?.addEventListener('abort', () => xhr.abort(), { once: true });
    xhr.send(body);
  });
}

export async function replaceContent(itemId: string, version: number, file: File) {
  const request = replaceContentRequest(itemId, version, file);
  return api<{ duplicates?: { item_id: string; name: string }[] | null }>(request.path, {
    method: request.method,
    raw: file,
    ifMatch: request.ifMatch,
    headers: request.headers,
  });
}

export function downloadContent(itemId: string, fallbackName: string, blobId?: string) {
  return downloadFile(downloadContentPath(itemId, blobId), fallbackName);
}
