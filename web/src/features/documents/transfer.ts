import { api } from '@/api/http';
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

export async function uploadDocument(url: string, file: File, metadata?: DocumentMetadata): Promise<DocumentUpload> {
  const request = documentUploadRequest(url, file.name, metadata);
  const body = new FormData();
  for (const part of request.parts) {
    if (part.value === 'file') body.append(part.name, file, part.filename);
    else body.append(part.name, part.value);
  }
  return (await api<DocumentUpload>(request.url, { method: request.method, raw: body })).data;
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

export function downloadContent(itemId: string, fallbackName: string) {
  return downloadFile(downloadContentPath(itemId), fallbackName);
}
