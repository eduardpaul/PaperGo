import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronRight, Folder, FolderOpen } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { api, listOf } from '@/api/http';
import { collectionChildren, keys } from '@/api/queries';
import type { Page, Resource } from '@/api/types';
import { FormDialog } from '@/components/form-dialog';
import { Alert, Skeleton } from '@/components/ui/feedback';
import { Label } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { problemMessage } from '@/lib/errors';

/** Moves an item to another folder, list or library of its workspace, browsing folders instead of typing ids. */
export function MoveDialog({
  item,
  collection,
  onClose,
  onMoved,
}: {
  item: Resource;
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
  const folders = useQuery({
    queryKey: ['resources', parentId, 'folders'],
    queryFn: async () =>
      listOf((await api<Page<Resource>>(`/v1/resources/${parentId}/children`, { query: { $top: 200 } })).data).filter(
        (child) => child.kind === 'folder',
      ),
  });
  const unchanged = parentId === (item.parent_id ?? collection.id);
  const move = useMutation({
    meta: { silent: true },
    mutationFn: async () =>
      (
        await api<Resource>(`/v1/items/${item.id}/move`, {
          method: 'POST',
          ifMatch: item.version,
          body: { collection_id: targetId, parent_id: path.at(-1)?.id },
        })
      ).data,
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
      title={`Move “${item.name}”`}
      submitLabel="Move here"
      pending={move.isPending}
      submitDisabled={unchanged}
      onClose={onClose}
      onSubmit={() => move.mutate()}
    >
      {move.isError && <Alert>{problemMessage(move.error)}</Alert>}
      <Label htmlFor="move-target">{collection.kind === 'library' ? 'Library' : 'List'}</Label>
      <Select
        id="move-target"
        value={targetId}
        onChange={(event) => {
          setTargetId(event.target.value);
          setPath([]);
        }}
      >
        {targets.data
          ?.filter((entry) => entry.kind === collection.kind)
          .map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.name}
              {entry.id === collection.id ? ' (current)' : ''}
            </option>
          ))}
      </Select>
      <p className="text-[13px] font-medium">Folder</p>
      <nav aria-label="Destination folder" className="flex flex-wrap items-center gap-1 text-[13px]">
        <button type="button" className="text-accent hover:underline" onClick={() => setPath([])}>
          {target?.name ?? collection.name}
        </button>
        {path.map((folder, index) => (
          <span key={folder.id} className="flex items-center gap-1">
            <ChevronRight className="size-3.5 text-muted" />
            <button type="button" className="text-accent hover:underline" onClick={() => setPath(path.slice(0, index + 1))}>
              {folder.name}
            </button>
          </span>
        ))}
      </nav>
      <div className="max-h-60 overflow-auto rounded-md border">
        {folders.isPending ? (
          <Skeleton className="m-2 h-8" />
        ) : folders.data?.length ? (
          <ul>
            {folders.data.map((folder) => (
              <li key={folder.id}>
                <button
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-surface-muted"
                  onClick={() => setPath([...path, folder])}
                >
                  <Folder className="size-4 text-muted" />
                  <span className="flex-1 truncate">{folder.name}</span>
                  <ChevronRight className="size-4 text-muted" />
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="flex items-center gap-2 px-3 py-2 text-[13px] text-muted">
            <FolderOpen className="size-4" /> No folders here
          </p>
        )}
      </div>
      <p className="text-xs text-muted">
        {unchanged
          ? 'The item is already here. Choose another folder or list.'
          : `The item moves into ${path.at(-1)?.name ?? target?.name ?? 'the list'} and takes on its permissions.`}
      </p>
    </FormDialog>
  );
}
