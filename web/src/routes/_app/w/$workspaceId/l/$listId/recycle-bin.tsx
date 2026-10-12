import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { ArrowLeft } from 'lucide-react';
import { api } from '@/api/http';
import { keys, recycleBin, resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/recycle-bin')({ component: RecycleBinPage });

function RecycleBinPage() {
  const { workspaceId, listId } = Route.useParams();
  const format = useFormat();
  const queryClient = useQueryClient();
  const collection = useQuery(resourceQuery(listId));
  const bin = useQuery({ queryKey: ['recycle-bin', listId], queryFn: () => recycleBin(listId) });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['recycle-bin', listId] }).then(() => queryClient.invalidateQueries({ queryKey: keys.resources }));
  const restore = useMutation({
    mutationFn: (itemId: string) => api(`/v1/resources/${listId}/recycle-bin/${itemId}/restore`, { method: 'POST', query: { recursive: 'true' } }),
    onSuccess: refresh,
  });
  const purge = useMutation({
    mutationFn: (itemId: string) => api(`/v1/resources/${listId}/recycle-bin/${itemId}`, { method: 'DELETE', query: { recursive: 'true' } }),
    onSuccess: refresh,
  });
  const rows = bin.data?.value ?? [];
  return (
    <Page>
      <Link to="/w/$workspaceId/l/$listId" params={{ workspaceId, listId }} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground">
        <ArrowLeft className="size-3.5" /> {collection.data?.name ?? 'Collection'}
      </Link>
      <PageHeader title="Recycle bin" description="Restore or permanently delete items and folders." />
      {bin.isPending ? (
        <Skeleton className="h-16" />
      ) : rows.length === 0 ? (
        <EmptyState title="The recycle bin is empty" />
      ) : (
        <ul className="overflow-hidden rounded-lg border bg-surface">
          {rows.map((item) => (
            <li key={item.id} className="flex flex-wrap items-center gap-3 border-b px-4 py-2.5 last:border-0">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{item.name}</p>
                <p className="text-xs text-muted">
                  {item.kind}
                  {item.deleted_at ? ` · ${format.dateTime(item.deleted_at)}` : ''}
                  {item.deleted_by ? ` · ${item.deleted_by}` : ''}
                </p>
              </div>
              <Button size="sm" disabled={restore.isPending} onClick={() => restore.mutate(item.id)}>
                Restore
              </Button>
              <Button size="sm" variant="danger" disabled={purge.isPending} onClick={() => purge.mutate(item.id)}>
                Delete permanently
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Page>
  );
}
