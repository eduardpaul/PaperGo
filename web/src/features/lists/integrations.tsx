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
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';

/** The error of an integration whose host the organization stopped trusting. */
const hostNotTrusted = 'Host no longer trusted';

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
    onSuccess: (op) => { setOperationId(op.id); toast.success('Refresh queued'); },
  });
  const pause = useMutation({
    mutationFn: (source: ListIntegration) => api(`${path}/${source.id}`, { method: 'PATCH', ifMatch: source.version, body: { paused: !source.paused } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['resources', listId, 'integrations'] }),
  });
  const remove = useMutation({
    mutationFn: (source: ListIntegration) => api(`${path}/${source.id}`, { method: 'DELETE', ifMatch: source.version }),
    onSuccess: async () => { setRemoving(undefined); await client.invalidateQueries({ queryKey: keys.resources }); toast.success('Integration removed. Its items are now editable.'); },
  });
  const download = useMutation({ mutationFn: () => downloadFile(`/v1/resources/${listId}/calendar/export`, 'calendar.ics') });
  return (
    <div className="grid gap-4">
      <SettingsSection title="Integrations" description="Connect this list to an external source. Each integration keeps its own items and refresh status."
        actions={canManage && <Button variant="primary" onClick={() => setEditing('new')}>Add integration</Button>}>
        <QueryFeedback query={sources} />
        {sources.data?.length === 0 && <p className="text-sm text-muted">No integrations configured.</p>}
        <ul className="divide-y">
          {sources.data?.map((source) => (
            <li key={source.id} className="grid gap-2 py-3">
              <div className="flex items-center gap-2"><strong className="text-sm">{source.name}</strong><Badge>{source.paused ? 'Paused' : 'Active'}</Badge></div>
              <p className="text-xs text-muted">HTTPS iCalendar · {source.item_count ?? 0} event series</p>
              <p className="text-xs text-muted">{source.last_success_at ? `Last refreshed ${format.dateTime(source.last_success_at)}` : 'Awaiting first successful refresh'}</p>
              {source.error && (
                <p role="alert" className="text-sm text-danger">
                  {source.error === hostNotTrusted ? "This feed's host is no longer trusted. It fetches nothing and keeps its events until the host is trusted again." : source.error}
                  {source.error === hostNotTrusted && administrator && (
                    <>
                      {' '}
                      <Link to="/admin/trusted-hosts" className="font-medium underline underline-offset-2">
                        Review trusted hosts
                      </Link>
                    </>
                  )}
                </p>
              )}
              {canManage && <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={source.paused || refresh.isPending} onClick={() => refresh.mutate(source)}>Refresh now</Button>
                <Button size="sm" disabled={pause.isPending} onClick={() => pause.mutate(source)}>{source.paused ? 'Resume' : 'Pause'}</Button>
                <Button size="sm" onClick={() => setEditing(source)}>Configure</Button>
                <Button size="sm" onClick={() => setRemoving(source)}>Remove</Button>
              </div>}
            </li>
          ))}
        </ul>
        <p className="mt-3 text-xs text-muted">Active feeds refresh every 15 minutes while the workspace’s Refresh list integrations workflow is enabled. Paused feeds keep their items source-managed.</p>
        {!!sources.data?.length && <Button className="mt-3" size="sm" disabled={download.isPending} onClick={() => download.mutate()}>Export calendar</Button>}
      </SettingsSection>
      {[refresh, pause, remove, download].filter((mutation) => mutation.error).map((mutation, index) => <QueryFeedback key={index} query={mutation} />)}
      {operationId && <OperationProgress id={operationId} label="Refresh integration" />}
      {editing && <IntegrationForm listId={listId} integration={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} />}
      <ConfirmDialog open={!!removing} title="Remove integration?" description="Its existing items will stay in this list as editable local items. Refreshing will stop." confirm="Remove integration" busy={remove.isPending} onOpenChange={(open) => !open && setRemoving(undefined)} onConfirm={() => removing && remove.mutate(removing)} />
    </div>
  );
}
function IntegrationForm({ listId, integration, onClose }: { listId: string; integration?: ListIntegration; onClose: () => void }) {
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
    onSuccess: async () => { setURL(''); await client.invalidateQueries({ queryKey: keys.resources }); onClose(); toast.success(integration ? 'Integration updated' : 'Integration added. First refresh queued.'); },
  });
  return (
    <FormDialog open title={integration ? 'Configure integration' : 'Add integration'} submitLabel={integration ? 'Save' : 'Add integration'} pending={save.isPending} onClose={onClose} onSubmit={() => save.mutate()}>
      <Label htmlFor="integration-kind">Integration</Label><Select id="integration-kind" value="ical_https" disabled><option value="ical_https">HTTPS iCalendar URL</option></Select>
      <Label htmlFor="integration-name">Name</Label><Input id="integration-name" autoFocus required maxLength={255} value={name} onChange={(event) => setName(event.target.value)} />
      <Label htmlFor="integration-url">{integration ? 'Replace URL (optional)' : 'HTTPS iCalendar URL'}</Label>
      <Input id="integration-url" type="url" required={!integration} autoComplete="off" value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://…" />
      <p className="text-xs text-muted">Use a public HTTPS iCalendar feed, including Google Calendar’s secret iCal address. The stored URL is kept private. Adding the integration attaches the Event content type to this ordinary list.</p>
      {integration && <label className="flex items-start gap-2 text-sm"><Checkbox checked={reset} disabled={!url.trim()} onChange={(event) => setReset(event.target.checked)} />This is a different feed: keep existing items local and import with new identities.</label>}
      <QueryFeedback query={save} />
      {allowed && !!save.error && problemMessage(save.error).includes("isn't a trusted host") && hostOf(url.trim()) && (
        <Button size="sm" disabled={trust.isPending} onClick={() => trust.mutate(hostOf(url.trim()), { onSuccess: () => save.mutate() })}>
          Trust {hostOf(url.trim())} and add
        </Button>
      )}
    </FormDialog>
  );
}
