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
  const queryClient = useQueryClient();
  const library = collection.kind === 'library';
  const noun = library ? 'library' : 'list';
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
      toast.success('Settings saved');
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
      toast.success(`${collection.name} deleted`);
      onDeleted();
    },
  });

  return (
    <>
      <SettingsSection
        title="General"
        actions={
          canManage && (
            <>
              <Button disabled={!dirty || save.isPending} onClick={() => setDraft(initial)}>
                Discard changes
              </Button>
              <Button
                variant="primary"
                disabled={!dirty || save.isPending || !draft.name.trim() || (draft.versioning !== 'off' && !validMax)}
                onClick={() => save.mutate()}
              >
                Save
              </Button>
            </>
          )
        }
      >
        <fieldset disabled={!canManage} className="contents">
          <SettingRow id="collection-name" label="Name">
            <Input id="collection-name" required value={draft.name} onChange={(event) => patch({ name: event.target.value })} />
          </SettingRow>
          <SettingRow id="collection-description" label="Description">
            <Textarea
              id="collection-description"
              value={draft.description}
              onChange={(event) => patch({ description: event.target.value })}
            />
          </SettingRow>
          <SettingRow id="allow-folders" label="Folders" hint="People can organize content in folders.">
            <label className="flex items-center gap-2 pt-2 text-[13px]">
              <Checkbox
                id="allow-folders"
                checked={draft.allowFolders}
                onChange={(event) => patch({ allowFolders: event.target.checked })}
              />
              Allow folders
            </label>
          </SettingRow>
          <SettingRow
            id="versioning"
            label="Version history"
            hint={`Keep earlier versions of each ${library ? 'file and its details' : 'item'} so they can be restored.`}
          >
            <Select
              id="versioning"
              value={draft.versioning}
              onChange={(event) => patch({ versioning: event.target.value === 'major' ? 'major' : 'off' })}
            >
              <option value="off">Off</option>
              <option value="major">Keep versions</option>
            </Select>
          </SettingRow>
          {draft.versioning !== 'off' && (
            <SettingRow id="max-versions" label="Versions to keep" hint="Between 1 and 500; older versions are removed.">
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
            label="Publishing"
            hint="Changes stay drafts, visible to editors only, until someone publishes them."
          >
            <label className="flex items-center gap-2 pt-2 text-[13px]">
              <Checkbox id="publishing" checked={draft.publishing} onChange={(event) => patch({ publishing: event.target.checked })} />
              Require publishing before readers see changes
            </label>
          </SettingRow>
          {library && (
            <>
              <SettingRow id="duplicate-policy" label="Duplicate files" hint="What happens when someone uploads a file that is already stored.">
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
                  <option value="allow">Allow them</option>
                  <option value="warn">Allow them with a warning</option>
                  <option value="block">Refuse them</option>
                </Select>
              </SettingRow>
              <SettingRow id="webdav" label="Network drive" hint="Open this library in Windows Explorer, macOS Finder or Office over WebDAV.">
                <label className="flex items-center gap-2 pt-2 text-[13px]">
                  <Checkbox id="webdav" checked={draft.webdav} onChange={(event) => patch({ webdav: event.target.checked })} />
                  Available as a network drive
                </label>
              </SettingRow>
            </>
          )}
        </fieldset>
      </SettingsSection>
      {canManage && !collection.system_key && (
        <SettingsSection
          title={`Delete ${noun}`}
          description={`Deletes the ${noun} with all its folders and items for everyone.`}
          actions={
            <Button variant="danger" onClick={() => setDeleting(true)}>
              Delete {noun}
            </Button>
          }
        />
      )}
      <ConfirmDialog
        open={deleting}
        onOpenChange={setDeleting}
        title={`Delete ${collection.name}?`}
        description={`All folders and items in this ${noun} become unavailable to everyone.`}
        confirm={`Delete ${noun}`}
        busy={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
      />
    </>
  );
}
