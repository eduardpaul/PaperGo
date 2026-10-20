import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { authenticationMode } from '@/api/auth';
import { meQuery, tokens } from '@/api/queries';
import type { CreatedAccessToken } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/webdav')({ component: WebDAVPage });

function WebDAVPage() {
  return authenticationMode() === 'proxy' ? <ProxyWebDAV /> : <BearerWebDAV />;
}

function ProxyWebDAV() {
  const me = useQuery(meQuery);
  return (
    <SettingsSection
      title="WebDAV through your proxy"
      description="Use your reverse-proxy username and password over HTTPS to open a WebDAV-enabled library in Explorer or Finder."
    >
      <p className="text-sm">
        Username: <strong>{me.data?.subject}</strong>
      </p>
      <p className="mt-2 break-all text-sm">Library address: {window.location.origin}/webdav/&#123;library-id&#125;/</p>
      <p className="mt-2 text-xs text-muted">Your proxy manages credentials for both the browser and WebDAV.</p>
    </SettingsSection>
  );
}

function BearerWebDAV() {
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['tokens'], queryFn: tokens });
  const [label, setLabel] = useState('');
  const [expires, setExpires] = useState('');
  const [created, setCreated] = useState<CreatedAccessToken | null>(null);
  const issue = useMutation({
    mutationFn: () =>
      api<CreatedAccessToken>('/v1/identity/me/tokens', {
        method: 'POST',
        body: {
          name: label.trim(),
          scopes: ['webdav.access'],
          expires_at: expires ? new Date(expires).toISOString() : undefined,
        },
      }).then((result) => result.data),
    onSuccess: async (credential) => {
      setCreated(credential);
      setLabel('');
      setExpires('');
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
        title="New WebDAV access token"
        description="Use this token as the password with your account subject to open a library in Explorer or Finder. It is shown once."
        actions={
          <Button variant="primary" disabled={issue.isPending || !label.trim()} onClick={() => issue.mutate()}>
            Create token
          </Button>
        }
      >
        <SettingRow id="dav-label" label="Label">
          <Input id="dav-label" value={label} onChange={(event) => setLabel(event.target.value)} />
        </SettingRow>
        <SettingRow id="dav-expires" label="Expires" hint="Optional.">
          <Input
            id="dav-expires"
            type="datetime-local"
            value={expires}
            onChange={(event) => setExpires(event.target.value)}
          />
        </SettingRow>
        {created?.secret && <CopyField label="Password" value={created.secret} />}
      </SettingsSection>
      <SettingsSection title="Access tokens">
        <ul className="flex flex-col gap-2">
          {(list.data ?? []).map((credential) => (
            <li key={credential.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium">{credential.name}</p>
                <p className="text-xs text-muted">
                  Created {format.date(credential.created_at)}
                  {credential.expires_at ? ` · expires ${format.date(credential.expires_at)}` : ''}
                </p>
              </div>
              <Button
                size="sm"
                variant="danger"
                disabled={revoke.isPending}
                onClick={() => revoke.mutate(credential.id)}
              >
                Revoke
              </Button>
            </li>
          ))}
          {list.isFetched && (list.data?.length ?? 0) === 0 && (
            <li className="text-sm text-muted">No access tokens.</li>
          )}
        </ul>
      </SettingsSection>
    </div>
  );
}
