import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { createFileRoute, Link } from '@tanstack/react-router';
import { ArrowLeft, Folder, Trash2 } from 'lucide-react';
import { api } from '@/api/http';
import { useApiPages } from '@/api/pages';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Page, PageHeader } from '@/components/page';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { EmptyState, Skeleton } from '@/components/ui/feedback';
import { usePeopleNames } from '@/features/fields/people';
import { can } from '@/features/lists/access';
import { ConfirmDialog } from '@/features/settings/section';
import { useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';

/** A recycle bin entry; a folder counts what was deleted with it. */
type DeletedEntry = Resource & { deleted_with?: number };

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/recycle-bin')({ component: RecycleBinPage });

function RecycleBinPage() {
  const { t } = useTranslation(['workspaces', 'common']);
  const { workspaceId, listId } = Route.useParams();
  const format = useFormat();
  const names = usePeopleNames();
  const [purging, setPurging] = useState<DeletedEntry>();
  const queryClient = useQueryClient();
  const collection = useQuery(resourceQuery(listId));
  // One entry per deletion: a folder stands for everything deleted with it.
  const bin = useApiPages<DeletedEntry>(['recycle-bin', listId], `/v1/resources/${listId}/recycle-bin`, {
    query: { top_level: 'true' },
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['recycle-bin', listId] }).then(() => queryClient.invalidateQueries({ queryKey: keys.resources }));
  const restore = useMutation({
    mutationFn: (item: DeletedEntry) => api(`/v1/resources/${listId}/recycle-bin/${item.id}/restore`, { method: 'POST', query: { recursive: 'true' } }),
    onSuccess: async (_result, item) => {
      toast.success(t('bin.restored', { name: item.name }));
      await refresh();
    },
  });
  const purge = useMutation({
    meta: { silent: true },
    mutationFn: (item: DeletedEntry) => api(`/v1/resources/${listId}/recycle-bin/${item.id}`, { method: 'DELETE', query: { recursive: 'true' } }),
    onSuccess: async () => {
      setPurging(undefined);
      await refresh();
    },
  });
  const rows = bin.items;
  const manageable = can(collection.data, 'manage');
  return (
    <Page>
      <Link to="/w/$workspaceId/l/$listId" params={{ workspaceId, listId }} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground">
        <ArrowLeft className="size-3.5" /> {collection.data?.name ?? t('bin.collection')}
      </Link>
      <PageHeader icon={Trash2} title={t('bin.title')} description={t('bin.description')} />
      {bin.isPending ? (
        <Skeleton className="h-16" />
      ) : rows.length === 0 ? (
        <Card>
          <EmptyState icon={Trash2} title={t('bin.empty')} />
        </Card>
      ) : (
        <>
          <ul className="overflow-hidden rounded-lg border bg-surface">
            {rows.map((item) => (
              <li key={item.id} className="flex flex-wrap items-center gap-3 border-b px-4 py-2.5 last:border-0">
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 truncate text-sm font-medium">
                    {item.kind === 'folder' && <Folder className="size-4 text-muted" />}
                    {item.name}
                  </p>
                  <p className="text-xs text-muted">
                    {[
                      t('bin.deleted'),
                      item.deleted_at && format.relative(item.deleted_at),
                      item.deleted_by && t('bin.by', { name: names.get(item.deleted_by) ?? item.deleted_by }),
                      item.deleted_with && t('bin.with', { count: item.deleted_with, formatted: format.number(item.deleted_with) }),
                    ].filter(Boolean).join(' ')}
                  </p>
                </div>
                <Button size="sm" disabled={restore.isPending} onClick={() => restore.mutate(item)}>
                  {t('common:actions.restore')}
                </Button>
                {manageable && (
                  <Button size="sm" variant="ghost" onClick={() => setPurging(item)}>
                    {t('bin.purge')}
                  </Button>
                )}
              </li>
            ))}
          </ul>
          {bin.hasNextPage && (
            <Button className="mt-3" disabled={bin.isFetchingNextPage} onClick={() => void bin.fetchNextPage()}>
              {bin.isFetchingNextPage ? t('common:status.loading') : t('bin.loadMore')}
            </Button>
          )}
        </>
      )}
      <ConfirmDialog
        open={!!purging}
        onOpenChange={(open) => !open && setPurging(undefined)}
        title={t('bin.purgeTitle', { name: purging?.name ?? '' })}
        description={purging?.kind === 'folder' ? t('bin.purgeFolder') : t('bin.purgeItem')}
        confirm={t('bin.purge')}
        busy={purge.isPending}
        error={purge.error}
        onConfirm={() => purging && purge.mutate(purging)}
      />
    </Page>
  );
}
