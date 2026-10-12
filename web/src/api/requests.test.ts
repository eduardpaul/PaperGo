import { describe, expect, it } from 'vitest';
import { documentUploadRequest, downloadContentPath, listingRequest, replaceContentRequest, searchRequest, smartFolderQueryRequest } from './requests';

describe('listing and search requests', () => {
  it('queries a saved view at the collection root', () => {
    expect(listingRequest('col', undefined, 'view-1', undefined)).toEqual({
      path: '/v1/views/view-1/query',
      method: 'POST',
      query: { $top: 50 },
      body: { include_total: true },
    });
  });

  it('browses the open folder, and a search term, instead of the saved view', () => {
    expect(listingRequest('col', 'folder', 'view-1', undefined)).toEqual({
      path: '/v1/resources/folder/children',
      method: 'GET',
      query: { q: undefined, $top: 50 },
    });
    expect(listingRequest('col', undefined, 'view-1', 'invoice')).toEqual({
      path: '/v1/resources/col/children',
      method: 'GET',
      query: { q: 'invoice', $top: 50 },
    });
  });

  it('searches one workspace', () => {
    expect(searchRequest('invoice', 'ws')).toEqual({
      path: '/v1/resources',
      method: 'GET',
      query: { q: 'invoice', workspace_id: 'ws', $top: 50 },
    });
  });

  it('posts the smart-folder path with the same body a continuation reuses', () => {
    expect(smartFolderQueryRequest('sf', ['2026', null])).toEqual({
      path: '/v1/smart-folders/sf/query',
      method: 'POST',
      query: { $top: 50 },
      body: { include_total: true, path: ['2026', null] },
    });
  });
});

describe('document transfer requests', () => {
  it('sends the file and a metadata part only when metadata is present', () => {
    expect(documentUploadRequest('/v1/resources/lib/documents', 'a.pdf')).toEqual({
      url: '/v1/resources/lib/documents',
      method: 'POST',
      parts: [{ name: 'file', filename: 'a.pdf', value: 'file' }],
    });
    const withFolder = documentUploadRequest('/v1/identity/me/inbox/documents', 'a.pdf', { folder_id: 'folder', name: 'A' });
    expect(withFolder.parts[1]).toEqual({ name: 'metadata', value: JSON.stringify({ folder_id: 'folder', name: 'A' }) });
  });

  it('replaces bytes with the resource version and downloads by item id', () => {
    expect(replaceContentRequest('item', 4, { name: 'a.pdf', type: '' })).toEqual({
      path: '/v1/items/item/content',
      method: 'PUT',
      ifMatch: 4,
      headers: { 'Content-Type': 'application/octet-stream', 'X-Filename': 'a.pdf' },
    });
    expect(downloadContentPath('item')).toBe('/v1/items/item/content');
  });
});
