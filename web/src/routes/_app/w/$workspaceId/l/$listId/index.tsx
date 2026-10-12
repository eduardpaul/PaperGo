import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { CollectionPage, type CollectionSearch } from '@/features/lists/collection-page';
import { textParam } from '@/lib/search';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/')({
  validateSearch: (search: Record<string, unknown>): CollectionSearch => ({
    folder: textParam(search.folder),
    view: textParam(search.view),
    item: textParam(search.item),
    tab: textParam(search.tab),
    q: textParam(search.q),
    sort: textParam(search.sort),
    cols: textParam(search.cols),
  }),
  component: CollectionRoute,
});

function CollectionRoute() {
  const { workspaceId, listId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  return (
    <CollectionPage
      workspaceId={workspaceId}
      listId={listId}
      search={search}
      onSearch={(patch, replace) => void navigate({ search: (current) => ({ ...current, ...patch }), replace })}
      onMoved={(moved) => {
        const destination = moved.container_id ?? listId;
        if (moved.workspace_id !== workspaceId || destination !== listId) {
          void navigate({
            to: '/w/$workspaceId/l/$listId',
            params: { workspaceId: moved.workspace_id, listId: destination },
            search: { item: moved.id },
          });
          return;
        }
        void navigate({ search: (current) => ({ ...current, item: moved.id }) });
      }}
    />
  );
}
