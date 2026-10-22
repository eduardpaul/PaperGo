import { useQueries, useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { Archive, Download } from 'lucide-react';
import { useState } from 'react';
import { api } from '@/api/http';
import { allPages, useApiPages } from '@/api/pages';
import { resourceQuery, workspacesQuery } from '@/api/queries';
import type { Resource, SearchHit } from '@/api/types';
import { KeywordInput } from '@/components/keyword-input';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { downloadContent } from '@/features/documents/transfer';
import { FieldEditor } from '@/features/fields/editor';
import { useResourceNames } from '@/features/fields/names';
import { usePeopleNames } from '@/features/fields/people';
import { can } from '@/features/lists/access';
import { useArchiveActions } from '@/features/lists/archive-actions';
import { useFormat } from '@/lib/preferences';
import { useSearchAvailable } from '@/lib/search';
import { useDebounced } from '@/lib/use-debounced';

export const Route = createFileRoute('/_app/archive')({ component: ArchivePage });
interface ArchiveEntry extends Resource { can_write: boolean }

function ArchivePage() {
  const [filters, setFilters] = useState({ q: '', workspace: '', collection: '', type: '', keyword: '', by: '', from: '', to: '', mode: 'name' });
  const [selected, setSelected] = useState<Map<string, Resource>>(new Map());
  const searchAvailable = useSearchAvailable();
  const format = useFormat();
  const people = usePeopleNames();
  const workspaces = useQuery(workspacesQuery);
  const collections = useQuery({ queryKey: ['archive-collections', filters.workspace], enabled: !!filters.workspace, queryFn: async () => (await allPages<Resource>(`/v1/resources/${filters.workspace}/children`)).filter(resource => resource.kind === 'list' || resource.kind === 'library') });
  const types = useQuery({ queryKey: ['archive-content-types', filters.workspace, filters.collection], queryFn: async () => (await api<{ id: string; name: string }[]>('/v1/search/content-types', { query: { workspace_id: filters.workspace || undefined, collection_id: filters.collection || undefined } })).data });
  const fullText = filters.mode === 'fulltext' && searchAvailable;
  const text = useDebounced(filters.q.trim(), 300);
  const query = {
    q: text || undefined, workspace_id: filters.workspace || undefined,
    collection_id: filters.collection || undefined, content_type_id: filters.type || undefined,
    keyword: filters.keyword || undefined, archived_by: filters.by || undefined,
    archived_from: filters.from ? `${filters.from}T00:00:00Z` : undefined,
    archived_to: filters.to ? `${filters.to}T23:59:59.999999999Z` : undefined,
  };
  const browse = useApiPages<ArchiveEntry>(['archive'], '/v1/archive', { query, enabled: !fullText });
  const search = useApiPages<SearchHit>(['archive', 'fulltext'], '/v1/search', { query: { ...query, archive: 'archived', kind: 'item' }, enabled: fullText });
  // Search hits are re-read for current location, version, and allowed actions.
  const details = useQueries({ queries: (fullText ? search.items : []).map(hit => resourceQuery(hit.id)) });
  const rows: ArchiveEntry[] = fullText
    ? details.flatMap(result => result.data?.archived ? [{ ...result.data, can_write: can(result.data, 'write') }] : [])
    : browse.items;
  const listing = fullText ? search : browse;
  const collectionIds = [...new Set(rows.map(row => row.container_id).filter((id): id is string => !!id))].sort();
  const collectionDetails = useQueries({ queries: collectionIds.map(id => resourceQuery(id)) });
  const isLibrary = (id?: string) => collectionDetails.some(result => result.data?.id === id && result.data?.kind === 'library');
  const ids = [...new Set(rows.flatMap(row => [row.workspace_id, row.container_id, row.parent_id].filter((id): id is string => !!id)))].sort();
  const names = useResourceNames(ids);
  const workspaceName = (id: string) => {
    const workspace = workspaces.data?.find(workspace => workspace.id === id);
    return workspace?.personal_owner_id ? 'Home' : workspace?.name ?? names.get(id) ?? 'Workspace';
  };
  const archive = useArchiveActions(completed => setSelected(current => new Map([...current].filter(([id]) => !completed.includes(id)))));
  const setFilter = (key: keyof typeof filters, value: string) => {
    setSelected(new Map());
    setFilters(current => ({ ...current, [key]: value, ...(key === 'workspace' ? { collection: '', type: '' } : key === 'collection' ? { type: '' } : {}) }));
  };
  const toggle = (row: Resource) => setSelected(current => { const next = new Map(current); if (next.has(row.id)) next.delete(row.id); else next.set(row.id, row); return next; });
  return <Page>
    <PageHeader icon={Archive} title="Archive" description="Archived documents and list items stay in their original locations. Find them here, edit them, or return them to active views." />
    <div role="search" className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <div><Label htmlFor="archive-q">Find archived items</Label><Input id="archive-q" value={filters.q} placeholder={fullText ? 'Search names, properties, and file content' : 'Search by name'} onChange={event => setFilter('q', event.target.value)} /></div>
      <div><Label htmlFor="archive-mode">Search in</Label><Select id="archive-mode" value={fullText ? 'fulltext' : 'name'} onChange={event => setFilter('mode', event.target.value)}><option value="name">Names</option>{searchAvailable && <option value="fulltext">Full text</option>}</Select></div>
      <div><Label htmlFor="archive-workspace">Workspace</Label><Select id="archive-workspace" value={filters.workspace} onChange={event => setFilter('workspace', event.target.value)}><option value="">All workspaces</option>{workspaces.data?.map(workspace => <option key={workspace.id} value={workspace.id}>{workspaceName(workspace.id)}</option>)}</Select></div>
      <div><Label htmlFor="archive-collection">List or library</Label><Select id="archive-collection" value={filters.collection} disabled={!filters.workspace} onChange={event => setFilter('collection', event.target.value)}><option value="">{filters.workspace ? 'All lists and libraries' : 'Choose a workspace first'}</option>{collections.data?.map(collection => <option key={collection.id} value={collection.id}>{collection.name}</option>)}</Select></div>
      <div><Label htmlFor="archive-type">Content type</Label><Select id="archive-type" value={filters.type} onChange={event => setFilter('type', event.target.value)}><option value="">All content types</option>{types.data?.map(type => <option key={type.id} value={type.id}>{type.name}</option>)}</Select></div>
      <div><Label htmlFor="archive-keyword">Keyword</Label><KeywordInput limit={1} id="archive-keyword" value={filters.keyword ? [filters.keyword] : []} onChange={values => setFilter('keyword', values.at(-1) ?? '')} /></div>
      <div><FieldEditor id="archive-by" field={{ id: '$archived_by', key: '$archived_by', label: 'Archived by', type: 'person' }} value={filters.by || null} onChange={value => setFilter('by', typeof value === 'string' ? value : '')} /></div>
      <div className="flex gap-2"><div className="min-w-0 flex-1"><Label htmlFor="archive-from">Archived from (UTC)</Label><Input type="date" id="archive-from" value={filters.from} onChange={event => setFilter('from', event.target.value)} /></div><div className="min-w-0 flex-1"><Label htmlFor="archive-to">Through (UTC)</Label><Input type="date" id="archive-to" value={filters.to} onChange={event => setFilter('to', event.target.value)} /></div></div>
    </div>
    {fullText && <p className="mb-3 text-sm text-muted">File-content searches follow search indexing. Use Names to browse newly archived items immediately.</p>}
    <QueryFeedback query={listing} />
    {filters.workspace && <QueryFeedback query={collections} />}
    <QueryFeedback query={types} />
    {details.filter(result => result.isError).map((result, index) => <QueryFeedback key={index} query={result} />)}
    {selected.size > 0 && <div className="mb-3 flex items-center gap-3 rounded-lg bg-accent-soft p-3 text-sm"><span>{selected.size} selected</span><Button size="sm" disabled={archive.isPending} onClick={() => archive.mutate({ items: [...selected.values()], archived: false })}>Unarchive selected</Button><Button size="sm" variant="ghost" onClick={() => setSelected(new Map())}>Clear selection</Button></div>}
    {listing.isPending || (fullText && details.some(result => result.isPending)) ? <Skeleton className="h-24" /> : rows.length === 0 && !listing.isError ? <EmptyState icon={Archive} title="No archived items match" /> : rows.length > 0 && <div className="overflow-x-auto rounded-lg border"><table className="w-full text-left text-sm"><thead className="border-b bg-surface-muted text-muted"><tr><th className="p-3"><span className="sr-only">Select</span></th><th className="p-3">Name</th><th className="p-3">Original location</th><th className="p-3">Archived</th><th className="p-3">Archived by</th><th className="p-3">Actions</th></tr></thead><tbody>
      {rows.map(row => <tr key={row.id} className="border-b last:border-0"><td className="p-3"><Checkbox aria-label={`Select ${row.name}`} disabled={!row.can_write || archive.isPending} checked={selected.has(row.id)} onChange={() => toggle(row)} /></td><td className="p-3 font-medium"><Link className="text-accent hover:underline" to="/w/$workspaceId/l/$listId" params={{ workspaceId: row.workspace_id, listId: row.container_id! }} search={{ view: 'archived', item: row.id }}>{row.name}</Link></td><td className="p-3 text-muted"><Link className="hover:underline" to="/w/$workspaceId/l/$listId" params={{ workspaceId: row.workspace_id, listId: row.container_id! }} search={{ view: 'archived', folder: row.parent_id !== row.container_id ? row.parent_id : undefined }}>{workspaceName(row.workspace_id)} / {names.get(row.container_id!) ?? 'List or library'}{row.parent_id && row.parent_id !== row.container_id ? ` / ${names.get(row.parent_id) ?? 'Folder'}` : ''}</Link></td><td className="whitespace-nowrap p-3 text-muted">{row.archived_at ? format.dateTime(row.archived_at) : '—'}</td><td className="p-3 text-muted">{row.archived_by ? people.get(row.archived_by) ?? row.archived_by : '—'}</td><td className="p-3"><div className="flex gap-2">{isLibrary(row.container_id) && <Button size="sm" variant="ghost" aria-label={`Download ${row.name}`} onClick={() => void downloadContent(row.id, row.name)}><Download /></Button>}{row.can_write && <Button size="sm" disabled={archive.isPending} onClick={() => archive.mutate({ items: [row], archived: false })}>Unarchive</Button>}</div></td></tr>)}
    </tbody></table></div>}
    {listing.hasNextPage && <Button className="mt-3" disabled={listing.isFetchingNextPage} onClick={() => void listing.fetchNextPage()}>{listing.isFetchingNextPage ? 'Loading…' : 'More items'}</Button>}
  </Page>;
}
