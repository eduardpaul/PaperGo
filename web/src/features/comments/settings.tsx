import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { commentsAvailable } from '@/api/auth';
import { api } from '@/api/http';
import { commentSettingsQuery, keys, organizationCommentSettingsQuery } from '@/api/queries';
import type { CommentMode, CommentSettings, OrganizationCommentSettings } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Alert } from '@/components/ui/feedback';
import { Select } from '@/components/ui/select';
import { SettingRow, SettingsSection } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';

export const modeLabels: Record<CommentMode, string> = {
  off: 'Off',
  collaborators: 'Collaborators: people who can read drafts',
  readers: 'Readers: everyone who can read the item',
};

const origins: Record<CommentSettings['effective']['from'], string> = {
  deployment: 'the deployment',
  organization: 'the organization',
  workspace: 'the workspace',
  collection: 'this list or library',
  default: 'the default',
};

/** A workspace's, list's or library's comment mode, which places below inherit. */
export function PlaceCommentSettings({
  resourceId,
  canManage,
  workspace,
}: {
  resourceId: string;
  canManage: boolean;
  workspace?: boolean;
}) {
  const queryClient = useQueryClient();
  const settings = useQuery({ ...commentSettingsQuery(resourceId), enabled: commentsAvailable() });
  const [mode, setMode] = useState<string | null>(null);
  const current = mode ?? settings.data?.mode ?? '';
  const save = useMutation({
    mutationFn: () =>
      api<CommentSettings>(`/v1/resources/${resourceId}/settings/comments`, {
        method: 'PUT',
        ifMatch: settings.data?.version ?? 0,
        body: { mode: current || null },
      }),
    onSuccess: async () => {
      setMode(null);
      toast.success('Comment settings saved');
      await queryClient.invalidateQueries({ queryKey: ['comment-settings'] });
      await queryClient.invalidateQueries({ queryKey: keys.comments });
    },
  });
  if (!commentsAvailable() || !settings.data) return null;
  const effective = settings.data.effective;
  return (
    <SettingsSection
      title="Comments"
      description={
        workspace
          ? 'Who discusses items in this workspace. Lists and libraries can choose their own.'
          : 'Who discusses the items of this list or library.'
      }
      actions={
        canManage && (
          <Button
            variant="primary"
            disabled={save.isPending || current === (settings.data.mode ?? '')}
            onClick={() => save.mutate()}
          >
            Save
          </Button>
        )
      }
    >
      <SettingRow
        id={`comments-${resourceId}`}
        label="Discussion"
        hint={`Now: ${modeLabels[effective.mode]}, set by ${origins[effective.from]}.`}
      >
        <Select
          id={`comments-${resourceId}`}
          value={current}
          disabled={!canManage}
          onChange={(event) => setMode(event.target.value)}
        >
          <option value="">Inherit</option>
          {(Object.keys(modeLabels) as CommentMode[]).map((m) => (
            <option key={m} value={m}>
              {modeLabels[m]}
            </option>
          ))}
        </Select>
      </SettingRow>
      {save.error && <Alert>{problemMessage(save.error)}</Alert>}
    </SettingsSection>
  );
}

/** The organization's switch for comments and its default mode. */
export function OrganizationCommentSettingsForm({ canEdit }: { canEdit: boolean }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ ...organizationCommentSettingsQuery, enabled: commentsAvailable() });
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [mode, setMode] = useState<string | null>(null);
  const currentEnabled = enabled ?? settings.data?.enabled ?? true;
  const currentMode = mode ?? settings.data?.mode ?? '';
  const save = useMutation({
    mutationFn: () =>
      api<OrganizationCommentSettings>('/v1/organization/settings/comments', {
        method: 'PUT',
        ifMatch: settings.data?.version ?? 0,
        body: { enabled: currentEnabled, mode: currentMode || null },
      }),
    onSuccess: async () => {
      setEnabled(null);
      setMode(null);
      toast.success('Comment settings saved');
      await queryClient.invalidateQueries({ queryKey: ['comment-settings'] });
      await queryClient.invalidateQueries({ queryKey: keys.comments });
    },
  });
  if (!commentsAvailable() || !settings.data) return null;
  const dirty = currentEnabled !== settings.data.enabled || currentMode !== (settings.data.mode ?? '');
  return (
    <SettingsSection
      title="Comments"
      description="Discussions on items across the organization. Turning them off here wins over every workspace and collection."
      actions={
        canEdit && (
          <Button variant="primary" disabled={save.isPending || !dirty} onClick={() => save.mutate()}>
            Save
          </Button>
        )
      }
    >
      <SettingRow id="comments-enabled" label="Comments" hint="Off hides every discussion and keeps its comments.">
        <Select
          id="comments-enabled"
          value={currentEnabled ? 'on' : 'off'}
          disabled={!canEdit}
          onChange={(event) => setEnabled(event.target.value === 'on')}
        >
          <option value="on">On</option>
          <option value="off">Off</option>
        </Select>
      </SettingRow>
      <SettingRow
        id="comments-mode"
        label="Default discussion"
        hint="Workspaces, lists and libraries that choose nothing use this."
      >
        <Select
          id="comments-mode"
          value={currentMode}
          disabled={!canEdit || !currentEnabled}
          onChange={(event) => setMode(event.target.value)}
        >
          <option value="">{modeLabels.collaborators} (default)</option>
          {(Object.keys(modeLabels) as CommentMode[]).map((m) => (
            <option key={m} value={m}>
              {modeLabels[m]}
            </option>
          ))}
        </Select>
      </SettingRow>
      {save.error && <Alert>{problemMessage(save.error)}</Alert>}
    </SettingsSection>
  );
}
