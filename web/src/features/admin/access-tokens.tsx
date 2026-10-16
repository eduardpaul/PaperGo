import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { scopeCatalog, scopes } from '@/api/queries';
import type { CreatedRestToken, RestToken } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Alert } from '@/components/ui/feedback';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { CopyField, ConfirmDialog } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';
import { useFormat } from '@/lib/preferences';
import { QueryFeedback, useHasScope } from './common';

export function AccessTokens() {
  const allowed = useHasScope('organization.manage');
  const client = useQueryClient();
  const format = useFormat();
  const list = useQuery({ queryKey: ['organization-tokens'], enabled: allowed, queryFn: async () => (await api<RestToken[]>('/v1/identity/tokens')).data });
  const catalog = useQuery({ queryKey: ['scope-catalog'], queryFn: scopeCatalog, enabled: allowed });
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes, enabled: allowed });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [days, setDays] = useState('90');
  const [chosen, setChosen] = useState<string[]>([]);
  const [created, setCreated] = useState<CreatedRestToken>();
  const [revoking, setRevoking] = useState<RestToken>();
  const issue = useMutation({
    meta: { silent: true },
    mutationFn: async () => (await api<CreatedRestToken>('/v1/identity/tokens', { method: 'POST', body: { name: name.trim(), scopes: chosen, expires_in_days: Number(days) } })).data,
    onSuccess: async result => { setCreating(false); setCreated(result); setName(''); setChosen([]); await client.invalidateQueries({ queryKey: ['organization-tokens'] }); },
  });
  const revoke = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/identity/tokens/${revoking!.id}`, { method: 'DELETE' }),
    onSuccess: async () => { setRevoking(undefined); await client.invalidateQueries({ queryKey: ['organization-tokens'] }); },
  });
  const closeSecret = () => { setCreated(undefined); issue.reset(); };
  if (!allowed) return <Alert>Organization management permission is required.</Alert>;
  return <section className="grid gap-4">
    <div className="flex items-center justify-between gap-3"><div><h2 className="font-medium">Access tokens</h2><p className="text-sm text-muted">Create scoped credentials for integrations. Each integration gets its own service identity. Grant that identity access to the workspaces it needs.</p></div><Button variant="primary" onClick={() => { issue.reset(); setCreating(true); }}>New integration token</Button></div>
    <QueryFeedback query={list} />
    <ul className="grid gap-2">{list.data?.map(token => <li key={token.id} className="flex items-start gap-3 rounded-md border p-3">
      <div className="min-w-0 flex-1"><p className="flex flex-wrap items-center gap-2 font-medium">{token.name}<Badge>{token.integration ? 'Integration' : 'Personal'}</Badge>{token.revoked_at && <Badge>Revoked</Badge>}</p><p className="break-all text-xs text-muted">Identity: {token.subject} · Issued by {token.created_by}</p><p className="text-xs text-muted">{token.prefix}… · {token.expires_at ? `Expires ${format.date(token.expires_at)}` : 'Never expires'} · {token.last_used_at ? `Last used ${format.dateTime(token.last_used_at)}` : 'Never used'}</p><p className="break-words text-xs text-muted">{token.scopes?.join(', ')}</p></div>
      {!token.revoked_at && <Button size="sm" variant="danger" onClick={() => { revoke.reset(); setRevoking(token); }}>Revoke</Button>}
    </li>)}</ul>
    {list.isSuccess && !list.data.length && <p className="text-sm text-muted">No access tokens.</p>}
    <FormDialog open={creating} onClose={() => setCreating(false)} title="New integration token" description="The secret is shown once. Scopes limit operations; workspace permissions still apply. Select workspace.manage only when the integration needs organization-wide content access." submitLabel="Create token" pending={issue.isPending} submitDisabled={!name.trim() || !chosen.length} onSubmit={() => issue.mutate()}>
      {issue.error && <Alert>{problemMessage(issue.error)}</Alert>}
      <Label htmlFor="integration-name">Name</Label><Input id="integration-name" value={name} onChange={event => setName(event.target.value)} required />
      <Label htmlFor="integration-expiry">Expiration</Label><Select id="integration-expiry" value={days} onChange={event => setDays(event.target.value)}><option value="30">30 days</option><option value="90">90 days</option><option value="365">1 year</option><option value="0">Never</option></Select>
      <fieldset className="grid max-h-64 gap-1 overflow-auto"><legend className="mb-2 text-sm font-medium">Scopes</legend><QueryFeedback query={catalog} />{catalog.data?.filter(scope => mine.data?.includes(scope.name)).map(scope => <label key={scope.name} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={chosen.includes(scope.name)} onChange={event => setChosen(values => event.target.checked ? [...values, scope.name] : values.filter(value => value !== scope.name))} />{scope.name}</label>)}</fieldset>
    </FormDialog>
    <Dialog open={!!created} onOpenChange={open => !open && closeSecret()}><DialogContent><DialogHeader><DialogTitle>Save your integration token</DialogTitle><DialogDescription>Copy this secret now. It will not be shown again.</DialogDescription></DialogHeader>{created && <><CopyField label="Secret" value={created.secret} /><p className="break-all text-xs text-muted">Service identity: {created.token.subject}</p></>}<Button onClick={closeSecret}>Done</Button></DialogContent></Dialog>
    <ConfirmDialog open={!!revoking} onOpenChange={open => !open && setRevoking(undefined)} title={`Revoke “${revoking?.name ?? ''}”?`} description="This credential stops authenticating immediately, including queued work that revalidates it." confirm="Revoke token" busy={revoke.isPending} error={revoke.error} onConfirm={() => revoke.mutate()} />
  </section>;
}
