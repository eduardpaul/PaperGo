import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { resourceQuery } from '@/api/queries';
import { useAccess } from '@/features/lists/access';
import { ListIntegrations } from '@/features/lists/integrations';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/integrations')({ component: IntegrationsPage });
function IntegrationsPage() {
  const { listId } = Route.useParams();
  const collection = useQuery(resourceQuery(listId));
  const { canManage } = useAccess(listId);
  if (collection.data?.kind !== 'list') return <p className="text-sm text-muted">Integrations are available on lists.</p>;
  return <ListIntegrations listId={listId} canManage={canManage} />;
}
