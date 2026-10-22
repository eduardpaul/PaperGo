import { describe, expect, it } from 'vitest';
import { bulkUpdateRequests, countRequest, documentUploadRequest, downloadContentPath, listingQuery, listingRequest, replaceContentRequest, searchRequest, smartFolderQueryRequest, viewGroupsRequest, viewRowsRequest } from './requests';

describe('listing and search requests', () => {
  it('browses the open folder when no view, sort or search applies', () => {
    expect(listingRequest('col', {})).toEqual({ path: '/v1/resources/col/children', method: 'GET', query: { $top: 50 } });
    expect(listingRequest('col', { folder: 'folder' }).path).toBe('/v1/resources/folder/children');
  });

  it('searches the open folder and everything below it, or the whole list at the top', () => {
    expect(listingRequest('col', { folder: 'folder', q: 'invoice' })).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 50 },
      body: { query: { ancestor_id: 'folder', search: 'invoice' } },
    });
    expect(listingRequest('col', { q: 'invoice' }).body).toEqual({ query: { search: 'invoice' } });
  });

  it('selects the columns chosen on the page instead of the view\'s', () => {
    const view = { columns: ['$name', 'status'], query: {} };
    expect(listingRequest('col', { view, columns: ['$name', 'amount'] }).body).toEqual({
      query: {},
      select: ['$name', 'amount'],
    });
  });

  it('sorts the open folder with a collection query', () => {
    expect(listingRequest('col', { sort: { field: 'amount', direction: 'desc' } })).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 50 },
      body: { query: { parent_id: 'col', sort: [{ field: 'amount', direction: 'desc' }] } },
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
      body: { query: view.query, select: ['$name', 'status'] },
    });
    expect(listingRequest('col', { view, folder: 'folder', sort: { field: '$modified_at', direction: 'desc' } }).body).toEqual({
      query: { ...view.query, parent_id: 'folder', sort: [{ field: '$modified_at', direction: 'desc' }] },
      select: ['$name', 'status'],
    });
    expect(listingRequest('col', { view, folder: 'folder', q: 'invoice' }).body).toEqual({
      query: { ...view.query, ancestor_id: 'folder', search: 'invoice' },
      select: ['$name', 'status'],
    });
  });

  it('counts what a listing shows apart from its rows, up to a limit', () => {
    expect(listingQuery(listingRequest('col', { folder: 'folder' }), 'col')).toEqual({ parent_id: 'folder' });
    expect(countRequest('col', { search: 'acme', sort: [{ field: 'amount', direction: 'asc' }], group_by: 'status' })).toEqual({
      path: '/v1/resources/col/query',
      method: 'POST',
      query: { $top: 1 },
      body: { total_limit: 100000, query: { search: 'acme' }, select: [] },
    });
  });

  it('searches every readable workspace, narrowed by facets', () => {
    expect(searchRequest({ q: 'invoice', workspaceId: 'ws', keyword: 'paid', kind: 'all' })).toEqual({
      path: '/v1/search',
      method: 'GET',
      query: { q: 'invoice', workspace_id: 'ws', collection_id: undefined, keyword: 'paid', kind: 'item,folder', $top: 25 },
    });
    expect(searchRequest({ q: 'invoice' }).query?.kind).toBeUndefined();
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
    expect(calls[1].body).toEqual({ query: { archive: 'all', filter: { field: '$id', op: 'in', value: ids.slice(1550, 1600) } }, values: { status: 'Paid' } });
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
