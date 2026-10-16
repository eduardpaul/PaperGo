import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type DragEvent, type HTMLAttributes } from 'react';
import { toast } from 'sonner';
import { api, isApiError } from '@/api/http';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ConfirmDialog } from '@/features/settings/section';
import { problemMessage } from '@/lib/errors';
import { can } from './access';
import { MoveDialog } from './move-dialog';
import { RenameDialog } from './rename-dialog';

export const resourceDragType = 'application/x-papergo-resources';
type Move = { items: Resource[]; target: Resource };
type Conflict = Move & { source: Resource; existing?: { id: string; version: number } };

/** Drag payloads are untrusted; permissions and all versions are checked again by the server. */
export function readMoveDrag(value: string, collection: Resource): Resource[] {
  if (value.length > 128_000) throw new Error('The dragged selection is too large.');
  const payload = JSON.parse(value) as { collection_id?: unknown; items?: Resource[] };
  if (payload.collection_id !== collection.id || !Array.isArray(payload.items) || !payload.items.length || payload.items.length > 100) throw new Error('Select up to 100 entries from this collection. Use Move for another collection.');
  const seen = new Set<string>();
  return payload.items.map(item => {
    if (!item || typeof item.id !== 'string' || !item.id || seen.has(item.id) || !Number.isSafeInteger(item.version) || item.version < 1 || !['item', 'folder'].includes(item.kind)) throw new Error('The dragged selection is invalid. Select the entries again.');
    seen.add(item.id);
    return { id: item.id, version: item.version, kind: item.kind, name: typeof item.name === 'string' ? item.name : 'Item', parent_id: typeof item.parent_id === 'string' ? item.parent_id : undefined, container_id: collection.id, workspace_id: collection.workspace_id };
  });
}

