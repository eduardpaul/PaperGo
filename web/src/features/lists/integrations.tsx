import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { downloadFile } from '@/api/files';
import { keys } from '@/api/queries';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

import { Input, Label } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { OperationProgress, type Operation } from '@/features/admin/operation-progress';
import { hostOf, useTrustHost } from '@/features/files/trust';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

/** The error of an integration whose host the organization stopped trusting, as the server reports it. */
const hostNotTrusted = 'Host no longer trusted';

const urlPlaceholder = 'https://…';

export interface ListIntegration {
  id: string;
  kind: 'ical_https';
  name: string;
  paused: boolean;
  version: number;
  last_attempt_at?: string;
  last_success_at?: string;
  error?: string;
  item_count?: number;
}

export function ListIntegrations({ listId, canManage }: { listId: string; canManage: boolean }) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const format = useFormat();
  const administrator = useHasScope('organization.manage');
  const path = `/v1/resources/${listId}/integrations`;
  const sources = useQuery({
    queryKey: ['resources', listId, 'integrations'],
    queryFn: async () => (await api<ListIntegration[]>(path)).data,
    refetchInterval: 10_000,
  });
  const [editing, setEditing] = useState<ListIntegration | 'new'>();
  const [removing, setRemoving] = useState<ListIntegration>();
  const [operationId, setOperationId] = useState('');
  const refresh = useMutation({
    mutationFn: async (source: ListIntegration) => (await api<Operation>(`${path}/${source.id}/refresh`, { method: 'POST' })).data,
    onSuccess: (op) => { setOperationId(op.id); toast.success(t('integrations.refreshQueued')); },
  });
  const pause = useMutation({
    mutationFn: (source: ListIntegration) => api(`${path}/${source.id}`, { method: 'PATCH', ifMatch: source.version, body: { paused: !source.paused } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['resources', listId, 'integrations'] }),
  });
  const remove = useMutation({
    mutationFn: (source: ListIntegration) => api(`${path}/${source.id}`, { method: 'DELETE', ifMatch: source.version }),
    onSuccess: async () => { setRemoving(undefined); await client.invalidateQueries({ queryKey: keys.resources }); toast.success(t('integrations.removed')); },
  });
  const download = useMutation({ mutationFn: () => downloadFile(`/v1/resources/${listId}/calendar/export`, 'calendar.ics') });
  return (
    <div className="grid gap-4">
      <SettingsSection title={t('integrations.title')} description={t('integrations.description')}
        actions={canManage && <Button variant="primary" onClick={() => setEditing('new')}>{t('integrations.add')}</Button>}>
        <QueryFeedback query={sources} />
        {sources.data?.length === 0 && <p className="text-sm text-muted">{t('integrations.none')}</p>}
        <ul className="divide-y">
          {sources.data?.map((source) => (
            <li key={source.id} className="grid gap-2 py-3">
              <div className="flex items-center gap-2"><strong className="text-sm">{source.name}</strong><Badge>{source.paused ? t('integrations.paused') : t('integrations.active')}</Badge></div>
              <p className="text-xs text-muted">{t('integrations.series', { count: source.item_count ?? 0 })}</p>
              <p className="text-xs text-muted">{source.last_success_at ? t('integrations.lastRefreshed', { time: format.dateTime(source.last_success_at) }) : t('integrations.awaiting')}</p>
              {source.error && (
                <p role="alert" className="text-sm text-danger">
                  {source.error === hostNotTrusted ? t('integrations.untrusted') : source.error}
                  {source.error === hostNotTrusted && administrator && (
                    <>
                      {' '}
                      <Link to="/admin/trusted-hosts" className="font-medium underline underline-offset-2">
                        {t('integrations.reviewHosts')}
                      </Link>
                    </>
                  )}
                </p>
              )}
              {canManage && <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={source.paused || refresh.isPending} onClick={() => refresh.mutate(source)}>{t('integrations.refreshNow')}</Button>
                <Button size="sm" disabled={pause.isPending} onClick={() => pause.mutate(source)}>{source.paused ? t('integrations.resume') : t('integrations.pause')}</Button>
                <Button size="sm" onClick={() => setEditing(source)}>{t('integrations.configure')}</Button>
                <Button size="sm" onClick={() => setRemoving(source)}>{t('common:actions.removeItem')}</Button>
              </div>}
            </li>
          ))}
        </ul>
        <p className="mt-3 text-xs text-muted">{t('integrations.schedule')}</p>
        {!!sources.data?.length && <Button className="mt-3" size="sm" disabled={download.isPending} onClick={() => download.mutate()}>{t('integrations.export')}</Button>}
      </SettingsSection>
      {[refresh, pause, remove, download].filter((mutation) => mutation.error).map((mutation, index) => <QueryFeedback key={index} query={mutation} />)}
      {operationId && <OperationProgress id={operationId} label={t('operations.kinds.integration_refresh')} />}
      {editing && <IntegrationForm listId={listId} integration={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} />}
      <ConfirmDialog open={!!removing} title={t('integrations.removeTitle')} description={t('integrations.removeDescription')} confirm={t('integrations.removeConfirm')} busy={remove.isPending} onOpenChange={(open) => !open && setRemoving(undefined)} onConfirm={() => removing && remove.mutate(removing)} />
    </div>
  );
}
function IntegrationForm({ listId, integration, onClose }: { listId: string; integration?: ListIntegration; onClose: () => void }) {
  const { t } = useTranslation(['lists', 'common']);
  const client = useQueryClient();
  const [name, setName] = useState(integration?.name ?? '');
  const [url, setURL] = useState('');
  const [reset, setReset] = useState(false);
  const { allowed, trust } = useTrustHost();
  const save = useMutation({
    mutationFn: () => api(`/v1/resources/${listId}/integrations${integration ? `/${integration.id}` : ''}`, {
      method: integration ? 'PATCH' : 'POST', ifMatch: integration?.version,
      body: integration ? { name: name.trim(), ...(url.trim() ? { configuration: { url: url.trim() }, reset } : {}) } : { kind: 'ical_https', name: name.trim(), configuration: { url: url.trim() } },
    }),
    onSuccess: async () => { setURL(''); await client.invalidateQueries({ queryKey: keys.resources }); onClose(); toast.success(integration ? t('integrations.updated') : t('integrations.added')); },
  });
  return (
    <FormDialog open title={integration ? t('integrations.configureTitle') : t('integrations.add')} submitLabel={integration ? t('common:actions.save') : t('integrations.add')} pending={save.isPending} onClose={onClose} onSubmit={() => save.mutate()}>
      <Label htmlFor="integration-kind">{t('integrations.kind')}</Label><Select id="integration-kind" value="ical_https" disabled><option value="ical_https">{t('integrations.url')}</option></Select>
      <Label htmlFor="integration-name">{t('common:fields.name')}</Label><Input id="integration-name" autoFocus required maxLength={255} value={name} onChange={(event) => setName(event.target.value)} />
      <Label htmlFor="integration-url">{integration ? t('integrations.replaceUrl') : t('integrations.url')}</Label>
      <Input id="integration-url" type="url" required={!integration} autoComplete="off" value={url} onChange={(event) => setURL(event.target.value)} placeholder={urlPlaceholder} />
      <p className="text-xs text-muted">{t('integrations.urlHint')}</p>
      {integration && <label className="flex items-start gap-2 text-sm"><Checkbox checked={reset} disabled={!url.trim()} onChange={(event) => setReset(event.target.checked)} />{t('integrations.reset')}</label>}
      <QueryFeedback query={save} />
      {allowed && !!save.error && problemMessage(save.error).includes("isn't a trusted host") && hostOf(url.trim()) && (
        <Button size="sm" disabled={trust.isPending} onClick={() => trust.mutate(hostOf(url.trim()), { onSuccess: () => save.mutate() })}>
          {t('integrations.trustAndAdd', { host: hostOf(url.trim()) })}
        </Button>
      )}
    </FormDialog>
  );
}
