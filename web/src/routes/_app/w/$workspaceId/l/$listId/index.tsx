import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { CollectionPage, type CollectionSearch } from '@/features/lists/collection-page';
import { flagParam, pageParam, textParam } from '@/lib/search';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/')({
  validateSearch: (search: Record<string, unknown>): CollectionSearch => ({
    create: search.create === 'folder' || search.create === 'upload' ? search.create : undefined,
    folder: textParam(search.folder),
    view: textParam(search.view),
    item: textParam(search.item),
    open: search.open === 'file' ? 'file' : undefined,
    page: pageParam(search.page),
    blob: textParam(search.blob),
    edit: flagParam(search.edit),
    tab: textParam(search.tab),
    comment: textParam(search.comment),
    q: textParam(search.q),
    sort: textParam(search.sort),
    cols: textParam(search.cols),
    nav: textParam(search.nav),
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
      onSearch={(patch, replace, keepScroll) => void navigate({ search: (current) => ({ ...current, ...patch }), replace, resetScroll: keepScroll ? false : undefined })}
      onMoved={(moved) => {
        const destination = moved.container_id ?? listId;
        if (moved.workspace_id !== workspaceId || destination !== listId) {
          void navigate({
            to: '/w/$workspaceId/l/$listId',
            params: { workspaceId: moved.workspace_id, listId: destination },
            search: { item: moved.id, folder: moved.parent_id !== destination ? moved.parent_id : undefined },
          });
          return;
        }
        void navigate({ search: (current) => ({ ...current, item: moved.id, folder: moved.parent_id !== destination ? moved.parent_id : undefined }) });
      }}
    />
  );
}
