import { describe, expect, it } from 'vitest';
import { bulkUpdateRequests, documentUploadRequest, downloadContentPath, listingRequest, replaceContentRequest, searchRequest, smartFolderQueryRequest, viewGroupsRequest, viewRowsRequest } from './requests';

describe('listing and search requests', () => {
  it('browses the open folder with an optional search when no view or sort applies', () => {
    expect(listingRequest('col', {})).toEqual({ path: '/v1/resources/col/children', method: 'GET', query: { q: undefined, $top: 50 } });
    expect(listingRequest('col', { folder: 'folder', q: 'invoice' })).toEqual({
      path: '/v1/resources/folder/children',
      method: 'GET',
      query: { q: 'invoice', $top: 50 },
    });
  });

  it('selects the columns chosen on the page instead of the view\'s', () => {
    const view = { columns: ['$name', 'status'], query: {} };
    expect(listingRequest('col', { view, columns: ['$name', 'amount'] }).body).toEqual({
      include_total: true,
      query: {},
      select: ['$name', 'amount'],
    });
  });

  it('sorts the open folder with a collection query', () => {
    expect(listingRequest('col', { sort: { field: 'amount', direction: 'desc' } })).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 50 },
      body: { include_total: true, query: { parent_id: 'col', sort: [{ field: 'amount', direction: 'desc' }] } },
    });
  });

  it('combines a saved view with the open folder, the search text and a column sort', () => {
    const view = {
      columns: ['$name', 'status'],
      query: { filter: { field: 'status', op: 'eq', value: 'Open' }, sort: [{ field: '$name', direction: 'asc' }], group_by: 'status' },
    };
    expect(listingRequest('col', { view })).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 50 },
      body: { include_total: true, query: view.query, select: ['$name', 'status'] },
    });
    expect(listingRequest('col', { view, folder: 'folder', q: 'invoice', sort: { field: '$modified_at', direction: 'desc' } }).body).toEqual({
      include_total: true,
      query: { ...view.query, parent_id: 'folder', search: 'invoice', sort: [{ field: '$modified_at', direction: 'desc' }] },
      select: ['$name', 'status'],
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
    expect(downloadContentPath('item', 'blob')).toBe('/v1/items/item/content?blob_id=blob');
  });
});

describe('bulk updates', () => {
  it('sends up to 1550 selected ids per request as groups of 50', () => {
    const ids = Array.from({ length: 1600 }, (_, i) => `id${i}`);
    const calls = bulkUpdateRequests('col', { ids }, { status: 'Paid' });
    expect(calls).toHaveLength(2);
    const first = calls[0].body as { query: { filter: { or: { value: string[] }[] } }; values: unknown };
    expect(first.query.filter.or).toHaveLength(31);
    expect(first.query.filter.or.every((part) => part.value.length === 50)).toBe(true);
    expect(first.values).toEqual({ status: 'Paid' });
    expect(calls[1].body).toEqual({ query: { filter: { field: '$id', op: 'in', value: ids.slice(1550, 1600) } }, values: { status: 'Paid' } });
  });

  it('updates everything a view matches without its sort or grouping', () => {
    const query = { filter: { field: 'status', op: 'eq', value: 'Open' }, sort: [{ field: 'due', direction: 'asc' }], group_by: 'status', search: 'acme' };
    expect(bulkUpdateRequests('col', { query }, { status: 'Paid' })).toEqual([
      {
        path: '/v1/resources/col/bulk-update',
        method: 'POST',
        body: { query: { filter: { field: 'status', op: 'eq', value: 'Open' }, search: 'acme' }, values: { status: 'Paid' } },
      },
    ]);
  });
});

describe('board and calendar requests', () => {
  it('narrows a view by one more condition and counts its groups', () => {
    const view = { columns: ['amount'], query: { filter: { field: 'amount', op: 'gt', value: 5 }, group_by: 'status', sort: [{ field: 'due', direction: 'asc' }] } };
    expect(viewRowsRequest('col', view, { field: 'status', op: 'eq', value: 'Open' }, 20)).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 20 },
      body: {
        query: {
          filter: { and: [view.query.filter, { field: 'status', op: 'eq', value: 'Open' }] },
          sort: [{ field: 'due', direction: 'asc' }],
        },
        select: ['$name', 'amount', 'status'],
      },
    });
    expect(viewGroupsRequest('col', view).body).toEqual({ query: { filter: view.query.filter, group_by: 'status' } });
  });
});
