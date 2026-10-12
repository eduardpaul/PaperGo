import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { collectionChildren, keys, permissions, resourceQuery, revisions } from '@/api/queries';
import type { ContentType, FieldDefinition, PermissionGrant, Resource, Revision } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Input, Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { downloadContent, replaceContent } from '@/features/documents/transfer';
import { FieldEditor } from '@/features/fields/editor';
import { valuesPatch } from '@/features/fields/values';
import { useFormat } from '@/lib/preferences';
import { fieldsForType, publishState } from './schema';
import { AddToSmartFolder } from '@/features/smart-folders/classification';
import { RelatedItems } from './related-items';
import { ItemSchemaView, Publications } from './item-history';
import { ConfirmDialog } from '@/features/settings/section';

const actions = ['read', 'read_draft', 'write', 'publish', 'manage'] as const;

export function ItemSheet({
  itemId,
  collection,
  fields,
  contentTypes,
  tab,
  onTab,
  onClose,
  onMoved,
}: {
  itemId: string;
  collection: Resource;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  tab: string;
  onTab: (tab: string) => void;
  onClose: () => void;
  onMoved: (resource: Resource) => void;
}) {
  const itemQuery = useQuery(resourceQuery(itemId));
  const item = itemQuery.data;
  const library = collection.kind === 'library';
  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent>
        <header className="flex items-start gap-3 border-b px-4 py-3">
          <div className="min-w-0 flex-1">
            <SheetTitle className="truncate text-base font-semibold">{item?.name ?? 'Item'}</SheetTitle>
            <SheetDescription className="text-xs text-muted">{collection.name}</SheetDescription>
          </div>
          <SheetClose />
        </header>
        {item ? (
          <Tabs value={tab || 'details'} onValueChange={onTab} className="flex min-h-0 flex-1 flex-col">
            <TabsList className="overflow-x-auto">
              <TabsTrigger value="details">Details</TabsTrigger>
              {library && item.kind === 'item' && <TabsTrigger value="content">Content</TabsTrigger>}
              <TabsTrigger value="versions">Versions</TabsTrigger>
              <TabsTrigger value="links">Links</TabsTrigger>
              <TabsTrigger value="access">Access</TabsTrigger>
              <TabsTrigger value="publish">Publish</TabsTrigger>
              <TabsTrigger value="schema">Schema</TabsTrigger>
            </TabsList>
            <div className="min-h-0 flex-1 overflow-auto">
              <TabsContent value="details" className="p-4">
                <Details
                  key={`${item.id}:${item.version}`}
                  item={item}
                  collection={collection}
                  fields={fields}
                  contentTypes={contentTypes}
                  onMoved={onMoved}
                  onClose={onClose}
                />
              </TabsContent>
              {library && item.kind === 'item' && (
                <TabsContent value="content" className="p-4">
                  <Content item={item} />
                </TabsContent>
              )}
              <TabsContent value="versions" className="p-4">
                <Versions item={item} />
              </TabsContent>
              <TabsContent value="links" className="p-4">
                <RelatedItems item={item} />
              </TabsContent>
              <TabsContent value="access" className="p-4">
                <Access item={item} />
              </TabsContent>
              <TabsContent value="schema" className="p-4">
                <ItemSchemaView itemId={item.id} />
              </TabsContent>
              <TabsContent value="publish" className="p-4">
                <Publish item={item} collection={collection} />
              </TabsContent>
            </div>
          </Tabs>
        ) : (
          <p className="p-4 text-sm text-muted">{itemQuery.isPending ? 'Loading…' : 'This item is not available.'}</p>
        )}
      </SheetContent>
    </Sheet>
  );
}

