import { useId, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, ArrowRight, CircleDot, Network } from 'lucide-react';
import { api } from '@/api/http';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/ui/feedback';
import { useTranslation } from '@/i18n';
import { cn } from '@/lib/utils';
import { QueryState } from './explorer';
import { explorerQuery, locationLabel, peerOf, type Connection, type Endpoint, type ExplorerSearch, type Neighborhood } from './model';

/** A bounded, deterministic one-hop map. Traversal replaces the current map. */
export default function RelationshipGraph({ workspaceId, search, focus, onExplore, onInspect, onList }: {
  workspaceId: string; search: ExplorerSearch; focus: Endpoint;
  onExplore: (item: Endpoint) => void; onInspect: (edge: Connection) => void; onList: () => void;
}) {
  const { t } = useTranslation('relationships');
  const marker = useId().replaceAll(':', '');
  const [selectedId, setSelectedId] = useState<string>();
  const graph = useQuery({
    queryKey: ['relationship-explorer-neighborhood', workspaceId, explorerQuery(search)],
    gcTime: 60_000,
    queryFn: async ({ signal }) => (await api<Neighborhood>(`/v1/workspaces/${workspaceId}/relationships/neighborhood`, { signal, query: explorerQuery(search) })).data,
  });
  const groups = new Map<string, { item: Endpoint; edges: Connection[] }>();
  for (const edge of graph.data?.edges ?? []) {
    const peer = peerOf(edge, focus.id);
    const existing = groups.get(peer.id);
    if (existing) existing.edges.push(edge);
    else groups.set(peer.id, { item: peer, edges: [edge] });
  }
  const incoming = [...groups.values()].filter(group => group.edges.every(edge => edge.directed && edge.target_id === focus.id));
  const outgoing = [...groups.values()].filter(group => group.edges.some(edge => edge.directed && edge.source_id === focus.id));
  const symmetric = [...groups.values()].filter(group => !incoming.includes(group) && !outgoing.includes(group));
  const height = Math.max(400, Math.max(incoming.length, outgoing.length) * 76 + 120);
  const totalHeight = height + (symmetric.length ? Math.ceil(symmetric.length / 3) * 86 + 60 : 0);
  const positions = new Map<string, { x: number; y: number }>();
  positions.set(focus.id, { x: 500, y: height / 2 });
  incoming.forEach((group, index) => positions.set(group.item.id, { x: 150, y: (index + 1) * height / (incoming.length + 1) }));
  outgoing.forEach((group, index) => positions.set(group.item.id, { x: 850, y: (index + 1) * height / (outgoing.length + 1) }));
  symmetric.forEach((group, index) => positions.set(group.item.id, { x: 150 + (index % 3) * 350, y: height + 60 + Math.floor(index / 3) * 86 }));
  const selected = selectedId === focus.id ? focus : groups.get(selectedId ?? '')?.item;
  const selectedGroup = groups.get(selectedId ?? '');
  return <div className="flex min-h-96 flex-1 flex-col">
    <QueryState query={graph} />
    {graph.isSuccess && !groups.size ? <EmptyState icon={Network} title={t('empty.filteredTitle')}><p>{t('graph.emptyHint')}</p><Button className="mt-3" onClick={onList}>{t('graph.viewList')}</Button></EmptyState> : graph.data ? <>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-2 text-xs text-muted"><span>{t('graph.summary', { count: groups.size })}</span><span className="flex items-center gap-3"><span className="inline-flex items-center gap-1"><ArrowLeft className="size-3" />{t('incoming')}</span><span className="inline-flex items-center gap-1">{t('outgoing')}<ArrowRight className="size-3" /></span><span>{`— ${t('symmetric')}`}</span></span></div>
      <div className="max-h-[65dvh] overflow-auto bg-surface-muted/30 p-3" aria-label={t('graph.label')}>
        <div className="relative min-w-[640px]" style={{ height: totalHeight }}>
          <svg viewBox={`0 0 1000 ${totalHeight}`} preserveAspectRatio="none" className="pointer-events-none absolute inset-0 h-full w-full text-border" aria-hidden="true">
            <defs><marker id={marker} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" /></marker></defs>
            {[...groups.values()].map(group => {
              const p = positions.get(group.item.id)!;
              const center = positions.get(focus.id)!;
              const symmetricOnly = group.edges.every(edge => !edge.directed);
              const incomingOnly = group.edges.every(edge => edge.directed && edge.target_id === focus.id);
              const bothDirections = group.edges.some(edge => edge.directed && edge.target_id === focus.id) && group.edges.some(edge => edge.directed && edge.source_id === focus.id);
              const startX = center.x + (p.x === center.x ? 0 : p.x > center.x ? 112 : -112);
              const endX = p.x + (p.x === center.x ? 0 : p.x > center.x ? -112 : 112);
              return <path key={group.item.id} d={`M${startX},${center.y} C${(startX + endX) / 2},${center.y} ${(startX + endX) / 2},${p.y} ${endX},${p.y}`} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" markerEnd={!symmetricOnly && !incomingOnly ? `url(#${marker})` : undefined} markerStart={incomingOnly || bothDirections ? `url(#${marker})` : undefined} />;
            })}
          </svg>
          {[{ item: focus, edges: [] }, ...groups.values()].map(group => {
            const p = positions.get(group.item.id)!;
            const active = group.item.id === focus.id;
            return <div key={group.item.id} className="absolute w-[23%] -translate-x-1/2 -translate-y-1/2" style={{ left: `${p.x / 10}%`, top: p.y }}>
              <button onClick={() => setSelectedId(group.item.id)} aria-label={t('graph.inspect', { name: group.item.name })} aria-pressed={selectedId === group.item.id} className={cn('w-full rounded-lg border bg-surface px-3 py-2.5 text-left shadow-xs hover:border-accent', active && 'border-accent bg-accent-soft', selectedId === group.item.id && 'ring-2 ring-accent')} title={`${group.item.name} · ${locationLabel(group.item, workspaceId)}`}>
                <span className="flex items-center gap-1.5 text-[13px] font-medium">{active && <CircleDot className="size-3.5 shrink-0 text-accent" />}<span className="truncate">{group.item.name}</span></span>
                <span className="mt-1 block truncate text-xs text-muted">{locationLabel(group.item, workspaceId)}</span>
              </button>
              {!active && <button onClick={() => { setSelectedId(undefined); onExplore(group.item); }} className="mx-auto mt-1 block rounded bg-surface px-2 py-1 text-xs text-accent hover:underline" aria-label={t('graph.exploreFrom', { name: group.item.name })}>{t('graph.exploreHere')}</button>}
            </div>;
          })}
        </div>
      </div>
      {selected && <div className="flex flex-wrap items-center gap-2 border-t p-3"><span className="mr-auto text-sm font-medium">{selected.name}</span>{selected.id !== focus.id && <Button size="sm" onClick={() => { setSelectedId(undefined); onExplore(selected); }}>{t('graph.exploreHere')}</Button>}{selectedGroup?.edges.map(edge => <Button key={edge.id} size="sm" onClick={() => onInspect(edge)}>{edge.type_name}{edge.directed ? edge.target_id === focus.id ? ' ←' : ' →' : ' —'}</Button>)}</div>}
    </> : null}
    {graph.data?.truncated && <div role="status" className="flex flex-wrap items-center justify-between gap-2 border-t bg-surface-muted px-4 py-3 text-xs"><span>{t('graph.truncated')}</span><Button size="sm" onClick={onList}>{t('graph.viewList')}</Button></div>}
    <div className="border-t px-4 py-2 text-xs text-muted">{t('graph.footer')}</div>
  </div>;
}
