import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { scopeCatalog, scopes } from '@/api/queries';
import type { CreatedAccessToken, AccessToken } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Alert } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { CopyField, ConfirmDialog } from '@/features/settings/section';
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { QueryFeedback, useHasScope } from './common';

export function AccessTokens() {
  const { t } = useTranslation(['admin', 'common']);
  const allowed = useHasScope('organization.manage');
  const client = useQueryClient();
  const format = useFormat();
  const list = useQuery({ queryKey: ['organization-tokens'], enabled: allowed, queryFn: async () => (await api<AccessToken[]>('/v1/identity/tokens')).data });
  const catalog = useQuery({ queryKey: ['scope-catalog'], queryFn: scopeCatalog, enabled: allowed });
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes, enabled: allowed });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [days, setDays] = useState('90');
  const [chosen, setChosen] = useState<string[]>([]);
  const [created, setCreated] = useState<CreatedAccessToken>();
  const [revoking, setRevoking] = useState<AccessToken>();
  const issue = useMutation({
    meta: { silent: true },
    mutationFn: async () => (await api<CreatedAccessToken>('/v1/identity/tokens', { method: 'POST', body: { name: name.trim(), scopes: chosen, expires_in_days: Number(days) } })).data,
    onSuccess: async result => { setCreating(false); setCreated(result); setName(''); setChosen([]); await client.invalidateQueries({ queryKey: ['organization-tokens'] }); },
  });
  const revoke = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/identity/tokens/${revoking!.id}`, { method: 'DELETE' }),
    onSuccess: async () => { setRevoking(undefined); await client.invalidateQueries({ queryKey: ['organization-tokens'] }); },
  });
  const closeSecret = () => { setCreated(undefined); issue.reset(); };
  if (!allowed) return <Alert>{t('tokens.needManage')}</Alert>;
  return <section className="grid gap-4">
    <div className="flex items-center justify-between gap-3"><div><h2 className="font-medium">{t('tokens.title')}</h2><p className="text-sm text-muted">{t('tokens.description')}</p></div><Button variant="primary" onClick={() => { issue.reset(); setCreating(true); }}>{t('tokens.new')}</Button></div>
    <QueryFeedback query={list} />
    <ul className="grid gap-2">{list.data?.map(token => <li key={token.id} className="flex items-start gap-3 rounded-md border p-3">
      <div className="min-w-0 flex-1"><p className="flex flex-wrap items-center gap-2 font-medium">{token.name}<Badge>{token.integration ? t('tokens.integration') : t('tokens.personal')}</Badge>{token.revoked_at && <Badge>{t('tokens.revoked')}</Badge>}</p><p className="break-all text-xs text-muted">{t('tokens.identity', { subject: token.subject, creator: token.created_by })}</p><p className="text-xs text-muted">{[`${token.prefix}…`, token.expires_at ? t('tokens.expires', { date: format.date(token.expires_at) }) : t('tokens.neverExpires'), token.last_used_at ? t('tokens.lastUsed', { date: format.dateTime(token.last_used_at) }) : t('tokens.neverUsed')].join(' · ')}</p><p className="break-words text-xs text-muted">{token.scopes?.join(', ')}</p></div>
      {!token.revoked_at && <Button size="sm" variant="danger" onClick={() => { revoke.reset(); setRevoking(token); }}>{t('tokens.revoke')}</Button>}
    </li>)}</ul>
    {list.isSuccess && !list.data.length && <p className="text-sm text-muted">{t('tokens.none')}</p>}
    <FormDialog open={creating} onClose={() => setCreating(false)} title={t('tokens.new')} description={t('tokens.newDescription')} submitLabel={t('tokens.create')} pending={issue.isPending} submitDisabled={!name.trim() || !chosen.length} onSubmit={() => issue.mutate()}>
      {issue.error && <Alert>{problemMessage(issue.error)}</Alert>}
      <Label htmlFor="integration-name">{t('tokens.name')}</Label><Input id="integration-name" value={name} onChange={event => setName(event.target.value)} required />
      <Label htmlFor="integration-expiry">{t('tokens.expiration')}</Label><Select id="integration-expiry" value={days} onChange={event => setDays(event.target.value)}><option value="30">{t('tokens.days', { count: 30 })}</option><option value="90">{t('tokens.days', { count: 90 })}</option><option value="365">{t('tokens.year')}</option><option value="0">{t('tokens.never')}</option></Select>
      <fieldset className="grid max-h-64 gap-1 overflow-auto"><legend className="mb-2 text-sm font-medium">{t('tokens.scopes')}</legend><QueryFeedback query={catalog} />{catalog.data?.filter(scope => mine.data?.includes(scope.name)).map(scope => <label key={scope.name} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={chosen.includes(scope.name)} onChange={event => setChosen(values => event.target.checked ? [...values, scope.name] : values.filter(value => value !== scope.name))} />{scope.name}</label>)}</fieldset>
    </FormDialog>
    <Dialog open={!!created} onOpenChange={open => !open && closeSecret()}><DialogContent><DialogHeader><DialogTitle>{t('tokens.saveTitle')}</DialogTitle><DialogDescription>{t('tokens.saveDescription')}</DialogDescription></DialogHeader>{created && <><CopyField label={t('tokens.secret')} value={created.secret} /><p className="break-all text-xs text-muted">{t('tokens.serviceIdentity', { subject: created.token.subject })}</p></>}<Button onClick={closeSecret}>{t('common:actions.done')}</Button></DialogContent></Dialog>
    <ConfirmDialog open={!!revoking} onOpenChange={open => !open && setRevoking(undefined)} title={t('tokens.revokeTitle', { name: revoking?.name ?? '' })} description={t('tokens.revokeDescription')} confirm={t('tokens.revokeConfirm')} busy={revoke.isPending} error={revoke.error} onConfirm={() => revoke.mutate()} />
  </section>;
}
