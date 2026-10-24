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
import { matches } from '@/features/files/trust';
import { ConfirmDialog, SettingsSection } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';

interface HostUse {
  host: string;
  integrations: number;
}

/** The form an entry is stored in, or why it cannot be one; the server has the last word. */
export function hostEntry(raw: string): { entry?: string; problem?: string } {
  const value = raw.trim().toLowerCase().replace(/\.$/, '');
  if (!value) return {};
  if (value === '*') return { entry: '*' };
  if (/^[a-z][a-z0-9+.-]*:\/\//.test(value) || /[/:@?#\s]/.test(value)) return { problem: 'Use a host name without a scheme, port or path, such as docs.example.com.' };
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) return { problem: "IP addresses can't be trusted; use a host name." };
  const name = value.startsWith('*.') ? value.slice(2) : value;
  if (name.includes('*')) return { problem: 'A wildcard is * on its own, or starts the entry, as in *.example.com.' };
  if (!name.includes('.')) return { problem: 'Use a full host name, such as docs.example.com.' };
  let ascii: string;
  try {
    ascii = new URL(`https://${name}`).hostname;
  } catch {
    return { problem: `${name} is not a valid host name.` };
  }
  return { entry: value.startsWith('*.') ? `*.${ascii}` : ascii };
}

/**
 * Administration → Trusted hosts: the organization's one list of outside
 * hosts that documents, URL fields and integrations reach without asking.
 */
export function TrustedHostsPage() {
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
        toast.error('Someone else changed the list. It has been reloaded; try again.');
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
    return (usage.data ?? []).filter((use) => matches(hosts, use.host) && !matches(rest, use.host));
  };
  const usedBy = (entry: string) => (usage.data ?? []).filter((use) => matches([entry], use.host)).reduce((sum, use) => sum + use.integrations, 0);
  const remove = (entry: string) => (stops(entry).length ? setRemoving(entry) : save.mutate(hosts.filter((host) => host !== entry)));
  const stopping = removing ? stops(removing) : [];

  return (
    <SettingsSection
      title="Trusted hosts"
      description="PaperGo shows and fetches content from these hosts without asking. Links to other hosts ask before they open, their images wait to be shown, and integrations can't use them."
    >
      <QueryFeedback query={list} />
      {list.data && (
        <div className="grid gap-3">
          {hosts.includes('*') && (
            <Alert tone="warning" className="flex items-start gap-2">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              <span>
                <strong>*</strong> trusts every public host. Add the hosts your organization uses, then remove <strong>*</strong> to trust only those.
              </span>
            </Alert>
          )}
          {hosts.length === 0 ? (
            <p className="rounded-md border border-dashed px-3 py-4 text-center text-[13px] text-muted">No host is trusted. Every link asks first, no remote image loads, and integrations can't fetch.</p>
          ) : (
            <ul className="divide-y rounded-md border" aria-label="Trusted hosts">
              {hosts.map((entry) => {
                const used = usedBy(entry);
                return (
                  <li key={entry} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                    <Globe className="size-4 shrink-0 text-muted" />
                    <span className="min-w-0 flex-1 truncate font-mono">{entry}</span>
                    {canEdit && <span className="text-xs text-muted">{used ? `Used by ${used} integration${used === 1 ? '' : 's'}` : '—'}</span>}
                    {canEdit && (
                      <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => remove(entry)} aria-label={`Remove ${entry}`}>
                        Remove
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
                <Input aria-label="Host to trust" placeholder="docs.example.com, *.example.com or *" value={draft} onChange={(event) => setDraft(event.target.value)} aria-invalid={!!parsed.problem || duplicate} />
                <Button type="submit" variant="primary" disabled={!parsed.entry || duplicate || save.isPending}>
                  <Plus /> Add
                </Button>
              </div>
              {(parsed.problem || duplicate) && <p className="text-xs text-danger">{parsed.problem ?? `${parsed.entry} is already trusted.`}</p>}
              {parsed.entry?.startsWith('*.') && <p className="text-xs text-muted">Trusts every subdomain of {parsed.entry.slice(2)}, but not {parsed.entry.slice(2)} itself.</p>}
            </form>
          )}
          {save.isError && !isApiError(save.error, 412) && <Alert>{problemMessage(save.error)}</Alert>}
          <p className="text-xs text-muted">Applies to images and links in Markdown, links in PDFs, URL fields and calendar integrations. Server-side fetches still reach only public addresses over HTTPS.</p>
        </div>
      )}
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(open) => !open && setRemoving(undefined)}
        title={`Stop trusting ${removing}?`}
        description={
          <>
            {stopping.map((use) => `${use.integrations} integration${use.integrations === 1 ? '' : 's'} on ${use.host}`).join(', ')} will stop refreshing and show “Host no longer trusted”. Their items stay, and they resume when the host is trusted again.
          </>
        }
        confirm="Remove"
        busy={save.isPending}
        onConfirm={() => removing && save.mutate(hosts.filter((host) => host !== removing))}
      />
    </SettingsSection>
  );
}
