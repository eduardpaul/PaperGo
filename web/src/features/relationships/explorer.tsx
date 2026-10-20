import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ArrowLeft, ArrowRight, ArrowUpRight, ChevronRight, CircleDot, GitFork, List, Network, SlidersHorizontal, X } from 'lucide-react';
import { api } from '@/api/http';
import { resourceQuery } from '@/api/queries';
import type { Page as ApiPage, Relationship } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetTitle } from '@/components/ui/sheet';
import { useHasScope } from '@/features/admin/common';
import { problemMessage } from '@/lib/errors';
import { useDebounced } from '@/lib/use-debounced';
import { cn } from '@/lib/utils';
import { connectionLabel, explorerQuery, locationLabel, type Connection, type ConnectionType, type Endpoint, type ExplorerSearch } from './model';
import { useExplorerPages } from './queries';

const RelationshipGraph = lazy(() => import('./graph'));
const scrollPositions = new Map<string, number>();

export function RelationshipsExplorer({ workspaceId, search, onSearch }: {
  workspaceId: string;
  search: ExplorerSearch;
  onSearch: (patch: Partial<ExplorerSearch>, replace?: boolean) => void;
}) {
  const base = `/v1/workspaces/${workspaceId}/relationships`;
  const workspace = useQuery(resourceQuery(workspaceId));
  const canReadTypes = useHasScope('taxonomy.read');
  const [typeSearch, setTypeSearch] = useState('');
  const [itemSearch, setItemSearch] = useState('');
  const [typesOpen, setTypesOpen] = useState(false);
  const typeQ = useDebounced(typeSearch);
  const itemQ = useDebounced(itemSearch);
  const query = explorerQuery(search);
  const types = useExplorerPages<ConnectionType>(`${base}/types`, { q: typeQ, collection_id: search.collection, scope: search.scope }, canReadTypes);
  const suggestions = useExplorerPages<Endpoint>(`${base}/items`, { ...query, item_id: undefined, direction: undefined, q: itemQ }, !!itemQ.trim());
  const connections = useExplorerPages<Connection>(base, query, search.view !== 'graph');
  const focused = useQuery({
    queryKey: ['relationship-explorer-focus', workspaceId, search.item],
    enabled: !!search.item,
    gcTime: 60_000,
    queryFn: async ({ signal }) => (await api<ApiPage<Endpoint>>(`${base}/items`, { signal, query: { item_id: search.item, $top: 1 } })).data.value?.[0],
  });
  const selectedEdge = useQuery({
    queryKey: ['relationship-explorer-edge', workspaceId, search.edge],
    enabled: !!search.edge,
    queryFn: async ({ signal }) => (await api<ApiPage<Connection>>(base, { signal, query: { $filter: `id eq ${search.edge}`, $top: 1 } })).data.value?.[0],
  });
  const edge = selectedEdge.data;
  const known = new Map<string, Endpoint>();
  connections.items.forEach(connection => { known.set(connection.source_id, connection.source_item); known.set(connection.target_id, connection.target_item); });
  if (focused.data) known.set(focused.data.id, focused.data);
  if (edge) { known.set(edge.source_id, edge.source_item); known.set(edge.target_id, edge.target_item); }
  const focus = focused.data ?? known.get(search.item ?? '');
  const changeFilters = (patch: Partial<ExplorerSearch>) => onSearch({ ...patch, edge: undefined });
  const explore = (item: Endpoint) => {
    if (item.id === search.item) return;
    onSearch({ item: item.id, edge: undefined, direction: undefined, trail: [...(search.trail ?? []), ...(search.item ? [search.item] : [])].slice(-12) });
  };
  const back = () => {
    const trail = [...(search.trail ?? [])];
    onSearch({ item: trail.pop(), trail: trail.length ? trail : undefined, edge: undefined, direction: undefined });
  };
  const reset = () => onSearch({ item: undefined, trail: undefined, direction: undefined, edge: undefined });
  const typeName = search.type === 'untyped' ? 'Untyped' : types.items.find(type => type.id === search.type)?.name;
  const collections = useExplorerPages<{ id: string; name: string; kind: string }>(`/v1/resources/${workspaceId}/children`, { $top: 100 });
  const [wide, setWide] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia('(min-width: 1440px)').matches);
  useEffect(() => {
    const media = window.matchMedia('(min-width: 1440px)');
    const update = () => setWide(media.matches);
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, []);

  const typePicker = <div className="flex h-full min-h-0 flex-col gap-3 p-4">
    <div><h2 className="font-medium">Relationship types</h2><p className="mt-1 text-xs text-muted">Choose a connection to explore.</p></div>
    {canReadTypes && <Input aria-label="Filter relationship types" placeholder="Filter types…" value={typeSearch} onChange={event => setTypeSearch(event.target.value)} />}
    <nav aria-label="Relationship types" className="min-h-0 flex-1 overflow-y-auto">
      <TypeButton active={!search.type} onClick={() => { changeFilters({ type: undefined }); setTypesOpen(false); }}>All relationships</TypeButton>
      <TypeButton active={search.type === 'untyped'} onClick={() => { changeFilters({ type: 'untyped' }); setTypesOpen(false); }}>Untyped<span className="text-xs text-muted">No type assigned</span></TypeButton>
      {canReadTypes && <div className="mt-3 border-t pt-2">
        <QueryState query={types} />
        {types.items.map(type => <TypeButton key={type.id} active={search.type === type.id} onClick={() => { changeFilters({ type: type.id }); setTypesOpen(false); }}>
          {type.name}<span className="text-xs text-muted">{type.directed ? type.inverse_label || 'Directed' : 'Symmetric'}</span>
        </TypeButton>)}
        {types.isSuccess && !types.items.length && <p className="p-2 text-xs text-muted">{typeQ ? 'No matching types.' : 'No typed connections here.'}</p>}
        {types.hasNextPage && <Button className="mt-2 w-full" size="sm" onClick={() => void types.fetchNextPage()} disabled={types.isFetchingNextPage}>More types</Button>}
      </div>}
    </nav>
  </div>;

  const inspector = search.edge ? <RelationshipInspector edge={edge} pending={selectedEdge.isPending} error={selectedEdge.error} retry={() => void selectedEdge.refetch()} workspaceId={workspaceId} onExplore={explore} /> : focus ? <ItemInspector item={focus} workspaceId={workspaceId} workspaceName={workspace.data?.name ?? 'this workspace'} onExplore={explore} /> : null;

  return <Page wide>
    <Link to="/w/$workspaceId" params={{ workspaceId }} className="mb-3 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground"><ArrowLeft className="size-3.5" />{workspace.data?.name ?? 'Workspace'}</Link>
    <PageHeader icon={GitFork} title="Relationships" description="Follow the connections between your workspace’s items." />
    <QueryState query={workspace} />
    <div className="mb-4 flex flex-wrap items-center gap-2">
      <Button className="xl:hidden" onClick={() => setTypesOpen(true)}><SlidersHorizontal />{typeName ?? 'Relationship types'}</Button>
      <div className="min-w-56 flex-1 xl:max-w-md">
        <span id="relationship-item-search" className="sr-only">Find an item to explore</span>
        <Combobox selected={[]} aria-labelledby="relationship-item-search" options={suggestions.items.map(item => ({ value: item.id, label: item.name, hint: locationLabel(item, workspaceId) }))} placeholder="Find an item by name…" onSearch={setItemSearch} loading={suggestions.isFetching || itemSearch !== itemQ} onChange={options => {
          const item = suggestions.items.find(item => item.id === options[0]?.value);
          if (item) explore(item);
          setItemSearch('');
        }} footer={suggestions.error ? <button onClick={() => void suggestions.refetch()} className="text-danger">Search failed. Retry</button> : suggestions.hasNextPage ? <button onClick={() => void suggestions.fetchNextPage()} disabled={suggestions.isFetchingNextPage}>More matches</button> : 'Search item names from the beginning.'} />
      </div>
      <Select aria-label="Filter collection" className="w-auto max-w-60" value={search.collection ?? ''} onChange={event => changeFilters({ collection: event.target.value || undefined })}>
        <option value="">All lists and libraries</option>
        {collections.items.filter(item => item.kind === 'list' || item.kind === 'library').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </Select>
      {collections.hasNextPage && <Button size="sm" disabled={collections.isFetchingNextPage} onClick={() => void collections.fetchNextPage()}>More collections</Button>}
      <Select aria-label="Filter workspace scope" className="w-auto" value={search.scope ?? ''} onChange={event => changeFilters({ scope: event.target.value as ExplorerSearch['scope'] || undefined })}>
        <option value="">All connections</option><option value="internal">Within this workspace</option><option value="external">Across workspaces</option>
      </Select>
    </div>
    {collections.error && <QueryState query={collections} />}
    <div className="grid min-h-[560px] overflow-hidden rounded-lg border bg-surface xl:grid-cols-[200px_minmax(0,1fr)]">
      <aside className="hidden min-h-0 border-r xl:block">{typePicker}</aside>
      <div className="flex min-w-0">
        <section aria-label="Connections" className="flex min-w-0 flex-1 flex-col">
          <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
            {search.item && <Button variant="ghost" size="icon" aria-label="Back in exploration" onClick={back}><ArrowLeft /></Button>}
            <div className="min-w-0 flex-1">
              <nav aria-label="Exploration trail" className="flex items-center gap-1 text-xs text-muted">
                <button onClick={reset} className="hover:text-accent">Workspace connections</button>
                {(search.trail?.length ?? 0) > 0 && <><ChevronRight className="size-3" /><span>{search.trail!.length} {search.trail!.length === 1 ? 'step' : 'steps'}</span></>}
              </nav>
              <h2 className="mt-1 truncate font-medium">{search.item ? focus?.name ?? (focused.isPending ? 'Loading item…' : 'Item unavailable') : typeName ?? 'All relationships'}</h2>
              {focus && <p className="mt-0.5 truncate text-xs text-muted">{locationLabel(focus, workspaceId)}{focus.workspace_id !== workspaceId && ` · Showing connections to ${workspace.data?.name ?? 'this workspace'}`}</p>}
            </div>
            {search.item && <Select aria-label="Filter direction" className="w-auto" value={search.direction ?? ''} onChange={event => changeFilters({ direction: event.target.value as ExplorerSearch['direction'] || undefined })}>
              <option value="">All directions</option><option value="incoming">Incoming</option><option value="outgoing">Outgoing</option><option value="symmetric">Symmetric</option>
            </Select>}
            <div role="group" aria-label="Connection view" className="flex rounded-md border p-0.5">
              <Button size="sm" variant={search.view !== 'graph' ? 'secondary' : 'ghost'} aria-pressed={search.view !== 'graph'} onClick={() => onSearch({ view: undefined })}><List />List</Button>
              <Button size="sm" variant={search.view === 'graph' ? 'secondary' : 'ghost'} aria-pressed={search.view === 'graph'} onClick={() => onSearch({ view: 'graph' })}><Network />Graph</Button>
            </div>
          </div>
          {search.item && !focused.isPending && !focus && <EmptyState icon={CircleDot} title="This item is unavailable here"><p>It may have moved, become unreadable, or no longer connect to this workspace.</p><Button className="mt-3" onClick={reset}>Workspace connections</Button></EmptyState>}
          {search.view === 'graph' ? search.item && focus ? <Suspense fallback={<Skeleton className="m-4 h-80" />}><RelationshipGraph workspaceId={workspaceId} search={search} focus={focus} onExplore={explore} onInspect={edge => onSearch({ edge: edge.id })} onList={() => onSearch({ view: undefined })} /></Suspense> : !search.item ? <EmptyState icon={Network} title="Start with an item"><p>Find an item above, or choose one in the list to see its immediate connections.</p><Button className="mt-3" onClick={() => onSearch({ view: undefined })}>Browse connections</Button></EmptyState> : null : <>
            <QueryState query={connections} />
            {connections.isSuccess && !connections.items.length ? <EmptyState icon={GitFork} title={search.item || search.type || search.collection || search.scope ? 'No connections match' : 'No relationships yet'}>
              <p>{search.item || search.type || search.collection || search.scope ? 'Try another type or broaden your filters.' : 'Relationships added to items will appear here. Start from an item’s Relationships tab.'}</p>
              {search.type && <Button className="mt-3" onClick={() => changeFilters({ type: undefined })}>Show all relationship types</Button>}
              {(search.collection || search.scope || search.direction) && <Button className="mt-3 ml-2" onClick={() => changeFilters({ collection: undefined, scope: undefined, direction: undefined })}>Clear filters</Button>}
            </EmptyState> : <ConnectionList key={JSON.stringify(query)} items={connections.items} workspaceId={workspaceId} focus={search.item} selected={search.edge} onExplore={explore} onInspect={edge => onSearch({ edge: edge.id })} cacheKey={`${workspaceId}:${JSON.stringify(query)}`} />}
            <div className="flex items-center justify-between gap-3 border-t px-4 py-3">
              <span className="text-xs text-muted" role="status">{connections.items.length.toLocaleString()} connections loaded{connections.data?.pages.length === 20 ? ' · Most recent pages' : ''}</span>
              <div className="flex gap-2">{connections.data?.pages.length === 20 && <Button size="sm" onClick={() => void connections.refetch()}>Refresh loaded pages</Button>}{connections.hasNextPage && <Button size="sm" disabled={connections.isFetchingNextPage} onClick={() => void connections.fetchNextPage()}>{connections.isFetchingNextPage ? 'Loading…' : 'Load more'}</Button>}</div>
            </div>
          </>}
          {!search.edge && focus && !wide && <div className="border-t p-3"><OpenItem item={focus} /></div>}
        </section>
        {wide && inspector && <aside aria-label="Inspector" className="w-80 shrink-0 border-l"><div className="flex items-center justify-between border-b px-4 py-3"><h2 className="font-medium">{search.edge ? 'Relationship details' : 'Item details'}</h2>{search.edge && <Button variant="ghost" size="icon" aria-label="Close relationship details" onClick={() => onSearch({ edge: undefined })}><X /></Button>}</div>{inspector}</aside>}
      </div>
    </div>
    <Sheet open={typesOpen} onOpenChange={setTypesOpen}><SheetContent className="md:w-80" aria-describedby={undefined}><div className="flex items-center justify-between border-b p-4"><SheetTitle>Browse types</SheetTitle><SheetClose /></div>{typePicker}</SheetContent></Sheet>
    <Sheet open={!!search.edge && !wide} onOpenChange={open => { if (!open) onSearch({ edge: undefined }); }}><SheetContent className="md:w-96" aria-describedby={undefined}><div className="flex items-center justify-between border-b p-4"><SheetTitle>Relationship details</SheetTitle><SheetClose /></div><div className="overflow-y-auto">{inspector}</div></SheetContent></Sheet>
  </Page>;
}

function TypeButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return <button onClick={onClick} aria-current={active ? 'true' : undefined} className={cn('my-0.5 flex w-full flex-col gap-0.5 rounded-md px-3 py-2 text-left text-[13px] hover:bg-surface-muted', active && 'bg-accent-soft text-accent font-medium')}>
    {children}
  </button>;
}

