import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input, Textarea } from '@/components/ui/input';
import { Checkbox, Select } from '@/components/ui/select';
import { ConfirmDialog, SettingRow, SettingsSection } from '@/features/settings/section';
import { useTranslation } from '@/i18n';

/** Name, folders, history, publishing and file settings of a list or library, and deleting it. */
export function CollectionGeneral({
  collection,
  canManage,
  onDeleted,
}: {
  collection: Resource;
  canManage: boolean;
  onDeleted: () => void;
}) {
  const { t } = useTranslation(['lists', 'common']);
  const queryClient = useQueryClient();
  const library = collection.kind === 'library';
  const initial = {
    name: collection.name,
    description: collection.description ?? '',
    duplicate: collection.duplicate_policy ?? 'warn',
    publishing: !!collection.publishing_enabled,
    webdav: !!collection.webdav_enabled,
    versioning: collection.versioning ?? 'off',
    maxVersions: String(collection.max_versions ?? 50),
    allowFolders: collection.allow_folders !== false,
  };
  const [draft, setDraft] = useState(initial);
  const [deleting, setDeleting] = useState(false);
  const patch = (next: Partial<typeof initial>) => setDraft((current) => ({ ...current, ...next }));
  const maxVersions = Number(draft.maxVersions);
  const validMax = Number.isInteger(maxVersions) && maxVersions >= 1 && maxVersions <= 500;

  const body = () => {
    const changes: Record<string, unknown> = {};
    if (draft.name.trim() !== initial.name) changes.name = draft.name.trim();
    if (draft.description !== initial.description) changes.description = draft.description;
    if (library && draft.duplicate !== initial.duplicate) changes.duplicate_policy = draft.duplicate;
    if (draft.publishing !== initial.publishing) changes.publishing_enabled = draft.publishing;
    if (library && draft.webdav !== initial.webdav) changes.webdav_enabled = draft.webdav;
    if (draft.versioning !== initial.versioning) changes.versioning = draft.versioning;
    if (draft.versioning !== 'off' && draft.maxVersions !== initial.maxVersions && validMax)
      changes.max_versions = maxVersions;
    if (draft.allowFolders !== initial.allowFolders) changes.allow_folders = draft.allowFolders;
    return changes;
  };
  const dirty = Object.keys(body()).length > 0;

  const save = useMutation({
    mutationFn: async () =>
      (await api<Resource>(`/v1/resources/${collection.id}`, { method: 'PATCH', ifMatch: collection.version, body: body() }))
        .data,
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.resource(collection.id), updated);
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success(t('general.saved'));
    },
  });
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () =>
      api(`/v1/resources/${collection.id}`, {
        method: 'DELETE',
        ifMatch: collection.version,
        query: { recursive: 'true' },
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success(t('general.deleted', { name: collection.name }));
      onDeleted();
    },
  });

  return (
    <>
      <SettingsSection
        title={t('general.title')}
        actions={
          canManage && (
            <>
              <Button disabled={!dirty || save.isPending} onClick={() => setDraft(initial)}>
                {t('general.discard')}
              </Button>
              <Button
                variant="primary"
                disabled={!dirty || save.isPending || !draft.name.trim() || (draft.versioning !== 'off' && !validMax)}
                onClick={() => save.mutate()}
              >
                {t('common:actions.save')}
              </Button>
            </>
          )
        }
      >
        <fieldset disabled={!canManage} className="contents">
          <SettingRow id="collection-name" label={t('common:fields.name')}>
            <Input id="collection-name" required value={draft.name} onChange={(event) => patch({ name: event.target.value })} />
          </SettingRow>
          <SettingRow id="collection-description" label={t('common:fields.description')}>
            <Textarea
              id="collection-description"
              value={draft.description}
              onChange={(event) => patch({ description: event.target.value })}
            />
          </SettingRow>
          <SettingRow id="allow-folders" label={t('general.folders')} hint={t('general.foldersHint')}>
            <label className="flex items-center gap-2 pt-2 text-[13px]">
              <Checkbox
                id="allow-folders"
                checked={draft.allowFolders}
                onChange={(event) => patch({ allowFolders: event.target.checked })}
              />
              {t('general.allowFolders')}
            </label>
          </SettingRow>
          <SettingRow
            id="versioning"
            label={t('general.versioning')}
            hint={library ? t('general.versioningHintLibrary') : t('general.versioningHintList')}
          >
            <Select
              id="versioning"
              value={draft.versioning}
              onChange={(event) => patch({ versioning: event.target.value === 'major' ? 'major' : 'off' })}
            >
              <option value="off">{t('general.versioningOff')}</option>
              <option value="major">{t('general.versioningOn')}</option>
            </Select>
          </SettingRow>
          {draft.versioning !== 'off' && (
            <SettingRow id="max-versions" label={t('general.maxVersions')} hint={t('general.maxVersionsHint')}>
              <Input
                id="max-versions"
                type="number"
                min={1}
                max={500}
                aria-invalid={!validMax}
                value={draft.maxVersions}
                onChange={(event) => patch({ maxVersions: event.target.value })}
              />
            </SettingRow>
          )}
          <SettingRow
            id="publishing"
            label={t('general.publishing')}
            hint={t('general.publishingHint')}
          >
            <label className="flex items-center gap-2 pt-2 text-[13px]">
              <Checkbox id="publishing" checked={draft.publishing} onChange={(event) => patch({ publishing: event.target.checked })} />
              {t('general.requirePublishing')}
            </label>
          </SettingRow>
          {library && (
            <>
              <SettingRow id="duplicate-policy" label={t('general.duplicates')} hint={t('general.duplicatesHint')}>
                <Select
                  id="duplicate-policy"
                  value={draft.duplicate}
                  onChange={(event) =>
                    patch({
                      duplicate:
                        event.target.value === 'allow' || event.target.value === 'block' ? event.target.value : 'warn',
                    })
                  }
                >
                  <option value="allow">{t('general.duplicateAllow')}</option>
                  <option value="warn">{t('general.duplicateWarn')}</option>
                  <option value="block">{t('general.duplicateBlock')}</option>
                </Select>
              </SettingRow>
              <SettingRow id="webdav" label={t('general.webdav')} hint={t('general.webdavHint')}>
                <label className="flex items-center gap-2 pt-2 text-[13px]">
                  <Checkbox id="webdav" checked={draft.webdav} onChange={(event) => patch({ webdav: event.target.checked })} />
                  {t('general.webdavEnabled')}
                </label>
              </SettingRow>
            </>
          )}
        </fieldset>
      </SettingsSection>
      {canManage && !collection.system_key && (
        <SettingsSection
          title={library ? t('general.deleteLibrary') : t('general.deleteList')}
          description={library ? t('general.deleteLibraryDescription') : t('general.deleteListDescription')}
          actions={
            <Button variant="danger" onClick={() => setDeleting(true)}>
              {library ? t('general.deleteLibrary') : t('general.deleteList')}
            </Button>
          }
        />
      )}
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={t('general.deleteTitle', { name: collection.name })}
        description={library ? t('general.deleteLibraryConfirm') : t('general.deleteListConfirm')}
        confirm={library ? t('general.deleteLibrary') : t('general.deleteList')}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </>
  );
}
