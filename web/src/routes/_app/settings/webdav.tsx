import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { authenticationMode } from '@/api/auth';
import { meQuery, scopes, tokens } from '@/api/queries';
import type { CreatedAccessToken } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { Trans, useTranslation } from '@/i18n';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/webdav')({ component: WebDAVPage });

function WebDAVPage() {
  return authenticationMode() === 'proxy' ? <ProxyWebDAV /> : <BearerWebDAV />;
}

function ProxyWebDAV() {
  const { t } = useTranslation('settings');
  const me = useQuery(meQuery);
  return (
    <SettingsSection
      title={t('webdav.proxyTitle')}
      description={t('webdav.proxyDescription')}
    >
      <p className="text-sm">
        <Trans t={t} i18nKey="webdav.username" values={{ subject: me.data?.subject ?? '' }} components={{ strong: <strong /> }} />
      </p>
      <p className="mt-2 break-all text-sm">{t('webdav.libraryAddress', { address: `${window.location.origin}/webdav/{${t('webdav.libraryId')}}/` })}</p>
      <p className="mt-2 text-xs text-muted">{t('webdav.proxyHint')}</p>
    </SettingsSection>
  );
}

function BearerWebDAV() {
  const { t } = useTranslation('settings');
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['tokens'], queryFn: tokens });
  const mine = useQuery({ queryKey: ['my-scopes'], queryFn: scopes });
  const [label, setLabel] = useState('');
  const [days, setDays] = useState('90');
  const [created, setCreated] = useState<CreatedAccessToken | null>(null);
  const tokenScopes = ['item.read', 'item.write', 'document.read', 'document.write'].filter(scope => mine.data?.includes(scope));
  const canRead = tokenScopes.includes('item.read') && tokenScopes.includes('document.read');
  const expires = Number(days);
  const validExpiry = Number.isInteger(expires) && expires >= 1 && expires <= 3650;
  const issue = useMutation({
    mutationFn: () =>
      api<CreatedAccessToken>('/v1/identity/me/tokens', {
        method: 'POST',
        body: {
          name: label.trim(),
          scopes: tokenScopes,
          expires_in_days: expires,
        },
      }).then((result) => result.data),
    onSuccess: async (credential) => {
      setCreated(credential);
      setLabel('');
      setDays('90');
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/v1/identity/me/tokens/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['tokens'] }),
  });
  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title={t('webdav.newTitle')}
        description={t('webdav.newDescription')}
        actions={
          <Button variant="primary" disabled={issue.isPending || !label.trim() || !canRead || !validExpiry} onClick={() => issue.mutate()}>
            {t('tokens.create')}
          </Button>
        }
      >
        <SettingRow id="dav-label" label={t('webdav.label')}>
          <Input id="dav-label" value={label} onChange={(event) => setLabel(event.target.value)} />
        </SettingRow>
        <SettingRow id="dav-expires" label={t('tokens.expiresInDays')} hint={t('webdav.expiresHint')}>
          <Input
            id="dav-expires"
            type="number"
            min={1}
            max={3650}
            value={days}
            onChange={(event) => setDays(event.target.value)}
          />
        </SettingRow>
        {mine.isSuccess && !canRead && <p className="text-sm text-muted">{t('webdav.readRequired')}</p>}
        {created?.secret && <CopyField label={t('webdav.password')} value={created.secret} />}
      </SettingsSection>
      <SettingsSection title={t('webdav.listTitle')}>
        <ul className="flex flex-col gap-2">
          {(list.data ?? []).map((credential) => (
            <li key={credential.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium">{credential.name}</p>
                <p className="text-xs text-muted">
                  {credential.expires_at
                    ? t('webdav.createdExpires', { date: format.date(credential.created_at), expires: format.date(credential.expires_at) })
                    : t('webdav.created', { date: format.date(credential.created_at) })}
                </p>
              </div>
              <Button
                size="sm"
                variant="danger"
                disabled={revoke.isPending}
                onClick={() => revoke.mutate(credential.id)}
              >
                {t('tokens.revoke')}
              </Button>
            </li>
          ))}
          {list.isFetched && (list.data?.length ?? 0) === 0 && (
            <li className="text-sm text-muted">{t('webdav.none')}</li>
          )}
        </ul>
      </SettingsSection>
    </div>
  );
}
