import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Globe, Plus, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api, isApiError } from '@/api/http';
import { trustedHostsQuery } from '@/api/queries';
import type { TrustedHosts } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Input } from '@/components/ui/input';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { serverMatches } from '@/features/files/trust';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { i18n, Trans, useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';

/** An outside host features reach, with how many uses of each feature reach it. */
interface HostUse {
  host: string;
  uses: Record<string, number>;
}

const total = (use: HostUse) => Object.values(use.uses).reduce((sum, n) => sum + n, 0);

/** The form an entry is stored in, or why it cannot be one; the server has the last word. */
export function hostEntry(raw: string): { entry?: string; problem?: string } {
  const value = raw.trim().toLowerCase().replace(/\.$/, '');
  if (!value) return {};
  if (value === '*') return { entry: '*' };
  if (/^[a-z][a-z0-9+.-]*:\/\//.test(value) || /[/:@?#\s]/.test(value)) return { problem: i18n.t('admin:trustedHosts.problem.url') };
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return { problem: i18n.t('admin:trustedHosts.problem.ip') };
  const name = value.startsWith('*.') ? value.slice(2) : value;
  if (name.includes('*')) return { problem: i18n.t('admin:trustedHosts.problem.wildcard') };
  if (!name.includes('.')) return { problem: i18n.t('admin:trustedHosts.problem.full') };
  let ascii: string;
  try {
    ascii = new URL(`https://${name}`).hostname;
  } catch {
    return { problem: i18n.t('admin:trustedHosts.problem.invalid', { name }) };
  }
  return { entry: value.startsWith('*.') ? `*.${ascii}` : ascii };
}

/**
 * Administration → Trusted hosts: the organization's one list of outside
 * hosts that documents, URL fields and integrations reach without asking.
 */
export function TrustedHostsPage() {
  const { t } = useTranslation(['admin', 'common']);
  const client = useQueryClient();
  const canEdit = useHasScope('organization.manage');
  const list = useQuery(trustedHostsQuery);
  const usage = useQuery({
    queryKey: ['organization', 'settings', 'trusted-hosts', 'usage'],
    queryFn: async () => (await api<{ value: HostUse[] }>('/v1/organization/settings/trusted-hosts/usage')).data.value,
    enabled: canEdit,
  });
  const [draft, setDraft] = useState('');
  const [removing, setRemoving] = useState<string>();
  const save = useMutation({
    meta: { silent: true },
    mutationFn: async (hosts: string[]) => (await api<TrustedHosts>('/v1/organization/settings/trusted-hosts', { method: 'PUT', ifMatch: list.data?.version ?? 0, body: { hosts } })).data,
    onSuccess: (saved) => {
      client.setQueryData(trustedHostsQuery.queryKey, saved);
      setRemoving(undefined);
    },
    onError: (error) => {
      if (isApiError(error, 412)) {
        void client.invalidateQueries({ queryKey: trustedHostsQuery.queryKey });
        toast.error(t('trustedHosts.reloaded'));
      }
    },
  });

  const hosts = list.data?.hosts ?? [];
  const parsed = hostEntry(draft);
  const duplicate = !!parsed.entry && hosts.includes(parsed.entry);
  const add = () => {
    if (!parsed.entry || duplicate) return;
    save.mutate([...hosts, parsed.entry], { onSuccess: () => setDraft('') });
  };
  // What removing an entry stops: hosts in use that the list trusts now and would not after.
  const stops = (entry: string) => {
    const rest = hosts.filter((host) => host !== entry);
    return (usage.data ?? []).filter((use) => serverMatches(hosts, use.host) && !serverMatches(rest, use.host));
  };
  const usedBy = (entry: string) => (usage.data ?? []).filter((use) => serverMatches([entry], use.host)).reduce((sum, use) => sum + total(use), 0);
  const remove = (entry: string) => (stops(entry).length ? setRemoving(entry) : save.mutate(hosts.filter((host) => host !== entry)));
  const stopping = removing ? stops(removing) : [];

  return (
    <SettingsSection
      title={t('trustedHosts.title')}
      description={t('trustedHosts.description')}
    >
      <QueryFeedback query={list} />
      {list.data && (
        <div className="grid gap-3">
          {hosts.includes('*') && (
            <Alert tone="warning" className="flex items-start gap-2">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <span>
                <Trans t={t} i18nKey="trustedHosts.wildcardWarning" components={{ strong: <strong /> }} />
              </span>
            </Alert>
          )}
          {hosts.length === 0 ? (
            <p className="rounded-md border border-dashed px-3 py-4 text-center text-[13px] text-muted">{t('trustedHosts.empty')}</p>
          ) : (
            <ul className="divide-y rounded-md border" aria-label={t('trustedHosts.title')}>
              {hosts.map((entry) => {
                const used = usedBy(entry);
                return (
                  <li key={entry} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                    <Globe className="size-4 shrink-0 text-muted" />
                    <span className="min-w-0 flex-1 truncate font-mono">{entry}</span>
                    {canEdit && <span className="text-xs text-muted">{used ? t('trustedHosts.usedBy', { count: used }) : '—'}</span>}
                    {canEdit && (
                      <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => remove(entry)} aria-label={t('trustedHosts.remove', { entry })}>
                        {t('common:actions.removeItem')}
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {canEdit && (
            <form
              className="grid gap-1.5"
              onSubmit={(event) => {
                event.preventDefault();
                add();
              }}
            >
              <div className="flex gap-2">
                <Input aria-label={t('trustedHosts.hostToTrust')} placeholder={t('trustedHosts.placeholder')} value={draft} onChange={(event) => setDraft(event.target.value)} aria-invalid={!!parsed.problem || duplicate} />
                <Button type="submit" variant="primary" disabled={!parsed.entry || duplicate || save.isPending}>
                  <Plus /> {t('trustedHosts.add')}
                </Button>
              </div>
              {(parsed.problem || duplicate) && <p className="text-xs text-danger">{parsed.problem ?? t('trustedHosts.duplicate', { entry: parsed.entry })}</p>}
              {parsed.entry?.startsWith('*.') && <p className="text-xs text-muted">{t('trustedHosts.subdomains', { host: parsed.entry.slice(2) })}</p>}
            </form>
          )}
          {save.isError && !isApiError(save.error, 412) && <Alert>{problemMessage(save.error)}</Alert>}
          <p className="text-xs text-muted">{t('trustedHosts.appliesTo')}</p>
        </div>
      )}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && setRemoving(undefined)}
        title={t('trustedHosts.stopTitle', { entry: removing })}
        description={t('trustedHosts.stopDescription', {
          uses: stopping
            .flatMap((use) =>
              Object.entries(use.uses).map(([feature, count]) => {
                if (feature === 'integrations') return t('trustedHosts.stopIntegrations', { count, host: use.host });
                if (feature === 'connections') return t('trustedHosts.stopConnections', { count, host: use.host });
                return t('trustedHosts.stopUses', { count, host: use.host });
              }),
            )
            .join(', '),
        })}
        confirm={t('common:actions.removeItem')}
        busy={save.isPending}
        onConfirm={() => removing && save.mutate(hosts.filter((host) => host !== removing))}
      />
    </SettingsSection>
  );
}