export function QueryState({ query }: { query: { isPending: boolean; error: unknown; refetch?: () => unknown } }) {
  if (query.error) return <div role="alert" className="m-3 rounded-md bg-danger-soft p-3 text-sm text-danger">{problemMessage(query.error)}{query.refetch && <Button variant="ghost" size="sm" className="ml-2" onClick={() => void query.refetch?.()}>Retry</Button>}</div>;
  return query.isPending ? <div aria-label="Loading connections" className="space-y-2 p-3"><Skeleton className="h-10" /><Skeleton className="h-10" /></div> : null;
}

function ConnectionList({ items, workspaceId, focus, selected, onExplore, onInspect, cacheKey }: { items: Connection[]; workspaceId: string; focus?: string; selected?: string; onExplore: (item: Endpoint) => void; onInspect: (edge: Connection) => void; cacheKey: string }) {
  const parent = useRef<HTMLDivElement>(null);
  // eslint-disable-next-line react-hooks/incompatible-library -- the compiler leaves this list unmemoized, which the virtualizer needs
  const virtual = useVirtualizer({ count: items.length, getScrollElement: () => parent.current, estimateSize: () => 112, overscan: 5, initialRect: { width: 800, height: 560 }, initialOffset: () => scrollPositions.get(cacheKey) ?? 0, getItemKey: index => items[index].id });
  useEffect(() => {
    const element = parent.current;
    if (!element) return;
    element.scrollTop = scrollPositions.get(cacheKey) ?? 0;
    return () => { if (scrollPositions.size > 100) scrollPositions.clear(); scrollPositions.set(cacheKey, element.scrollTop); };
  }, [cacheKey]);
  return <div ref={parent} className="h-[min(65dvh,720px)] min-h-80 overflow-auto" tabIndex={0} aria-label="Relationship connections">
    <ul className="relative w-full" style={{ height: virtual.getTotalSize() }}>
      {virtual.getVirtualItems().map(row => {
        const edge = items[row.index];
        return <li key={edge.id} data-index={row.index} ref={virtual.measureElement} className={cn('absolute top-0 left-0 w-full border-b px-4 py-3 hover:bg-surface-muted/50', edge.id === selected && 'bg-accent-soft')} style={{ transform: `translateY(${row.start}px)` }}>
          <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3">
            <EndpointButton item={edge.source_item} workspaceId={workspaceId} focus={focus} onExplore={onExplore} />
            <button aria-label={`Inspect ${edge.type_name} relationship between ${edge.source_item.name} and ${edge.target_item.name}`} aria-pressed={selected === edge.id} onClick={() => onInspect(edge)} className="flex max-w-36 flex-col items-center gap-1 rounded-md px-2 py-1 text-center text-xs text-muted hover:bg-accent-soft hover:text-accent">
              <span className="max-w-full truncate" title={connectionLabel(edge, focus)}>{connectionLabel(edge, focus)}</span>
              {edge.directed ? <ArrowRight className="size-4" aria-label="Source to target" /> : <span aria-label="Symmetric connection">—</span>}
            </button>
            <EndpointButton item={edge.target_item} workspaceId={workspaceId} focus={focus} onExplore={onExplore} />
          </div>
        </li>;
      })}
    </ul>
  </div>;
}

