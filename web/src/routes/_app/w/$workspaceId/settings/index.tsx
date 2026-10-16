import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { useHasScope } from '@/features/admin/common';
import { SmartFolderPackages } from '@/features/smart-folders/packages';
import { can } from '@/features/lists/access';
import { ConfirmDialog, SettingRow, SettingsSection } from '@/features/settings/section';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/')({ component: GeneralPage });

function GeneralPage() {
  const { workspaceId } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const canTransfer = useHasScope('organization.manage');
  const [name, setName] = useState<string | null>(null);
  const [description, setDescription] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
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
    meta: { silent: true },
    mutationFn: () => api(`/v1/resources/${workspaceId}`, { method: 'DELETE', ifMatch: workspace.data?.version }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.workspaces });
      void navigate({ to: '/w' });
    },
  });
  if (!workspace.data) return null;
  const canManage = can(workspace.data, 'manage');
  const dirty = currentName.trim() !== workspace.data.name || currentDescription !== (workspace.data.description ?? '');
  return (
    <div className="grid gap-5">
      <SettingsSection
        title="General"
        actions={
          canManage && (
            <Button variant="primary" disabled={save.isPending || !dirty || !currentName.trim()} onClick={() => save.mutate()}>
              Save
            </Button>
          )
        }
      >
        <fieldset disabled={!canManage} className="contents">
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
        </fieldset>
      </SettingsSection>
      {canManage && canTransfer && !workspace.data.personal_owner_id && (
        <SettingsSection title="Copy workspace" description="Export this workspace's configuration, with optional items and files, and import it into another PaperGo instance.">
          <Link to="/admin/maintenance" search={{ workspace: workspaceId }}>Export this workspace</Link>
        </SettingsSection>
      )}
      <SmartFolderPackages workspaceId={workspaceId} />
      {canManage && !workspace.data.personal_owner_id && (
        <SettingsSection
          title="Delete workspace"
          description="Deletes the workspace with all its lists, libraries, folders, items and workflows."
          actions={
            <Button variant="danger" onClick={() => setDeleting(true)}>
              Delete workspace
            </Button>
          }
        />
      )}
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={`Delete ${workspace.data.name}?`}
        description="Everything in this workspace becomes unavailable to everyone, including its lists, libraries and workflows. This cannot be undone here."
        confirm="Delete workspace"
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </div>
  );
}
