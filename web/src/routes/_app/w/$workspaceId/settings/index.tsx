import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { CurrencyPicker } from '@/features/fields/currency-picker';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { useHasScope } from '@/features/admin/common';
import { SmartFolderPackages } from '@/features/smart-folders/packages';
import { can } from '@/features/lists/access';
import { ConfirmDialog, SettingRow, SettingsSection } from '@/features/settings/section';
import { PlaceCommentSettings } from '@/features/comments/settings';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/settings/')({ component: GeneralPage });

function GeneralPage() {
  const { t } = useTranslation(['workspaces', 'common']);
  const { workspaceId } = Route.useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workspace = useQuery(resourceQuery(workspaceId));
  const canTransfer = useHasScope('organization.manage');
  const [name, setName] = useState<string | null>(null);
  const [description, setDescription] = useState<string | null>(null);
  const [currency, setCurrency] = useState<string | null>(null);
  const currentCurrency = currency ?? workspace.data?.default_currency ?? '';
  const [deleting, setDeleting] = useState(false);
  const currentName = name ?? workspace.data?.name ?? '';
  const currentDescription = description ?? workspace.data?.description ?? '';
  const save = useMutation({
    mutationFn: () => {
      const body: Record<string, string> = {};
      if (workspace.data && currentName.trim() !== workspace.data.name) body.name = currentName.trim();
      if (workspace.data && currentDescription !== (workspace.data.description ?? ''))
        body.description = currentDescription;
      if (currentCurrency !== workspace.data?.default_currency) body.default_currency = currentCurrency;
      return api<Resource>(`/v1/resources/${workspaceId}`, {
        method: 'PATCH',
        ifMatch: workspace.data?.version,
        body,
      }).then((result) => result.data);
    },
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.resource(workspaceId), updated);
      await queryClient.invalidateQueries({ queryKey: keys.workspaces });
      setCurrency(null);
      setName(null);
      setDescription(null);
      toast.success(t('general.saved'));
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
  const dirty = currentName.trim() !== workspace.data.name || currentDescription !== (workspace.data.description ?? '') || currentCurrency !== workspace.data.default_currency;
  return (
    <div className="grid gap-5">
      <SettingsSection
        title={t('settings.general')}
        actions={
          canManage && (
            <Button variant="primary" disabled={save.isPending || !dirty || !currentName.trim()} onClick={() => save.mutate()}>
              {t('common:actions.save')}
            </Button>
          )
        }
      >
        <fieldset disabled={!canManage} className="contents">
        <SettingRow id="ws-name" label={t('common:fields.name')}>
          <Input id="ws-name" value={currentName} onChange={(event) => setName(event.target.value)} />
        </SettingRow>
        <SettingRow id="ws-description" label={t('common:fields.description')}>
          <Textarea
            id="ws-description"
            value={currentDescription}
            onChange={(event) => setDescription(event.target.value)}
          />
        </SettingRow>
        <SettingRow id="workspace-currency" label={t('general.currency')} hint={t('general.currencyHint')}>
          <CurrencyPicker id="workspace-currency" labelledBy="workspace-currency-label" value={currentCurrency} onChange={setCurrency} placeholder={t('general.orgDefault')} disabled={!canManage} />
        </SettingRow>
        </fieldset>
      </SettingsSection>
      {canManage && canTransfer && !workspace.data.personal_owner_id && (
        <SettingsSection title={t('general.copyTitle')} description={t('general.copyDescription')}>
          <Link to="/admin/maintenance" search={{ workspace: workspaceId }}>{t('general.copyLink')}</Link>
        </SettingsSection>
      )}
      <SmartFolderPackages workspaceId={workspaceId} />
      <PlaceCommentSettings resourceId={workspaceId} canManage={canManage} workspace />
      {canManage && !workspace.data.personal_owner_id && (
        <SettingsSection
          title={t('general.deleteTitle')}
          description={t('general.deleteDescription')}
          actions={
            <Button variant="danger" onClick={() => setDeleting(true)}>
              {t('general.deleteTitle')}
            </Button>
          }
        />
      )}
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={t('general.confirmTitle', { name: workspace.data.name })}
        description={t('general.confirmDescription')}
        confirm={t('general.deleteTitle')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </div>
  );
}
