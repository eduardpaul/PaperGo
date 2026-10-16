import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { Briefcase, Library, Plus, Settings, Tag } from 'lucide-react';
import { useState } from 'react';
import { collectionChildren, resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { QueryFeedback } from '@/features/admin/common';
import { useAccess } from '@/features/lists/access';
import { CollectionIcon } from '@/features/lists/collection-icon';
import { PlaceContextMenu } from '@/features/workspaces/place-context-menu';
import { NewCollectionDialog } from '@/features/workspaces/new-collection-dialog';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/w/$workspaceId/')({
  component: WorkspacePage,
});

function WorkspacePage() {
  const { workspaceId } = Route.useParams();
  const format = useFormat();
  const workspace = useQuery(resourceQuery(workspaceId));
  const collections = useQuery({
    queryKey: ['resources', workspaceId, 'collections'],
    queryFn: () => collectionChildren(workspaceId),
  });
  const { canManage } = useAccess(workspaceId);
  const [creating, setCreating] = useState(false);

  if (workspace.isError) {
    return (
      <Page>
        <QueryFeedback query={workspace} />
      </Page>
    );
  }
  return (
    <Page>
      <PageHeader
        icon={Briefcase}
        title={workspace.data?.name ?? ''}
        description={workspace.data?.description}
        actions={
          <>
            <Button asChild variant="ghost">
              <Link to="/w/$workspaceId/tags" params={{ workspaceId }}>
                <Tag /> Tags
              </Link>
            </Button>
            {canManage && (
              <Button asChild>
                <Link to="/w/$workspaceId/settings" params={{ workspaceId }}>
                  <Settings /> Settings
                </Link>
              </Button>
            )}
            {canManage && (
              <Button variant="primary" onClick={() => setCreating(true)}>
                <Plus /> New list or library
              </Button>
            )}
          </>
        }
      />
      <h2 className="mb-3 text-xs font-medium tracking-wide text-muted uppercase">Lists and libraries</h2>
      {collections.isPending ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-24" />
          <Skeleton className="h-24" />
        </div>
      ) : collections.isError ? (
        <QueryFeedback query={collections} />
      ) : collections.data.length ? (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {collections.data.map((collection) => (
            <li key={collection.id}>
              <PlaceContextMenu resource={collection}><Link
                to="/w/$workspaceId/l/$listId"
                params={{ workspaceId, listId: collection.id }}
                className="group block h-full"
              >
                <Card className="flex h-full flex-col gap-2 p-4 transition-colors group-hover:border-accent/50">
                  <span className="flex items-center gap-2">
                    <CollectionIcon collection={collection} className="size-4 shrink-0 text-accent" />
                    <span className="truncate font-semibold">{collection.name}</span>
                  </span>
                  <span className="line-clamp-2 flex-1 text-[13px] text-muted">
                    {collection.description || (collection.kind === 'library' ? 'Library' : 'List')}
                  </span>
                  {collection.updated_at && (
                    <span className="text-xs text-muted">Updated {format.relative(collection.updated_at)}</span>
                  )}
                </Card>
              </Link></PlaceContextMenu>
            </li>
          ))}
        </ul>
      ) : (
        <Card>
          <EmptyState icon={Library} title="No lists or libraries yet">
            <p className="mb-3">Lists hold rows of information such as contacts or budgets; libraries hold files.</p>
            {canManage && (
              <Button variant="primary" onClick={() => setCreating(true)}>
                <Plus /> New list or library
              </Button>
            )}
          </EmptyState>
        </Card>
      )}
      <NewCollectionDialog key={String(creating)} workspaceId={workspaceId} open={creating} onOpenChange={setCreating} />
    </Page>
  );
}
