import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { SmartFolderPackages } from '@/features/smart-folders/packages';
import { SettingRow, SettingsSection } from '@/features/settings/section';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/')({ component: GeneralPage });

function GeneralPage() {
  const { workspaceId } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const [name, setName] = useState<string | null>(null);
  const [description, setDescription] = useState<string | null>(null);
  const currentName = name ?? workspace.data?.name ?? '';
  const currentDescription = description ?? workspace.data?.description ?? '';
  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, string> = {};
      if (workspace.data && currentName.trim() !== workspace.data.name) body.name = currentName.trim();
      if (workspace.data && currentDescription !== (workspace.data.description ?? ''))
        body.description = currentDescription;
      return api<Resource>(`/v1/resources/${workspaceId}`, {
        method: 'PATCH',
        ifMatch: workspace.data?.version,
        body,
      }).then((result) => result.data);
    },
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.resource(workspaceId), updated);
      await queryClient.invalidateQueries({ queryKey: keys.workspaces });
      setName(null);
      setDescription(null);
      toast.success('Workspace saved');
    },
  });
  const remove = useMutation({
    mutationFn: () => api(`/v1/resources/${workspaceId}`, { method: 'DELETE', ifMatch: workspace.data?.version }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.workspaces });
      void navigate({ to: '/w' });
    },
  });
  if (!workspace.data) return null;
  return (
    <div className="grid gap-5">
      <SettingsSection
        title="General"
        actions={
          <Button variant="primary" disabled={save.isPending || !currentName.trim()} onClick={() => save.mutate()}>
            Save
          </Button>
        }
      >
        <SettingRow id="ws-name" label="Name">
          <Input id="ws-name" value={currentName} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow id="ws-description" label="Description">
          <Textarea
            id="ws-description"
            value={currentDescription}
            onChange={(event) => setDescription(event.target.value)}
          />
        </SettingRow>
        {!workspace.data.personal_owner_id && (
          <div className="pt-2">
            <Button variant="danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
              Delete workspace
            </Button>
          </div>
        )}
      </SettingsSection>
      <SmartFolderPackages workspaceId={workspaceId} />
    </div>
  );
}
