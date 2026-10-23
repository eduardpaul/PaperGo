import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { QueryFeedback, useHasScope } from '@/features/admin/common';
import { PreferencesForm } from '@/features/settings/preferences-form';
import { SettingRow, SettingsSection } from '@/features/settings/section';
import { OrganizationCommentSettingsForm } from '@/features/comments/settings';

interface Organization {
  id: string;
  identifier: string;
  display_name: string;
  description: string;
  version: number;
}
export const Route = createFileRoute('/_app/admin/')({ component: OrganizationPage });

function OrganizationPage() {
  const organization = useQuery({
    queryKey: ['organization', 'profile'],
    queryFn: async () => (await api<Organization>('/v1/organization')).data,
  });
  const canEdit = useHasScope('organization.manage');
  return (
    <>
      <QueryFeedback query={organization} />
      {organization.data && (
        <OrganizationForm key={organization.data.version} organization={organization.data} canEdit={canEdit} />
      )}
      <PreferencesForm organization canEdit={canEdit} />
      <OrganizationCommentSettingsForm canEdit={canEdit} />
    </>
  );
}

function OrganizationForm({ organization, canEdit }: { organization: Organization; canEdit: boolean }) {
  const client = useQueryClient();
  const [identifier, setIdentifier] = useState(organization.identifier);
  const [name, setName] = useState(organization.display_name);
  const [description, setDescription] = useState(organization.description);
  const save = useMutation({
    mutationFn: () =>
      api('/v1/organization', {
        method: 'PATCH',
        ifMatch: organization.version,
        body: { identifier: identifier.trim(), display_name: name.trim(), description },
      }),
    onSuccess: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: ['organization', 'profile'] }),
        client.invalidateQueries({ queryKey: keys.me }),
      ]);
      toast.success('Organization saved');
    },
  });
  return (
    <SettingsSection
      title="Organization"
      actions={
        canEdit && (
          <Button
            variant="primary"
            disabled={save.isPending || !name.trim() || !identifier.trim()}
            onClick={() => save.mutate()}
          >
            Save organization
          </Button>
        )
      }
    >
      <fieldset disabled={!canEdit}>
        <SettingRow id="org-identifier" label="Identifier">
          <Input id="org-identifier" value={identifier} onChange={(event) => setIdentifier(event.target.value)} />
        </SettingRow>
        <SettingRow id="org-name" label="Display name">
          <Input id="org-name" value={name} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow id="org-description" label="Description">
          <Textarea id="org-description" value={description} onChange={(event) => setDescription(event.target.value)} />
        </SettingRow>
      </fieldset>
    </SettingsSection>
  );
}
