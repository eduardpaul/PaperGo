import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, meQuery } from '@/api/queries';
import type { Me } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { SettingRow, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/settings/')({ component: ProfilePage });

function ProfilePage() {
  const { t } = useTranslation(['settings', 'common']);
  const queryClient = useQueryClient();
  const me = useQuery(meQuery);
  const [name, setName] = useState<string | null>(null);
  const displayName = name ?? me.data?.display_name ?? '';
  const save = useMutation({
    mutationFn: () => api<Me>('/v1/identity/me', { method: 'PATCH', body: { display_name: displayName.trim() } }).then((result) => result.data),
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.me, updated);
      setName(null);
      toast.success(t('profile.saved'));
    },
  });
  return (
    <SettingsSection
      title={t('profile.title')}
      description={t('profile.description')}
      actions={
        <Button variant="primary" disabled={save.isPending || !displayName.trim()} onClick={() => save.mutate()}>
          {t('common:actions.save')}
        </Button>
      }
    >
      <SettingRow id="display-name" label={t('profile.displayName')}>
        <Input id="display-name" value={displayName} onChange={(event) => setName(event.target.value)} />
      </SettingRow>
      <SettingRow id="email" label={t('profile.email')}>
        <Input id="email" value={me.data?.email ?? ''} disabled />
      </SettingRow>
      <SettingRow id="subject" label={t('profile.subject')}>
        <Input id="subject" value={me.data?.subject ?? ''} disabled />
      </SettingRow>
    </SettingsSection>
  );
}
