import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { resourceQuery } from '@/api/queries';
import { CollectionColumns, CollectionContentTypes } from '@/features/admin/content-types';
import { useAccess } from '@/features/lists/access';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/columns')({ component: ColumnsPage });

function ColumnsPage() {
  const { listId } = Route.useParams();
  const collection = useQuery(resourceQuery(listId));
  const { canManage } = useAccess(listId);
  if (!collection.data) return null;
  return (
    <>
      <CollectionColumns collection={collection.data} canManage={canManage} />
      <CollectionContentTypes collection={collection.data} canManage={canManage} />
    </>
  );
}
