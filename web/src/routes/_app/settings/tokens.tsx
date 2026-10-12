import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { scopeCatalog, scopes, tokens } from '@/api/queries';
import type { CreatedRestToken } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/tokens')({ component: TokensPage });

function TokensPage() {
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['tokens'], queryFn: tokens });
  const catalog = useQuery({ queryKey: ['scope-catalog'], queryFn: scopeCatalog });
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes });
  const [name, setName] = useState('');
  const [days, setDays] = useState('30');
  const [chosen, setChosen] = useState<string[]>([]);
  const [custom, setCustom] = useState('');
  const [created, setCreated] = useState<CreatedRestToken | null>(null);

  const issue = useMutation({
    mutationFn: () => {
      const fromCatalog = chosen;
      const extra = custom.split(',').map((scope) => scope.trim()).filter(Boolean);
      const selected = [...new Set([...fromCatalog, ...extra])];
      const expires = Number(days);
      return api<CreatedRestToken>('/v1/identity/me/tokens', {
        method: 'POST',
        body: { name: name.trim(), scopes: selected, expires_in_days: Number.isInteger(expires) ? expires : undefined },
      }).then((result) => result.data);
    },
    onSuccess: async (token) => {
      setCreated(token);
      setName('');
      setChosen([]);
      setCustom('');
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/v1/identity/me/tokens/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tokens'] }),
  });

  const options = catalog.data ?? [];
  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title="New token"
        description="The secret is shown once. Scopes limit what the token can do."
        actions={
          <Button variant="primary" disabled={issue.isPending || !name.trim()} onClick={() => issue.mutate()}>
            Create token
          </Button>
        }
      >
        <SettingRow id="token-name" label="Name">
          <Input id="token-name" value={name} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow id="token-days" label="Expires in days">
          <Input id="token-days" inputMode="numeric" value={days} onChange={(event) => setDays(event.target.value)} />
        </SettingRow>
        <SettingRow id="token-scopes" label="Scopes" hint={mine.data ? `Your account has ${mine.data.length} scopes.` : undefined}>
          {options.length > 0 ? (
            <div className="flex max-h-40 flex-col gap-1 overflow-auto">
              {options.map((scope) => (
                <label key={scope.name} className="flex items-center gap-2 text-[13px]">
                  <input
                    type="checkbox"
                    checked={chosen.includes(scope.name)}
                    onChange={(event) => setChosen((current) => (event.target.checked ? [...current, scope.name] : current.filter((name) => name !== scope.name)))}
                  />
                  {scope.name}
                </label>
              ))}
            </div>
          ) : (
            <Input id="token-scopes" value={custom} onChange={(event) => setCustom(event.target.value)} placeholder="scope.read, scope.write" />
          )}
        </SettingRow>
        {created && <CopyField label="Secret" value={created.secret} />}
      </SettingsSection>
      <SettingsSection title="Tokens">
        <ul className="flex flex-col gap-2">
          {(list.data ?? []).map((token) => (
            <li key={token.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium">{token.name}</p>
                <p className="text-xs text-muted">
                  {token.prefix}… · expires {format.date(token.expires_at)}
                </p>
              </div>
              <Button size="sm" variant="danger" disabled={revoke.isPending} onClick={() => revoke.mutate(token.id)}>
                Revoke
              </Button>
            </li>
          ))}
          {list.isFetched && (list.data?.length ?? 0) === 0 && <li className="text-sm text-muted">No tokens.</li>}
        </ul>
      </SettingsSection>
    </div>
  );
}
