import { useQuery } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { resourceQuery } from '@/api/queries';
import { useAccess } from '@/features/lists/access';
import { CollectionGeneral } from '@/features/lists/collection-settings';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/')({ component: GeneralPage });

function GeneralPage() {
  const { workspaceId, listId } = Route.useParams();
  const navigate = useNavigate();
  const collection = useQuery(resourceQuery(listId));
  const { canManage } = useAccess(listId);
  if (!collection.data) return null;
  return (
    <CollectionGeneral
      key={collection.data.version}
      collection={collection.data}
      canManage={canManage}
      onDeleted={() => void navigate({ to: '/w/$workspaceId', params: { workspaceId } })}
    />
  );
}
