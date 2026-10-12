import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { createFileRoute, Link } from '@tanstack/react-router';
import { ArrowLeft, Folder, Trash2 } from 'lucide-react';
import { api } from '@/api/http';
import { keys, recycleBin, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { usePeopleNames } from '@/features/fields/people';
import { can } from '@/features/lists/access';
import { ConfirmDialog } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/recycle-bin')({ component: RecycleBinPage });

function RecycleBinPage() {
  const { workspaceId, listId } = Route.useParams();
  const format = useFormat();
  const names = usePeopleNames();
  const [purging, setPurging] = useState<Resource>();
  const queryClient = useQueryClient();
  const collection = useQuery(resourceQuery(listId));
  const bin = useQuery({ queryKey: ['recycle-bin', listId], queryFn: () => recycleBin(listId) });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['recycle-bin', listId] }).then(() => queryClient.invalidateQueries({ queryKey: keys.resources }));
  const restore = useMutation({
    mutationFn: (item: Resource) => api(`/v1/resources/${listId}/recycle-bin/${item.id}/restore`, { method: 'POST', query: { recursive: 'true' } }),
    onSuccess: async (_result, item) => {
      toast.success(`${item.name} restored`);
      await refresh();
    },
  });
  const purge = useMutation({
    meta: { silent: true },
    mutationFn: (item: Resource) => api(`/v1/resources/${listId}/recycle-bin/${item.id}`, { method: 'DELETE', query: { recursive: 'true' } }),
    onSuccess: async () => {
      setPurging(undefined);
      await refresh();
    },
  });
  const rows = bin.data?.value ?? [];
  return (
    <Page>
      <Link to="/w/$workspaceId/l/$listId" params={{ workspaceId, listId }} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground">
        <ArrowLeft className="size-3.5" /> {collection.data?.name ?? 'Collection'}
      </Link>
      <PageHeader icon={Trash2} title="Recycle bin" description="Deleted items and folders of this list. Restore them, or delete them for good." />
      {bin.isPending ? (
        <Skeleton className="h-16" />
      ) : rows.length === 0 ? (
        <Card>
          <EmptyState icon={Trash2} title="The recycle bin is empty" />
        </Card>
      ) : (
        <ul className="overflow-hidden rounded-lg border bg-surface">
          {rows.map((item) => (
            <li key={item.id} className="flex flex-wrap items-center gap-3 border-b px-4 py-2.5 last:border-0">
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 truncate text-sm font-medium">
                  {item.kind === 'folder' && <Folder className="size-4 text-muted" />}
                  {item.name}
                </p>
                <p className="text-xs text-muted">
                  Deleted
                  {item.deleted_at ? ` ${format.relative(item.deleted_at)}` : ''}
                  {item.deleted_by ? ` by ${names.get(item.deleted_by) ?? item.deleted_by}` : ''}
                </p>
              </div>
              <Button size="sm" disabled={restore.isPending} onClick={() => restore.mutate(item)}>
                Restore
              </Button>
              {can(collection.data, 'manage') && (
                <Button size="sm" variant="ghost" onClick={() => setPurging(item)}>
                  Delete permanently
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={!!purging}
        onOpenChange={(open) => !open && setPurging(undefined)}
        title={`Delete “${purging?.name ?? ''}” permanently?`}
        description={purging?.kind === 'folder' ? 'The folder and everything in it can no longer be restored.' : 'It can no longer be restored.'}
        confirm="Delete permanently"
        busy={purge.isPending}
        error={purge.error}
        onConfirm={() => purging && purge.mutate(purging)}
      />
    </Page>
  );
}