/** Folder rows and breadcrumbs share one move operation and one conflict-resolution flow. */
export function useFolderMoves(collection: Resource | undefined, selected: Map<string, Resource>, onSelection: (items: Resource[]) => void) {
  const client = useQueryClient();
  const [over, setOver] = useState<string>();
  const [conflict, setConflict] = useState<Conflict>();
  const [renaming, setRenaming] = useState<Resource>();
  const [confirmingReplace, setConfirmingReplace] = useState(false);
  const [reviewing, setReviewing] = useState<Resource[]>();
  const enabled = can(collection, 'write');
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: keys.resources });
    await client.invalidateQueries({ queryKey: keys.smartFolders });
  };
  const move = useMutation({
    meta: { silent: true },
    mutationFn: ({ items, target }: Move) => api<{ id: string; version: number }[]>(`/v1/resources/${collection!.id}/move-selection`, { method: 'POST', body: { parent_id: target.id, items: items.map(item => ({ id: item.id, version: item.version })) } }),
    onSuccess: (_result, input) => { onSelection([]); toast.success(`Moved ${input.items.length === 1 ? 'entry' : `${input.items.length} entries`} to ${input.target.name}`); },
    onError: async (error, input) => {
      const sourceId = isApiError(error) && error.code === 'name_conflict' ? error.problem?.source_item_id : undefined;
      if (sourceId && input.items.some(item => item.id === sourceId)) {
        try {
          const source = (await api<Resource>(`/v1/resources/${sourceId}`)).data;
          const existing = isApiError(error) ? error.problem?.existing_item : undefined;
          setConflict({ ...input, source, existing: input.items.length === 1 && source.kind === 'item' ? existing : undefined });
          return;
        } catch { /* Access may have changed after the failed move. */ }
      }
      toast.error(`Nothing moved. ${problemMessage(error)}`, { action: { label: 'Choose destination…', onClick: () => setReviewing(input.items) } });
    },
    onSettled: refresh,
  });
  const replace = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/resources/${collection!.id}/bulk`, { method: 'POST', body: { operations: [
      { action: 'delete', id: conflict!.existing!.id, version: conflict!.existing!.version },
      { action: 'update', id: conflict!.source.id, version: conflict!.source.version, update: { parent_id: conflict!.target.id } },
    ] } }),
    onSuccess: () => { setConfirmingReplace(false); setConflict(undefined); onSelection([]); toast.success('Moved entry; the replaced file is in the recycle bin.'); },
    onSettled: refresh,
  });
  const accepts = (event: DragEvent, target: Resource) => enabled && !move.isPending && (target.access ? can(target, 'write') : true) && Array.from(event.dataTransfer.types).includes(resourceDragType);
  const sourceProps = (item: Resource): HTMLAttributes<HTMLElement> => ({
    draggable: enabled && ['item', 'folder'].includes(item.kind),
    onDragStart: event => {
      if (!enabled) { event.preventDefault(); return; }
      const items = selected.has(item.id) ? [...selected.values()] : [item];
      if (items.length > 100) { event.preventDefault(); toast.error('Select up to 100 entries to move at once.'); return; }
      onSelection(items);
      event.dataTransfer.setData(resourceDragType, JSON.stringify({ collection_id: collection!.id, items: items.map(({ id, version, kind, name, parent_id }) => ({ id, version, kind, name, parent_id })) }));
      event.dataTransfer.effectAllowed = 'move';
    },
    onDragEnd: () => setOver(undefined),
  });
  const targetProps = (target: Resource): HTMLAttributes<HTMLElement> => ({
    onDragOver: event => {
      if (!accepts(event, target) || selected.has(target.id)) return;
      event.preventDefault(); event.stopPropagation(); event.dataTransfer.dropEffect = 'move'; setOver(target.id);
    },
    onDragLeave: event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOver(undefined); },
    onDrop: event => {
      if (!accepts(event, target)) return;
      event.preventDefault(); event.stopPropagation(); setOver(undefined);
      try {
        const items = readMoveDrag(event.dataTransfer.getData(resourceDragType), collection!);
        if (items.some(item => item.id === target.id)) throw new Error('An entry cannot be moved into itself.');
        if (items.every(item => item.parent_id === target.id)) { toast.info('The selection is already here.'); return; }
        replace.reset(); move.mutate({ items, target });
      } catch (error) { toast.error(problemMessage(error)); }
    },
  });
  const dialogs = <>
    <Dialog open={!!conflict && !renaming && !confirmingReplace} onOpenChange={open => !open && setConflict(undefined)}>
      <DialogContent><DialogHeader><DialogTitle>Name already in use</DialogTitle><DialogDescription>“{conflict?.source.name}” conflicts with an entry in {conflict?.target.name}. Nothing moved. Rename it and drag again, or replace the writable file when available.</DialogDescription></DialogHeader>
        <DialogFooter><Button onClick={() => setConflict(undefined)}>Cancel</Button><Button disabled={!can(conflict?.source, 'write')} onClick={() => setRenaming(conflict?.source)}>Rename…</Button>{conflict?.existing && <Button variant="danger" onClick={() => setConfirmingReplace(true)}>Replace…</Button>}</DialogFooter>
      </DialogContent>
    </Dialog>
    {renaming && <RenameDialog item={renaming} onClose={() => { setRenaming(undefined); setConflict(undefined); }} />}
    <ConfirmDialog open={confirmingReplace} onOpenChange={setConfirmingReplace} title={`Replace the file named “${conflict?.source.name ?? ''}”?`} description="The destination file moves to the recycle bin and the dragged entry moves into its folder. Both changes happen together; a changed version or permission cancels the whole operation." confirm="Replace and move" busy={replace.isPending} error={replace.error} onConfirm={() => replace.mutate()} />
    {reviewing && collection && <MoveDialog item={reviewing[0]} items={reviewing} collection={collection} onClose={() => setReviewing(undefined)} onMoved={() => setReviewing(undefined)} />}
  </>;
  return { over, sourceProps, targetProps, dialogs };
}