function Details({
  item,
  collection,
  fields,
  contentTypes,
  onMoved,
  onClose,
}: {
  item: Resource;
  collection: Resource;
  fields: FieldDefinition[];
  contentTypes: ContentType[];
  onMoved: (resource: Resource) => void;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [classifying, setClassifying] = useState(false);
  const [name, setName] = useState(item.name);
  const [tags, setTags] = useState((item.tags ?? []).join(', '));
  const [contentTypeId, setContentTypeId] = useState(item.content_type_id ?? '');
  const visible = fieldsForType(fields, contentTypes, contentTypeId || item.content_type_id);
  const [values, setValues] = useState<Record<string, unknown>>({ ...(item.values ?? {}) });
  const [collectionId, setCollectionId] = useState(item.container_id ?? collection.id);
  const [parentId, setParentId] = useState('');
  const lists = useQuery({
    queryKey: ['resources', item.workspace_id, 'collections'],
    queryFn: () => collectionChildren(item.workspace_id),
  });

  const save = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {};
      if (name.trim() !== item.name) body.name = name.trim();
      const nextTags = tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean);
      if (nextTags.join('\n') !== (item.tags ?? []).join('\n')) body.tags = nextTags;
      if (contentTypeId && contentTypeId !== (item.content_type_id ?? '')) body.content_type_id = contentTypeId;
      const patch = valuesPatch(visible, item.values, values);
      if (patch) body.values = patch;
      if (Object.keys(body).length === 0) return item;
      return (await api<Resource>(`/v1/resources/${item.id}`, { method: 'PATCH', ifMatch: item.version, body })).data;
    },
    onSuccess: async (updated) => {
      queryClient.setQueryData(keys.resource(item.id), updated);
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success('Saved');
    },
  });

  const move = useMutation({
    mutationFn: () =>
      api<Resource>(`/v1/items/${item.id}/move`, {
        method: 'POST',
        ifMatch: item.version,
        body: { collection_id: collectionId, parent_id: parentId.trim() || undefined },
      }).then((result) => result.data),
    onSuccess: async (moved) => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      onMoved(moved);
    },
  });

  const remove = useMutation({
    mutationFn: () =>
      api(`/v1/resources/${item.id}`, {
        method: 'DELETE',
        ifMatch: item.version,
        query: item.kind === 'folder' ? { recursive: 'true' } : undefined,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      onClose();
    },
  });

  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <Label htmlFor="item-name">Name</Label>
      <Input id="item-name" value={name} onChange={(event) => setName(event.target.value)} required />
      <Label htmlFor="item-tags">Tags</Label>
      <Input
        id="item-tags"
        value={tags}
        onChange={(event) => setTags(event.target.value)}
        placeholder="Comma separated"
      />
      {contentTypes.length > 0 && item.kind === 'item' && (
        <>
          <Label htmlFor="item-type">Content type</Label>
          <Select id="item-type" value={contentTypeId} onChange={(event) => setContentTypeId(event.target.value)}>
            <option value="">Default</option>
            {contentTypes.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name}
              </option>
            ))}
          </Select>
        </>
      )}
      {visible.map((field) => (
        <FieldEditor
          key={field.id}
          field={field}
          value={values[field.key]}
          onChange={(value) => setValues((current) => ({ ...current, [field.key]: value }))}
        />
      ))}
      {item.kind === 'item' && (
        <>
          <Button onClick={() => setClassifying(true)}>Add to smart folder</Button>
          {classifying && <AddToSmartFolder item={item} onClose={() => setClassifying(false)} />}
        </>
      )}
      <Button type="submit" variant="primary" disabled={save.isPending}>
        Save
      </Button>
      {item.kind === 'item' && (
        <div className="mt-2 flex flex-col gap-2 border-t pt-3">
          <p className="text-sm font-medium">Move</p>
          <Select value={collectionId} onChange={(event) => setCollectionId(event.target.value)}>
            {lists.data?.map((list) => (
              <option key={list.id} value={list.id}>
                {list.name}
              </option>
            ))}
          </Select>
          <Input
            value={parentId}
            onChange={(event) => setParentId(event.target.value)}
            placeholder="Folder id, or empty for the root"
          />
          <Button type="button" disabled={move.isPending || !collectionId} onClick={() => move.mutate()}>
            Move
          </Button>
        </div>
      )}
      <Button type="button" variant="danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
        {item.kind === 'folder' ? 'Delete folder and its contents' : 'Delete'}
      </Button>
    </form>
  );
}

