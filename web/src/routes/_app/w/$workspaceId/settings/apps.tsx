import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { appKeys, appQuery, appsQuery, type App } from '@/features/apps/api';
import { AppEditor } from '@/features/apps/app-editor';
import { useAccess } from '@/features/lists/access';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/apps')({ component: AppsPage });

function AppsPage() {
  const { workspaceId } = Route.useParams();
  const { t } = useTranslation(['apps', 'common']);
  const client = useQueryClient();
  const list = useQuery(appsQuery(workspaceId));
  const { canManage, loaded } = useAccess(workspaceId);
  const hasScope = useHasScope('app.manage');
  const canWrite = canManage && hasScope;
  const [editing, setEditing] = useState<App | 'new'>();
  const [deleting, setDeleting] = useState<App>();
  const open = useMutation({ mutationFn: (entry: App) => client.fetchQuery(appQuery(entry.id)), onSuccess: setEditing });
  const remove = useMutation({
    mutationFn: (entry: App) => api(`/v1/apps/${entry.id}`, { method: 'DELETE', ifMatch: entry.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: appKeys.lists });
    },
  });
  return (
    <SettingsSection
      title={t('page.title')}
      description={t('page.description')}
      actions={
        canWrite && (
          <Button variant="primary" onClick={() => setEditing('new')}>
            {t('actions.new')}
          </Button>
        )
      }
    >
      {loaded && !canWrite && <Alert tone="warning">{t('page.needScope')}</Alert>}
      <QueryFeedback query={list} />
      <QueryFeedback query={open} />
      {list.data?.length === 0 && <p className="text-sm text-muted">{t('page.empty')}</p>}
      <div className="grid gap-4">
        {(list.data ?? []).map((entry) => (
          <article key={entry.id} className="grid gap-2 rounded-lg border p-4">
            <div className="flex items-center gap-2">
              <h3 className="flex-1 text-sm font-medium">{entry.name}</h3>
              <Badge>{t(`state.${entry.state}`)}</Badge>
            </div>
            <p className="text-xs text-muted">{t('page.version', { version: entry.current_version, key: entry.key })}</p>
            {entry.description && <p className="text-sm">{entry.description}</p>}
            <div className="flex flex-wrap gap-2">
              <Button size="sm" asChild>
                <Link to="/w/$workspaceId/a/$appId" params={{ workspaceId, appId: entry.id }}>
                  {t('actions.open')}
                </Link>
              </Button>
              {entry.can_write && (
                <Button size="sm" disabled={open.isPending} onClick={() => open.mutate(entry)}>
                  {t('actions.edit')}
                </Button>
              )}
              {entry.can_write && (
                <Button size="sm" variant="danger" onClick={() => setDeleting(entry)}>
                  {t('common:actions.delete')}
                </Button>
              )}
            </div>
          </article>
        ))}
      </div>
      {editing && <AppEditor workspaceId={workspaceId} app={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(value) => !value && setDeleting(undefined)}
        title={t('page.deleteTitle', { name: deleting?.name ?? '' })}
        description={t('page.deleteDescription')}
        confirm={t('page.deleteConfirm')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </SettingsSection>
  );
}
