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
import { useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';

export const modes: CommentMode[] = ['off', 'collaborators', 'readers'];

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
  const { t } = useTranslation(['comments', 'common']);
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
      toast.success(t('settings.saved'));
      await queryClient.invalidateQueries({ queryKey: ['comment-settings'] });
      await queryClient.invalidateQueries({ queryKey: keys.comments });
    },
  });
  if (!commentsAvailable() || !settings.data) return null;
  const effective = settings.data.effective;
  return (
    <SettingsSection
      title={t('settings.title')}
      description={workspace ? t('settings.workspaceDescription') : t('settings.collectionDescription')}
      actions={
        canManage && (
          <Button
            variant="primary"
            disabled={save.isPending || current === (settings.data.mode ?? '')}
            onClick={() => save.mutate()}
          >
            {t('common:actions.save')}
          </Button>
        )
      }
    >
      <SettingRow
        id={`comments-${resourceId}`}
        label={t('settings.discussion')}
        hint={t('settings.now', { mode: t(`settings.modes.${effective.mode}`), origin: t(`settings.setBy.${effective.from}`) })}
      >
        <Select
          id={`comments-${resourceId}`}
          value={current}
          disabled={!canManage}
          onChange={(event) => setMode(event.target.value)}
        >
          <option value="">{t('settings.inherit')}</option>
          {modes.map((m) => (
            <option key={m} value={m}>
              {t(`settings.modes.${m}`)}
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
  const { t } = useTranslation(['comments', 'common']);
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
      toast.success(t('settings.saved'));
      await queryClient.invalidateQueries({ queryKey: ['comment-settings'] });
      await queryClient.invalidateQueries({ queryKey: keys.comments });
    },
  });
  if (!commentsAvailable() || !settings.data) return null;
  const dirty = currentEnabled !== settings.data.enabled || currentMode !== (settings.data.mode ?? '');
  return (
    <SettingsSection
      title={t('settings.title')}
      description={t('settings.organizationDescription')}
      actions={
        canEdit && (
          <Button variant="primary" disabled={save.isPending || !dirty} onClick={() => save.mutate()}>
            {t('common:actions.save')}
          </Button>
        )
      }
    >
      <SettingRow id="comments-enabled" label={t('settings.enabled')} hint={t('settings.enabledHint')}>
        <Select
          id="comments-enabled"
          value={currentEnabled ? 'on' : 'off'}
          disabled={!canEdit}
          onChange={(event) => setEnabled(event.target.value === 'on')}
        >
          <option value="on">{t('settings.on')}</option>
          <option value="off">{t('settings.off')}</option>
        </Select>
      </SettingRow>
      <SettingRow
        id="comments-mode"
        label={t('settings.defaultMode')}
        hint={t('settings.defaultModeHint')}
      >
        <Select
          id="comments-mode"
          value={currentMode}
          disabled={!canEdit || !currentEnabled}
          onChange={(event) => setMode(event.target.value)}
        >
          <option value="">{t('settings.defaultOption', { mode: t('settings.modes.collaborators') })}</option>
          {modes.map((m) => (
            <option key={m} value={m}>
              {t(`settings.modes.${m}`)}
            </option>
          ))}
        </Select>
      </SettingRow>
      {save.error && <Alert>{problemMessage(save.error)}</Alert>}
    </SettingsSection>
  );
}