function EndpointButton({ item, workspaceId, focus, onExplore }: { item: Endpoint; workspaceId: string; focus?: string; onExplore: (item: Endpoint) => void }) {
  return <button onClick={() => onExplore(item)} className="group min-w-0 rounded px-1 py-2 text-left" title={`${item.name} · ${locationLabel(item, workspaceId)}`} aria-label={`Explore ${item.name}`} aria-current={focus === item.id ? 'true' : undefined}>
    <span className={cn('block truncate text-[13px] font-medium group-hover:text-accent', focus === item.id && 'text-accent')}>{item.name}</span>
    <span className="mt-1 block truncate text-xs text-muted">{locationLabel(item, workspaceId)}</span>
    {item.workspace_id !== workspaceId && <span className="mt-1 inline-flex items-center gap-1 text-[11px] text-muted"><ArrowUpRight className="size-3" />Other workspace</span>}
  </button>;
}

export function OpenItem({ item }: { item: Endpoint }) {
  return <Button asChild size="sm"><Link to="/w/$workspaceId/l/$listId" params={{ workspaceId: item.workspace_id, listId: item.collection_id }} search={{ item: item.id }}><ArrowUpRight />Open item</Link></Button>;
}

function ItemInspector({ item, workspaceId, workspaceName, onExplore }: { item: Endpoint; workspaceId: string; workspaceName: string; onExplore: (item: Endpoint) => void }) {
  return <div className="space-y-4 p-4"><CircleDot className="size-6 text-accent" /><h3 className="break-words font-medium">{item.name}</h3><p className="text-xs text-muted">{locationLabel(item, workspaceId)}</p>{item.workspace_id !== workspaceId && <p className="text-xs text-muted">Showing connections to {workspaceName}.</p>}<div className="flex flex-wrap gap-2"><Button size="sm" onClick={() => onExplore(item)}>Explore item</Button><OpenItem item={item} /></div></div>;
}

