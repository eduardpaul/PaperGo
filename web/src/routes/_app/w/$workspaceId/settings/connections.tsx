import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { connectionKeys, connectionsQuery, working, type Connection, type TestResult } from '@/features/connections/api';
import { ConnectionEditor } from '@/features/connections/editor';
import { ConnectionMonitor } from '@/features/connections/monitor';
import { useAccess } from '@/features/lists/access';
import { ConfirmDialog, CopyField, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/connections')({ component: ConnectionsPage });

/**
 * Workspace settings → Devices & services: the workspace's MQTT brokers and
 * HTTP services, which its apps and workflows reach by key through named
 * channels. Workspace owners with connection.manage set them up.
 */
function ConnectionsPage() {
  const { workspaceId } = Route.useParams();
  const { t } = useTranslation(['connections', 'common']);
  const client = useQueryClient();
  const { canManage, loaded } = useAccess(workspaceId);
  const hasScope = useHasScope('connection.manage');
  const canWrite = canManage && hasScope;
  const list = useQuery({ ...connectionsQuery(workspaceId), enabled: canWrite });
  const [editing, setEditing] = useState<Connection | 'new'>();
  const [monitoring, setMonitoring] = useState<Connection>();
  const [deleting, setDeleting] = useState<Connection>();
  const [secret, setSecret] = useState<{ name: string; value: string }>();
  const [tested, setTested] = useState<Record<string, TestResult>>({});
  const test = useMutation({
    mutationFn: async (entry: Connection) => (await api<TestResult>(`/v1/connections/${entry.id}/test`, { method: 'POST' })).data,
    onSuccess: (result, entry) => setTested((current) => ({ ...current, [entry.id]: result })),
  });
  const renew = useMutation({
    mutationFn: async (entry: Connection) => (await api<Connection>(`/v1/connections/${entry.id}/signing-secret`, { method: 'POST' })).data,
    onSuccess: async (saved) => {
      if (saved.signing_secret) setSecret({ name: saved.name, value: saved.signing_secret });
      await client.invalidateQueries({ queryKey: connectionKeys.list(workspaceId) });
    },
  });
  const remove = useMutation({
    mutationFn: (entry: Connection) => api(`/v1/connections/${entry.id}`, { method: 'DELETE', ifMatch: entry.version }),
    onSuccess: async () => {
      setDeleting(undefined);
      await client.invalidateQueries({ queryKey: connectionKeys.list(workspaceId) });
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
      {canWrite && <QueryFeedback query={list} />}
      {secret && (
        <Alert tone="warning" className="grid gap-2">
          <span>{t('page.secretOnce', { name: secret.name })}</span>
          <CopyField label={t('page.signingSecret')} value={secret.value} />
        </Alert>
      )}
      {list.data?.length === 0 && <p className="text-sm text-muted">{t('page.empty')}</p>}
      <div className="grid gap-4">
        {(list.data ?? []).map((entry) => {
          const result = tested[entry.id];
          return (
            <article key={entry.id} className="grid gap-2 rounded-lg border p-4">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="flex-1 text-sm font-medium">{entry.name}</h3>
                <Badge>{t(`kind.${entry.kind}`)}</Badge>
                <Badge tone={entry.status.state === 'disabled' ? 'neutral' : working(entry.status) ? 'success' : 'danger'}>{t(`state.${entry.status.state}`)}</Badge>
              </div>
              <p className="font-mono text-xs text-muted">
                {entry.key} · {entry.target}
              </p>
              {!entry.target && <Alert tone="warning">{t('page.needsTarget')}</Alert>}
              {entry.status.last_error && <p className="text-xs text-danger">{entry.status.last_error}</p>}
              <p className="text-xs text-muted">{t('page.channels', { count: entry.channels.length, names: entry.channels.map((c) => c.name || c.key).join(', ') })}</p>
              {result && <Alert tone={result.ok ? 'success' : 'danger'}>{t(result.ok ? 'page.testOk' : 'page.testFailed', { detail: result.detail, ms: result.took_ms })}</Alert>}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" onClick={() => setEditing(entry)}>
                  {t('actions.edit')}
                </Button>
                <Button size="sm" disabled={test.isPending || !entry.target} onClick={() => test.mutate(entry)}>
                  {t('actions.test')}
                </Button>
                <Button size="sm" disabled={!entry.enabled} onClick={() => setMonitoring(entry)}>
                  {t('actions.monitor')}
                </Button>
                {entry.kind === 'http' && (
                  <Button size="sm" disabled={renew.isPending} onClick={() => renew.mutate(entry)}>
                    {t('actions.renewSecret')}
                  </Button>
                )}
                <Button size="sm" variant="danger" onClick={() => setDeleting(entry)}>
                  {t('common:actions.delete')}
                </Button>
              </div>
            </article>
          );
        })}
      </div>
      <QueryFeedback query={{ isPending: false, error: test.error ?? renew.error }} />
      {editing && (
        <ConnectionEditor
          workspaceId={workspaceId}
          connection={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(undefined)}
          onSaved={(saved) => {
            setEditing(undefined);
            if (saved.signing_secret) setSecret({ name: saved.name, value: saved.signing_secret });
          }}
        />
      )}
      {monitoring && <ConnectionMonitor connection={monitoring} onClose={() => setMonitoring(undefined)} />}
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
