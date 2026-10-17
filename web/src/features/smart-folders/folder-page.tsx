import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type ReactElement } from 'react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { keys, meQuery, resourceQuery, smartFolder } from '@/api/queries';
import type { Page, Resource, SmartFolderEntry, SmartFolderGroup } from '@/api/types';
import { smartFolderQueryRequest } from '@/api/requests';
import type { ListingSort } from '@/api/requests';
import { Page as Screen, PageHeader } from '@/components/page';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { useValueNames } from '@/features/fields/names';
import { can } from '@/features/lists/access';
import { ItemContextMenu } from '@/features/lists/item-context-menu';
import { ItemsGallery } from '@/features/lists/items-layouts';
import { ItemsTable } from '@/features/lists/items-table';
import { ConfirmDialog } from '@/features/settings/section';
import { ClassifyDialog } from './classification';
import { SmartFolderDialog } from './editor-dialog';
import { RuleSummary } from './rule-summary';

export function SmartFolderPage({ folderId, edit = false }: { folderId: string; edit?: boolean }) {
  return <FolderContents key={folderId} folderId={folderId} edit={edit} />;
}
function FolderContents({ folderId, edit }: { folderId: string; edit: boolean }) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const folder = useQuery({ queryKey: ['smart-folders', folderId], queryFn: () => smartFolder(folderId) });
  const me = useQuery(meQuery);
  const workspace = useQuery({ ...resourceQuery(folder.data?.workspace_id ?? ''), enabled: !!folder.data?.workspace_id && !folder.data.owner_id });
  const writable = me.data?.scopes?.includes('list.write') ?? false;
  const canManage = writable && (folder.data?.owner_id ? folder.data.owner_id === me.data?.subject : can(workspace.data, 'manage'));
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [classifying, setClassifying] = useState(false);
  const [unclassifying, setUnclassifying] = useState<SmartFolderEntry>();
  const [layout, setLayout] = useState('table');
  const [grouping, setGrouping] = useState(false);
  const [path, setPath] = useState<{ value: string | null; label: string }[]>([]);
  const [sort, setSort] = useState<ListingSort>({ field: '$modified_at', direction: 'desc' });
  const groupBy = folder.data?.definition.group_by ?? [];
  const grouped = grouping && path.length < groupBy.length;
  const activePath = grouping ? path.map(segment => segment.value) : [];
  const call = smartFolderQueryRequest(folderId, activePath, sort);
  const entries = useApiPages<SmartFolderEntry>(['smart-folders', folderId, 'entries'], call.path, { enabled: !!folder.data && !grouped, query: call.query, body: call.body });
  const groups = useApiPages<SmartFolderGroup>(['smart-folders', folderId, 'groups'], `/v1/smart-folders/${folderId}/query/groups`, { enabled: !!folder.data && grouped, body: { path: activePath } });
  const names = useValueNames([], entries.items.map(entry => entry.item));
  const refresh = async () => { await client.invalidateQueries({ queryKey: keys.smartFolders }); await client.invalidateQueries({ queryKey: keys.resources }); };
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/smart-folders/${folderId}`, { method: 'DELETE', ifMatch: folder.data!.version }),
    onSuccess: async () => { await refresh(); void navigate({ to: '/' }); },
  });
  const unclassify = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/smart-folders/${folderId}/items/${unclassifying!.item.id}`, { method: 'DELETE', ifMatch: unclassifying!.item.version, body: { folder_version: folder.data!.version } }),
    onSuccess: async () => { setUnclassifying(undefined); await refresh(); },
  });
  const closeEditor = () => {
    setEditing(false);
    if (edit) void navigate({ to: '/f/$folderId', params: { folderId }, search: { edit: undefined }, replace: true });
  };
  if (folder.isPending) return <Screen><Skeleton className="h-16" /></Screen>;
  if (!folder.data) return <Screen><QueryFeedback query={folder} /><EmptyState title="Smart folder unavailable" /></Screen>;
  const current = folder.data;
  const classification = current.definition.mode === 'classification';
  const byItem = new Map(entries.items.map(entry => [entry.item.id, entry]));
  const provenance = new Map(entries.items.map(entry => [entry.item.id, entry.collection_name]));
  const open = (item: Resource, tab?: string) => {
    const entry = byItem.get(item.id)!;
    void navigate({ to: '/w/$workspaceId/l/$listId', params: { workspaceId: entry.workspace_id, listId: entry.collection_id }, search: item.kind === 'folder' && !tab ? { folder: item.id } : { item: item.id, tab } });
  };
  const renderItem = (item: Resource, element: ReactElement) => {
    const entry = byItem.get(item.id)!;
    const collection: Resource = { id: entry.collection_id, workspace_id: entry.workspace_id, kind: entry.collection_kind, name: entry.collection_name, version: 1 };
    return <ItemContextMenu key={item.id} item={item} collection={collection} onOpen={open} onDetails={(item, tab) => open(item, tab ?? 'details')}>{element}</ItemContextMenu>;
  };
  const total = (entries.data?.pages[0] as Page<SmartFolderEntry> | undefined)?.total;
  return <Screen>
    <PageHeader title={<span className="flex flex-wrap items-center gap-2">{current.name}<Badge>{classification ? 'Classification folder' : 'Saved search'}</Badge></span>} description={current.description || (current.owner_id ? 'Personal smart folder' : 'Shared smart folder')} actions={<>
      {classification && writable && <Button variant="primary" onClick={() => setClassifying(true)}>Classify item…</Button>}
      {canManage && <><Button onClick={() => setEditing(true)}>Edit rules…</Button><Button variant="ghost" onClick={() => setDeleting(true)}>Delete…</Button></>}
    </>} />
    <RuleSummary folder={current} />
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <Select aria-label="Results layout" value={layout} onChange={event => setLayout(event.target.value)}><option value="table">Table</option><option value="gallery">Grid</option></Select>
      {!grouped && <Select aria-label="Sort results" value={`${sort.field}:${sort.direction}`} onChange={event => { const [field, direction] = event.target.value.split(':'); setSort({ field, direction: direction as 'asc' | 'desc' }); }}><option value="$modified_at:desc">Modified — newest first</option><option value="$modified_at:asc">Modified — oldest first</option><option value="$name:asc">Name — A to Z</option><option value="$name:desc">Name — Z to A</option><option value="$created_at:desc">Created — newest first</option><option value="$created_at:asc">Created — oldest first</option></Select>}
      {!!groupBy.length && <Button onClick={() => { setGrouping(!grouping); setPath([]); }}>{grouping ? 'Show all results' : 'Browse groups'}</Button>}
      {total !== undefined && !grouped && <span className="text-xs text-muted">{total} matching items</span>}
    </div>
    {grouping && <nav aria-label="Group path" className="mb-3 flex flex-wrap gap-2 text-sm"><button type="button" className="text-accent" onClick={() => { setGrouping(false); setPath([]); }}>All results</button><button type="button" className="text-accent" onClick={() => setPath([])}>Groups</button>{path.map((segment, index) => <button type="button" key={index} onClick={() => setPath(path.slice(0, index + 1))}>/ {segment.label}</button>)}</nav>}
    <QueryFeedback query={grouped ? groups : entries} />
    {grouped ? <><ul className="divide-y rounded-lg border bg-surface">{groups.items.map(group => <li key={String(group.value)}><button type="button" className="flex w-full justify-between px-4 py-3 text-left text-sm hover:bg-surface-muted" onClick={() => setPath([...path, { value: group.value == null ? null : String(group.value), label: group.label || String(group.value ?? 'No value') }])}><span>{group.label || String(group.value ?? 'No value')}</span><Badge>{group.count}</Badge></button></li>)}</ul>{groups.isSuccess && !groups.items.length && <EmptyState title="No matching groups" />}{groups.hasNextPage && <Button className="mt-3" disabled={groups.isFetchingNextPage} onClick={() => void groups.fetchNextPage()}>More groups</Button>}</> : <>
      {entries.items.length > 0 && (layout === 'gallery' ? <ItemsGallery previewItems={new Set(entries.items.filter(entry => entry.collection_kind === 'library').map(entry => entry.item.id))} items={entries.items.map(entry => entry.item)} fields={[]} provenance={provenance} onOpen={open} renderItem={renderItem} /> : <ItemsTable rows={entries.items.map(entry => entry.item)} columns={['$name', '$modified_at', '$keywords']} fields={[]} publishing={false} names={names} selectable={false} selected={new Map()} sortable={new Set(['$name', '$modified_at'])} sort={sort} onSort={next => setSort(next ?? { field: '$modified_at', direction: 'desc' })} onToggle={() => {}} onToggleAll={() => {}} onOpen={open} provenance={provenance} renderRow={renderItem} />)}
      {entries.isSuccess && !entries.items.length && <EmptyState title="No matching items">Matching items appear here automatically as their information changes.</EmptyState>}
      {classification && writable && entries.items.length > 0 && <details className="mt-4 text-sm"><summary className="cursor-pointer">Remove a classification</summary><ul>{entries.items.filter(entry => entry.item.kind === 'item').map(entry => <li key={entry.item.id} className="flex items-center justify-between gap-2 py-1"><span>{entry.item.name}</span><Button size="sm" onClick={() => setUnclassifying(entry)}>Remove classification</Button></li>)}</ul></details>}
      {entries.hasNextPage && <Button className="mt-3" disabled={entries.isFetchingNextPage} onClick={() => void entries.fetchNextPage()}>More results</Button>}
    </>}
    <SmartFolderDialog open={editing || (edit && canManage)} folder={current} onClose={closeEditor} onSaved={closeEditor} />
    {classifying && <ClassifyDialog folder={current} path={activePath} onClose={() => setClassifying(false)} />}
    <ConfirmDialog open={deleting} onOpenChange={setDeleting} title={`Delete “${current.name}”?`} description="Removes the saved rules. Items stay in their collections." confirm="Delete smart folder" busy={remove.isPending} error={remove.error} onConfirm={() => remove.mutate()} />
    <ConfirmDialog open={!!unclassifying} onOpenChange={value => !value && setUnclassifying(undefined)} title={`Remove classification from “${unclassifying?.item.name ?? ''}”?`} description="Clears the folder's keywords and equality values. The item stays in its collection; other rules may still match it." confirm="Remove classification" busy={unclassify.isPending} error={unclassify.error} onConfirm={() => unclassify.mutate()} />
  </Screen>;
}
