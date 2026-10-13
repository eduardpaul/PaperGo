import { createFileRoute } from '@tanstack/react-router';
import { CollectionOperations } from '@/features/lists/operations';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/activity')({ component: ActivityPage });

function ActivityPage() {
  const { listId } = Route.useParams();
  return <CollectionOperations collectionId={listId} />;
}
