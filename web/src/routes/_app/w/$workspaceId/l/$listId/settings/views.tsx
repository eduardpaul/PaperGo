import { useQuery } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { contentTypesQuery, fieldsQuery } from '@/api/queries';
import { ViewsEditor } from '@/features/lists/views-editor';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings/views')({ component: ViewsPage });

function ViewsPage() {
  const { listId } = Route.useParams();
  const fields = useQuery(fieldsQuery(listId));
  const types = useQuery(contentTypesQuery(listId));
  return <ViewsEditor collectionId={listId} fields={fields.data ?? []} contentTypes={types.data ?? []} />;
}