function RelationshipInspector({ edge, pending, error, retry, workspaceId, onExplore }: { edge?: Connection; pending: boolean; error: unknown; retry: () => void; workspaceId: string; onExplore: (item: Endpoint) => void }) {
  const attributes = useQuery({
    queryKey: ['relationship-explorer-attributes', workspaceId, edge?.id],
    enabled: !!edge,
    queryFn: async ({ signal }) => (await api<Relationship & { metadata?: Record<string, unknown> }>(`/v1/items/${edge!.source_id}/relationships/${edge!.id}`, { signal })).data,
  });
  const policy = useQuery({
    queryKey: ['relationship-explorer-policy', edge?.type_id],
    enabled: !!edge?.type_id,
    queryFn: async ({ signal }) => (await api<{ attributes?: { fields: { id: string; label: string; type: string }[] } }>(`/v1/relationship-types/${edge!.type_id}`, { signal })).data,
  });
  if (pending || error) return <QueryState query={{ isPending: pending, error, refetch: retry }} />;
  if (!edge) return <EmptyState title="Relationship unavailable"><p>It may have been removed or become unreadable.</p></EmptyState>;
  const metadata = attributes.data?.metadata ?? {};
  return <div className="space-y-5 p-4">
    <div><p className="text-xs text-muted">Relationship</p><h3 className="mt-1 break-words font-medium">{edge.type_name}</h3><p className="mt-1 text-xs text-muted">{edge.directed ? 'Directed' : 'Symmetric'}{edge.inverse_label && ` · Inverse: ${edge.inverse_label}`}</p></div>
    {[edge.source_item, edge.target_item].map((item, index) => <div key={item.id} className="space-y-2"><p className="text-xs text-muted">{edge.directed ? index === 0 ? 'Source' : 'Target' : 'Connected item'}</p><p className="break-words text-sm font-medium">{item.name}</p><p className="text-xs text-muted">{locationLabel(item, workspaceId)}</p><div className="flex flex-wrap gap-2"><Button size="sm" onClick={() => onExplore(item)}>Explore item</Button><OpenItem item={item} /></div></div>)}
    <div className="border-t pt-4"><h4 className="mb-2 text-sm font-medium">Attributes</h4><QueryState query={attributes} />{policy.error && <QueryState query={policy} />}{attributes.isSuccess && (Object.keys(metadata).length ? <dl className="space-y-3">{Object.entries(metadata).map(([key, value]) => <div key={key}><dt className="text-xs text-muted">{policy.data?.attributes?.fields.find(field => field.id === key)?.label ?? key}</dt><dd className="mt-1 break-words text-sm">{typeof value === 'boolean' ? value ? 'Yes' : 'No' : String(value)}</dd></div>)}</dl> : <p className="text-xs text-muted">No attributes on this relationship.</p>)}</div>
    <p className="text-xs text-muted">Created {new Date(edge.created_at).toLocaleString()}</p>
  </div>;
}
