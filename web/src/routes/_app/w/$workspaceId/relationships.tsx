import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { RelationshipsExplorer } from '@/features/relationships/explorer';
import type { ExplorerSearch } from '@/features/relationships/model';
import { textParam } from '@/lib/search';

export const Route = createFileRoute('/_app/w/$workspaceId/relationships')({
  validateSearch: (search: Record<string, unknown>): ExplorerSearch => ({
    type: textParam(search.type),
    item: textParam(search.item),
    collection: textParam(search.collection),
    scope: search.scope === 'internal' || search.scope === 'external' ? search.scope : undefined,
    direction: search.direction === 'incoming' || search.direction === 'outgoing' || search.direction === 'symmetric' ? search.direction : undefined,
    view: search.view === 'graph' ? 'graph' : undefined,
    edge: textParam(search.edge),
    trail: Array.isArray(search.trail) ? search.trail.filter((value): value is string => typeof value === 'string').slice(-12) : undefined,
  }),
  component: RelationshipsRoute,
});

function RelationshipsRoute() {
  const { workspaceId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  return <RelationshipsExplorer workspaceId={workspaceId} search={search} onSearch={(patch, replace) => {
    void navigate({ search: current => ({ ...current, ...patch }), replace });
  }} />;
}