function Content({ item }: { item: Resource }) {
  const queryClient = useQueryClient();
  const [fileVersion, setFileVersion] = useState('');
  const [restoring, setRestoring] = useState<number>();
  const restore = useMutation({
    mutationFn: (number: number) =>
      api(`/v1/items/${item.id}/content/versions/${number}/restore`, { method: 'POST', ifMatch: item.version }),
    onSuccess: async () => {
      setRestoring(undefined);
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      await queryClient.invalidateQueries({ queryKey: ['revisions', item.id] });
      toast.success('File restored as a new version');
    },
  });
  const replace = useMutation({
    mutationFn: (file: File) => replaceContent(item.id, item.version, file),
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: keys.resource(item.id) });
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      const duplicates = result.data.duplicates ?? [];
      if (duplicates.length)
        toast.warning(`Possible duplicates: ${duplicates.map((duplicate) => duplicate.name).join(', ')}`);
      else toast.success('File replaced');
    },
  });
  return (
    <div className="flex flex-col gap-3">
      <Button type="button" variant="primary" onClick={() => void downloadContent(item.id, item.name)}>
        Download
      </Button>
      <Label htmlFor="replace-file">Replace file</Label>
      <Input
        id="replace-file"
        type="file"
        disabled={replace.isPending}
        onChange={(event) => {
          const file = event.target.files?.[0];
          if (file) replace.mutate(file);
          event.target.value = '';
        }}
      />
      <p className="text-xs text-muted">Replacing the file keeps the current metadata and adds a new file version.</p>
      <form
        className="grid gap-2 border-t pt-3"
        onSubmit={(event) => {
          event.preventDefault();
          setRestoring(Number(fileVersion));
        }}
      >
        <Label htmlFor="restore-file-version">Restore file version</Label>
        <Input
          id="restore-file-version"
          type="number"
          required
          min={1}
          step={1}
          value={fileVersion}
          onChange={(event) => setFileVersion(event.target.value)}
        />
        <p className="text-xs text-muted">
          Enter a known file version number. File versions are separate from metadata revisions; the API does not list
          retained file versions.
        </p>
        <Button type="submit" disabled={!Number.isSafeInteger(Number(fileVersion)) || Number(fileVersion) < 1}>
          Restore file
        </Button>
      </form>
      <ConfirmDialog
        open={restoring !== undefined}
        onOpenChange={(open) => !open && setRestoring(undefined)}
        title={`Restore file version ${restoring ?? ''}?`}
        description="This creates a new file version using the retained bytes. The current name, tags and fields are kept."
        confirm="Restore file bytes"
        busy={restore.isPending}
        error={restore.error}
        onConfirm={() => restoring !== undefined && restore.mutate(restoring)}
      />
    </div>
  );
}

function Versions({ item }: { item: Resource }) {
  const format = useFormat();
  const queryClient = useQueryClient();
  const history = useQuery({ queryKey: ['revisions', item.id], queryFn: () => revisions(item.id) });
  const restore = useMutation({
    mutationFn: (revision: Revision) =>
      api(`/v1/items/${item.id}/revisions/${revision.revision_number}/restore`, {
        method: 'POST',
        ifMatch: item.version,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resource(item.id) });
      await queryClient.invalidateQueries({ queryKey: ['revisions', item.id] });
      toast.success('Metadata restored');
    },
  });
  const rows = history.data?.value ?? [];
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted">
        Restore brings back the saved name, tags and fields. File bytes stay on the current version.
      </p>
      {rows.map((revision) => (
        <div key={revision.id} className="flex items-center gap-3 rounded-md border px-3 py-2">
          <div className="min-w-0 flex-1">
            <p className="truncate text-[13px] font-medium">
              {revision.revision_number}. {revision.name}
              {revision.is_current ? ' · current' : ''}
            </p>
            <p className="text-xs text-muted">
              {format.dateTime(revision.created_at)} · {revision.created_by}
            </p>
          </div>
          {!revision.is_current && (
            <Button type="button" size="sm" disabled={restore.isPending} onClick={() => restore.mutate(revision)}>
              Restore
            </Button>
          )}
        </div>
      ))}
      {!history.isPending && rows.length === 0 && <p className="text-sm text-muted">No retained history.</p>}
    </div>
  );
}

function Access({ item }: { item: Resource }) {
  const queryClient = useQueryClient();
  const current = useQuery({ queryKey: ['permissions', item.id], queryFn: () => permissions(item.id) });
  if (!current.data)
    return <p className="text-sm text-muted">{current.isPending ? 'Loading…' : 'Permissions are not available.'}</p>;
  return (
    <AccessForm
      key={`${item.version}:${current.data.inherit}`}
      item={item}
      inherit={current.data.inherit}
      grants={current.data.grants ?? []}
      effective={current.data.effective_grants ?? []}
      onSaved={() => queryClient.invalidateQueries({ queryKey: ['permissions', item.id] })}
    />
  );
}

