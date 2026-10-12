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

export const Route = createFileRoute('/_app/settings/')({ component: ProfilePage });

function ProfilePage() {
  const queryClient = useQueryClient();
  const me = useQuery(meQuery);
  const [name, setName] = useState<string | null>(null);
  const displayName = name ?? me.data?.display_name ?? '';
  const save = useMutation({
    mutationFn: () => api<Me>('/v1/identity/me', { method: 'PATCH', body: { display_name: displayName.trim() } }).then((result) => result.data),
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.me, updated);
      setName(null);
      toast.success('Profile saved');
    },
  });
  return (
    <SettingsSection
      title="Profile"
      description="Your display name is visible to people in this organization."
      actions={
        <Button variant="primary" disabled={save.isPending || !displayName.trim()} onClick={() => save.mutate()}>
          Save
        </Button>
      }
    >
      <SettingRow id="display-name" label="Display name">
        <Input id="display-name" value={displayName} onChange={(event) => setName(event.target.value)} />
      </SettingRow>
      <SettingRow id="email" label="Email">
        <Input id="email" value={me.data?.email ?? ''} disabled />
      </SettingRow>
      <SettingRow id="subject" label="Subject">
        <Input id="subject" value={me.data?.subject ?? ''} disabled />
      </SettingRow>
    </SettingsSection>
  );
}
