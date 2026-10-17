import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { allPages } from '@/api/pages';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Alert } from '@/components/ui/feedback';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { problemMessage } from '@/lib/errors';
import { QueryFeedback } from '@/features/admin/common';
import { can } from './access';
import { FolderBrowser } from './folder-browser';

/** Moves content between workspaces and collections, browsing destination folders. */
export function MoveDialog({
  item,
  items,
  collection,
  onClose,
  onMoved,
}: {
  item: Resource;
  items?: Resource[];
  collection: Resource;
  onClose: () => void;
  onMoved: (moved: Resource) => void;
}) {
  const queryClient = useQueryClient();
  const moving = items ?? [item];
  const bulk = moving.length > 1;
  const crossCollection = !bulk && item.kind === 'item';
  const [workspaceId, setWorkspaceId] = useState(item.workspace_id);
  const workspaces = useQuery({
    queryKey: [...keys.workspaces, 'move-destinations'],
    queryFn: () => allPages<Resource>('/v1/workspaces'),
    enabled: crossCollection,
  });
  const targets = useQuery({
    queryKey: ['resources', workspaceId, 'move-destinations', collection.kind],
    queryFn: () => allPages<Resource>(`/v1/resources/${workspaceId}/children`, { kind: collection.kind }),
    enabled: crossCollection,
  });
  const [targetId, setTargetId] = useState(collection.id);
  const [path, setPath] = useState<Resource[]>([]);
  const parentId = path.at(-1)?.id ?? targetId;
  const target = targetId === collection.id ? collection : targets.data?.find((entry) => entry.id === targetId);
  const destination = useQuery({ ...resourceQuery(parentId), enabled: !!target });
  const changed = moving.filter(item => parentId !== (item.parent_id ?? collection.id));
  const unchanged = changed.length === 0;
  const submitDisabled = unchanged || moving.length > 100 || !target || !can(destination.data, 'write');
  const move = useMutation({
    meta: { silent: true },
    mutationFn: async () => {
      if (bulk) {
        const result = await api<{ id: string; version: number }[]>(`/v1/resources/${collection.id}/move-selection`, { method: 'POST', body: { parent_id: parentId, items: changed.map(item => ({ id: item.id, version: item.version })) } });
        return { ...item, parent_id: parentId, version: result.data.find(row => row.id === item.id)?.version ?? item.version };
      }
      return (
        await api<Resource>(`/v1/items/${item.id}/move`, {
          method: 'POST',
          ifMatch: item.version,
          body: { collection_id: targetId, parent_id: path.at(-1)?.id },
        })
      ).data;
    },
    onSuccess: async (moved) => {
      await queryClient.invalidateQueries({ queryKey: keys.resources });
      toast.success(`Moved to ${path.at(-1)?.name ?? target?.name}`);
      onClose();
      onMoved(moved);
    },
  });
  const options = crossCollection ? targets.data ?? [] : [collection];
  return (
    <FormDialog
      open
      title={bulk ? `Move ${moving.length} selected entries` : `Move “${item.name}”`}
      submitLabel="Move here"
      pending={move.isPending}
      submitDisabled={submitDisabled}
      onClose={onClose}
      onSubmit={() => { if (!submitDisabled) move.mutate(); }}
    >
      {move.isError && <Alert>{problemMessage(move.error)}</Alert>}
      <fieldset disabled={move.isPending} className="flex flex-col gap-3">
        {crossCollection && <>
          <Label htmlFor="move-workspace">Workspace</Label>
          <Select id="move-workspace" value={workspaceId} onChange={(event) => {
            setWorkspaceId(event.target.value);
            setTargetId('');
            setPath([]);
          }}>
            {!workspaces.data?.some(workspace => workspace.id === workspaceId) && <option value={workspaceId}>Current workspace</option>}
            {workspaces.data?.map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
          </Select>
          <QueryFeedback query={workspaces} />
        </>}
        <Label htmlFor="move-target">{collection.kind === 'library' ? 'Library' : 'List'}</Label>
        <Select
          id="move-target"
          disabled={!crossCollection || targets.isPending}
          value={targetId}
          onChange={(event) => {
            setTargetId(event.target.value);
            setPath([]);
          }}
        >
          <option value="">Choose a {collection.kind === 'library' ? 'library' : 'list'}</option>
          {targetId === collection.id && !options.some(entry => entry.id === collection.id) && <option value={collection.id}>{collection.name} (current)</option>}
          {options
            .filter((entry) => entry.kind === collection.kind)
            .map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.name}
                {entry.id === collection.id ? ' (current)' : ''}
              </option>
            ))}
        </Select>
        {crossCollection && <QueryFeedback query={targets} />}
        {crossCollection && targets.isSuccess && options.length === 0 && <p className="text-xs text-muted">No {collection.kind === 'library' ? 'libraries' : 'lists'} are available in this workspace.</p>}
        {target && <>
          <p className="text-[13px] font-medium">Folder</p>
          <FolderBrowser root={target} path={path} onChange={setPath} excluded={moving.map(item => item.id)} />
          <QueryFeedback query={destination} />
          {destination.isSuccess && !can(destination.data, 'write') && <p className="text-xs text-muted">You don’t have write access here. Choose another destination.</p>}
        </>}
      </fieldset>
      {bulk && <p className="text-xs text-muted">Move up to 100 entries between folders of this collection. The selection moves atomically.</p>}
      <p className="text-xs text-muted">
        {unchanged
          ? 'The item is already here. Choose another destination.'
          : `The item moves into ${path.at(-1)?.name ?? target?.name ?? 'the destination'} and takes on its permissions.`}
      </p>
    </FormDialog>
  );
}
