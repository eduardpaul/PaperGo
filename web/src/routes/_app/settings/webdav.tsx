import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { api } from '@/api/http';
import { webdavCredentials } from '@/api/queries';
import type { WebDAVCredential } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { CopyField, SettingRow, SettingsSection } from '@/features/settings/section';
import { useFormat } from '@/lib/preferences';

export const Route = createFileRoute('/_app/settings/webdav')({ component: WebDAVPage });

function WebDAVPage() {
  const format = useFormat();
  const queryClient = useQueryClient();
  const list = useQuery({ queryKey: ['webdav-credentials'], queryFn: webdavCredentials });
  const [label, setLabel] = useState('');
  const [expires, setExpires] = useState('');
  const [created, setCreated] = useState<WebDAVCredential | null>(null);
  const issue = useMutation({
    mutationFn: () =>
      api<WebDAVCredential>('/v1/webdav-credentials', {
        method: 'POST',
        body: { label: label.trim(), expires_at: expires ? new Date(expires).toISOString() : undefined },
      }).then((result) => result.data),
    onSuccess: async (credential) => {
      setCreated(credential);
      setLabel('');
      setExpires('');
      await queryClient.invalidateQueries({ queryKey: ['webdav-credentials'] });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/v1/webdav-credentials/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['webdav-credentials'] }),
  });
  return (
    <div className="flex flex-col gap-5">
      <SettingsSection
        title="New WebDAV password"
        description="Use this password with your account subject to open a library in Explorer or Finder. It is shown once."
        actions={
          <Button variant="primary" disabled={issue.isPending || !label.trim()} onClick={() => issue.mutate()}>
            Create password
          </Button>
        }
      >
        <SettingRow id="dav-label" label="Label">
          <Input id="dav-label" value={label} onChange={(event) => setLabel(event.target.value)} />
        </SettingRow>
        <SettingRow id="dav-expires" label="Expires" hint="Optional.">
          <Input id="dav-expires" type="datetime-local" value={expires} onChange={(event) => setExpires(event.target.value)} />
        </SettingRow>
        {created?.password && <CopyField label="Password" value={created.password} />}
      </SettingsSection>
      <SettingsSection title="Passwords">
        <ul className="flex flex-col gap-2">
          {(list.data ?? []).map((credential) => (
            <li key={credential.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px] font-medium">{credential.label}</p>
                <p className="text-xs text-muted">
                  Created {format.date(credential.created_at)}
                  {credential.expires_at ? ` · expires ${format.date(credential.expires_at)}` : ''}
                </p>
              </div>
              <Button size="sm" variant="danger" disabled={revoke.isPending} onClick={() => revoke.mutate(credential.id)}>
                Revoke
              </Button>
            </li>
          ))}
          {list.isFetched && (list.data?.length ?? 0) === 0 && <li className="text-sm text-muted">No WebDAV passwords.</li>}
        </ul>
      </SettingsSection>
    </div>
  );
}
