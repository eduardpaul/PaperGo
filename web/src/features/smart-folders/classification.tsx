import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api } from '@/api/http';
import { allPages, useApiPages } from '@/api/pages';
import { contentTypesQuery, fieldsQuery, keys, resourceQuery, smartFoldersQuery } from '@/api/queries';
import type { Resource, SmartFolder } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Button } from '@/components/ui/button';
import { Combobox } from '@/components/ui/combobox';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { QueryFeedback } from '@/features/admin/common';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { FolderBrowser } from '@/features/lists/folder-browser';
import { fieldsForType } from '@/features/lists/schema';
import { useTranslation } from '@/i18n';

/** PaperDotNet's AddToFolderDialog with Go's folder and item concurrency checks. */
export function AddToSmartFolder({ item, onClose }: { item: Resource; onClose: () => void }) {
  const { t } = useTranslation('smartFolders');
  const client = useQueryClient();
  const folders = useQuery(smartFoldersQuery);
  const [id, setId] = useState('');
  const folder = folders.data?.find((folder) => folder.id === id);
  const add = useMutation({
    mutationFn: () =>
      api(`/v1/smart-folders/${id}/items`, {
        method: 'POST',
        body: {
          folder_version: folder!.version,
          collection_id: item.container_id,
          item_id: item.id,
          version: item.version,
        },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.smartFolders });
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  return (
    <FormDialog
      open
      title={t('classify.addTitle', { name: item.name })}
      description={t('classify.addDescription')}
      submitLabel={t('classify.classifyItem')}
      pending={add.isPending}
      submitDisabled={!folder}
      onClose={onClose}
      onSubmit={() => add.mutate()}
    >
      <QueryFeedback query={folders} />
      <Label htmlFor="classify-folder">{t('classify.smartFolder')}</Label>
      <Select id="classify-folder" required value={id} onChange={(event) => setId(event.target.value)}>
        <option value="">{t('classify.choose')}</option>
        {folders.data
          ?.filter((folder) => folder.definition.mode === 'classification' && (!folder.workspace_id || folder.workspace_id === item.workspace_id))
          .map((folder) => (
            <option key={folder.id} value={folder.id}>
              {folder.name}
            </option>
          ))}
      </Select>
    </FormDialog>
  );
}

export function ClassifyDialog({
  folder,
  path,
  onClose,
}: {
  folder: SmartFolder;
  path: (string | null)[];
  onClose: () => void;
}) {
  const { t } = useTranslation(['smartFolders', 'common']);
  const client = useQueryClient();
  const [chosenCollectionId, setCollectionId] = useState('');
  const [mode, setMode] = useState('existing');
  const [itemId, setItemId] = useState('');
  const [q, setQ] = useState('');
  const [name, setName] = useState('');
  const [typeId, setTypeId] = useState('');
  const [destinationPath, setDestinationPath] = useState<Resource[]>([]);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const collections = useQuery({
    queryKey: ['resources', folder.id, 'classification-collections'],
    queryFn: async () => {
      const workspaces = folder.workspace_id
        ? [{ id: folder.workspace_id }]
        : await allPages<Resource>('/v1/workspaces');
      return (
        await Promise.all(workspaces.map((workspace) => allPages<Resource>(`/v1/resources/${workspace.id}/children`)))
      )
        .flat()
        .filter(
          (resource) =>
            (resource.kind === 'list' || resource.kind === 'library') &&
            (!folder.definition.collections?.length || folder.definition.collections.some(name => name.toLowerCase() === resource.name.toLowerCase())) && (!folder.definition.collection_kind || resource.kind === folder.definition.collection_kind) && (!folder.definition.templates?.length || resource.template_keys?.some(key => folder.definition.templates!.some(template => template.toLowerCase() === key.toLowerCase()))),
        );
    },
  });
  const collectionId = chosenCollectionId || collections.data?.[0]?.id || '';
  const types = useQuery({ ...contentTypesQuery(collectionId), enabled: !!collectionId });
  const fields = useQuery({ ...fieldsQuery(collectionId), enabled: !!collectionId });
  const typeOptions = (types.data ?? []).filter(type => !folder.definition.content_types?.length || folder.definition.content_types.some(value => [type.key.toLowerCase(), type.name.toLowerCase()].includes(value.toLowerCase())));
  const visible = fieldsForType(fields.data ?? [], typeOptions, typeId);
  const items = useApiPages<Resource>(
    ['resources', collectionId, 'classify', q],
    `/v1/resources/${collectionId}/query`,
    { enabled: !!collectionId && mode === 'existing', body: { surface: 'head', query: { search: q || undefined } } },
  );
  const item = useQuery({ ...resourceQuery(itemId), enabled: !!itemId && mode === 'existing' });
  const add = useMutation({
    mutationFn: () =>
      api(`/v1/smart-folders/${folder.id}/items`, {
        method: 'POST',
        body: {
          folder_version: folder.version,
          collection_id: collectionId,
          path,
          item_id: mode === 'existing' ? itemId : undefined,
          version: mode === 'existing' ? item.data!.version : undefined,
          parent_id: mode === 'new' ? destinationPath.at(-1)?.id : undefined,
          create:
            mode === 'new'
              ? {
                  name: name.trim(),
                  content_type_id: typeId || undefined,
                  keywords: [],
                  values: valuesPatch(visible, {}, values) ?? {},
                }
              : undefined,
        },
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: keys.smartFolders });
      await client.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });
  const options = items.items
    .filter((item) => item.kind === 'item')
    .map((item) => ({ value: item.id, label: item.name }));
  return (
    <FormDialog
      open
      title={t('classify.toTitle', { name: folder.name })}
      description={t('classify.description')}
      submitLabel={mode === 'new' ? t('classify.createAndClassify') : t('classify.classifyItem')}
      pending={add.isPending}
      submitDisabled={!collectionId || (mode === 'existing' && !item.data)}
      onClose={onClose}
      onSubmit={() => add.mutate()}
    >
      <QueryFeedback query={collections} />
      <Label htmlFor="classify-collection">{t('classify.collection')}</Label>
      <Select
        id="classify-collection"
        required
        value={collectionId}
        onChange={(event) => {
          setCollectionId(event.target.value);
          setItemId('');
          setDestinationPath([]);
          setTypeId('');
          setValues({});
        }}
      >
        <option value="">{t('classify.choose')}</option>
        {collections.data?.map((collection) => (
          <option key={collection.id} value={collection.id}>
            {collection.name}
          </option>
        ))}
      </Select>
      <Select aria-label={t('classify.mode')} value={mode} onChange={(event) => setMode(event.target.value)}>
        <option value="existing">{t('classify.existing')}</option>
        <option value="new">{t('classify.new')}</option>
      </Select>
      {mode === 'existing' ? (
        <>
          <Label id="classify-item-label">{t('classify.item')}</Label>
          <Combobox
            aria-labelledby="classify-item-label"
            options={options}
            selected={
              itemId
                ? [
                    options.find((option) => option.value === itemId) ?? {
                      value: itemId,
                      label: item.data?.name ?? itemId,
                    },
                  ]
                : []
            }
            onSearch={setQ}
            onChange={(values) => setItemId(values[0]?.value ?? '')}
            loading={items.isFetching}
          />
          <QueryFeedback query={items} />
          {items.hasNextPage && <Button onClick={() => void items.fetchNextPage()}>{t('classify.moreItems')}</Button>}
          {itemId && <QueryFeedback query={item} />}
        </>
      ) : (
        <>
          <Label htmlFor="classify-name">{t('common:fields.name')}</Label>
          <Input id="classify-name" required value={name} onChange={(event) => setName(event.target.value)} />
          <Label htmlFor="classify-type">{t('classify.contentType')}</Label>
          <Select id="classify-type" value={typeId} onChange={(event) => setTypeId(event.target.value)}>
            <option value="">{t('classify.default')}</option>
            {typeOptions.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name}
              </option>
            ))}
          </Select>
          {collections.data?.find(collection => collection.id === collectionId) && <><p className="text-sm font-medium">{t('classify.destination')}</p><FolderBrowser root={collections.data.find(collection => collection.id === collectionId)!} path={destinationPath} onChange={setDestinationPath} /></>}
          <p className="text-xs text-muted">{t('classify.emptyHint')}</p>
          {visible.map((field) => (
            <FieldEditor
              key={field.id}
              field={{ ...field, required: false }}
              value={values[field.key]}
              onChange={(value) => setValues((current) => ({ ...current, [field.key]: value }))}
            />
          ))}
        </>
      )}
    </FormDialog>
  );
}
