import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { contentTypesQuery, fieldsQuery, resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { ViewsEditor } from '@/features/lists/views-editor';
export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/views')({ component: ViewsPage });
function ViewsPage() {
  const { workspaceId, listId } = Route.useParams();
  const collection = useQuery(resourceQuery(listId));
  const fields = useQuery(fieldsQuery(listId));
  const types = useQuery(contentTypesQuery(listId));
  return (
    <Page>
      <Link className="text-accent" to="/w/$workspaceId/l/$listId" params={{ workspaceId, listId }}>
        Back to {collection.data?.name ?? 'collection'}
      </Link>
      <PageHeader title="Views" />
      <ViewsEditor collectionId={listId} fields={fields.data ?? []} contentTypes={types.data ?? []} />
    </Page>
  );
}
