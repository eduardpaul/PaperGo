import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { collectionChildren, keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Alert } from '@/components/ui/feedback';
import { Label } from '@/components/ui/input';
import { FolderBrowser } from './folder-browser';
import { Select } from '@/components/ui/select';
import { problemMessage } from '@/lib/errors';

/** Moves an item to another folder, list or library of its workspace, browsing folders instead of typing ids. */
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
  const targets = useQuery({
    queryKey: ['resources', item.workspace_id, 'collections'],
    queryFn: () => collectionChildren(item.workspace_id),
  });
  const [targetId, setTargetId] = useState(collection.id);
  const [path, setPath] = useState<Resource[]>([]);
  const parentId = path.at(-1)?.id ?? targetId;
  const moving = items ?? [item];
  const bulk = moving.length > 1;
  const changed = moving.filter(item => parentId !== (item.parent_id ?? collection.id));
  const unchanged = changed.length === 0;
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
      toast.success(`Moved to ${path.at(-1)?.name ?? targets.data?.find((target) => target.id === targetId)?.name}`);
      onMoved(moved);
    },
  });
  const target = targets.data?.find((entry) => entry.id === targetId);
  return (
    <FormDialog
      open
      title={bulk ? `Move ${moving.length} selected entries` : `Move “${item.name}”`}
      submitLabel="Move here"
      pending={move.isPending}
      submitDisabled={unchanged || moving.length > 100}
      onClose={onClose}
      onSubmit={() => move.mutate()}
    >
      {move.isError && <Alert>{problemMessage(move.error)}</Alert>}
      <Label htmlFor="move-target">{collection.kind === 'library' ? 'Library' : 'List'}</Label>
      <Select
        id="move-target"
        disabled={bulk}
        value={targetId}
        onChange={(event) => {
          setTargetId(event.target.value);
          setPath([]);
        }}
      >
        {targets.data
          ?.filter((entry) => entry.kind === collection.kind && (!bulk || entry.id === collection.id))
          .map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.name}
              {entry.id === collection.id ? ' (current)' : ''}
            </option>
          ))}
      </Select>
      <p className="text-[13px] font-medium">Folder</p>
      <FolderBrowser root={target ?? { ...collection, id: targetId }} path={path} onChange={setPath} excluded={moving.map(item => item.id)} />
      {bulk && <p className="text-xs text-muted">Move up to 100 entries between folders of this collection. The selection moves atomically.</p>}
      <p className="text-xs text-muted">
        {unchanged
          ? 'The item is already here. Choose another folder or list.'
          : `The item moves into ${path.at(-1)?.name ?? target?.name ?? 'the list'} and takes on its permissions.`}
      </p>
    </FormDialog>
  );
}