function AccessForm({
  item,
  inherit,
  grants,
  effective,
  onSaved,
}: {
  item: Resource;
  inherit: boolean;
  grants: PermissionGrant[];
  effective: PermissionGrant[];
  onSaved: () => void;
}) {
  const [localInherit, setInherit] = useState(inherit);
  const [localGrants, setGrants] = useState<PermissionGrant[]>(grants.map((grant) => ({ ...grant, effect: 'allow' })));
  const [copyInherited, setCopy] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      api(`/v1/resources/${item.id}/permissions`, {
        method: 'PUT',
        ifMatch: item.version,
        body: {
          inherit: localInherit,
          copy_inherited: !localInherit && copyInherited,
          grants: localInherit ? [] : localGrants.filter((grant) => grant.principal_id.trim()),
        },
      }),
    onSuccess: async () => {
      toast.success('Access saved');
      onSaved();
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={localInherit} onChange={(event) => setInherit(event.target.checked)} />
        Inherit access from the parent
      </label>
      {localInherit ? (
        <ul className="flex flex-col gap-1 text-[13px] text-muted">
          {effective.map((grant, index) => (
            <li key={`${grant.principal_id}-${grant.action}-${index}`}>
              {grant.principal_type} {grant.principal_id} · {grant.action}
            </li>
          ))}
          {effective.length === 0 && <li>No effective grants.</li>}
        </ul>
      ) : (
        <>
          {inherit && (
            <label className="flex items-center gap-2 text-[13px]">
              <input type="checkbox" checked={copyInherited} onChange={(event) => setCopy(event.target.checked)} />
              Copy the inherited grants
            </label>
          )}
          {localGrants.map((grant, index) => (
            <div key={index} className="grid grid-cols-[6rem_1fr_7rem] gap-2">
              <Select
                value={grant.principal_type}
                onChange={(event) =>
                  updateGrant(setGrants, index, {
                    principal_type: event.target.value as PermissionGrant['principal_type'],
                  })
                }
              >
                <option value="user">User</option>
                <option value="group">Group</option>
              </Select>
              <Input
                value={grant.principal_id}
                placeholder="Subject or group id"
                onChange={(event) => updateGrant(setGrants, index, { principal_id: event.target.value })}
              />
              <Select
                value={grant.action}
                onChange={(event) => updateGrant(setGrants, index, { action: event.target.value })}
              >
                {actions.map((action) => (
                  <option key={action} value={action}>
                    {action}
                  </option>
                ))}
              </Select>
            </div>
          ))}
          <Button
            type="button"
            onClick={() =>
              setGrants((current) => [
                ...current,
                { principal_type: 'user', principal_id: '', action: 'read', effect: 'allow' },
              ])
            }
          >
            Add grant
          </Button>
        </>
      )}
      <Button type="submit" variant="primary" disabled={save.isPending}>
        Save access
      </Button>
    </form>
  );
}

function updateGrant(
  setGrants: (value: PermissionGrant[] | ((current: PermissionGrant[]) => PermissionGrant[])) => void,
  index: number,
  patch: Partial<PermissionGrant>,
) {
  setGrants((current) => current.map((grant, i) => (i === index ? { ...grant, ...patch, effect: 'allow' } : grant)));
}

function Publish({ item, collection }: { item: Resource; collection: Resource }) {
  const queryClient = useQueryClient();
  const state = publishState(item, !!collection.publishing_enabled);
  const act = useMutation({
    mutationFn: (action: 'publish' | 'unpublish') =>
      api(action === 'publish' ? `/v1/items/${item.id}/publications` : `/v1/items/${item.id}/unpublish`, {
        method: 'POST',
        ifMatch: item.version,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: keys.resource(item.id) });
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      await queryClient.invalidateQueries({ queryKey: ['publications', item.id] });
    },
  });
  if (!collection.publishing_enabled) {
    return (
      <div className="grid gap-3">
        <p className="text-sm text-muted">
          Publishing is off for this collection. Changes are visible as soon as they are saved.
        </p>
        <Publications itemId={item.id} />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">Status: {state}</p>
      <div className="flex gap-2">
        <Button
          type="button"
          variant="primary"
          disabled={act.isPending || state === 'Published'}
          onClick={() => act.mutate('publish')}
        >
          Publish
        </Button>
        <Button
          type="button"
          disabled={act.isPending || state === 'Unpublished'}
          onClick={() => act.mutate('unpublish')}
        >
          Unpublish
        </Button>
      </div>
      <Publications itemId={item.id} />
    </div>
  );
}
