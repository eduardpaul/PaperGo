import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type DragEvent, type HTMLAttributes } from 'react';
import { toast } from 'sonner';
import { api, isApiError } from '@/api/http';
import { keys } from '@/api/queries';
import type { Resource } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { ConfirmDialog } from '@/features/settings/section';
import { i18n, useTranslation } from '@/i18n';
import { problemMessage } from '@/lib/errors';
import { can } from './access';
import { MoveDialog } from './move-dialog';
import { RenameDialog } from './rename-dialog';

export const resourceDragType = 'application/x-papergo-resources';
type Move = { items: Resource[]; target: Resource };
type Conflict = Move & { source: Resource; existing?: { id: string; version: number } };

/** Drag payloads are untrusted; permissions and all versions are checked again by the server. */
export function readMoveDrag(value: string, collection: Resource): Resource[] {
  if (value.length > 128_000) throw new Error(i18n.t('lists:moves.tooLarge'));
  const payload = JSON.parse(value) as { collection_id?: unknown; items?: Resource[] };
  if (payload.collection_id !== collection.id || !Array.isArray(payload.items) || !payload.items.length || payload.items.length > 100) throw new Error(i18n.t('lists:moves.otherCollection'));
  const seen = new Set<string>();
  return payload.items.map(item => {
    if (!item || typeof item.id !== 'string' || !item.id || seen.has(item.id) || !Number.isSafeInteger(item.version) || item.version < 1 || !['item', 'folder'].includes(item.kind)) throw new Error(i18n.t('lists:moves.invalid'));
    seen.add(item.id);
    return { id: item.id, version: item.version, kind: item.kind, name: typeof item.name === 'string' ? item.name : i18n.t('lists:moves.unnamed'), parent_id: typeof item.parent_id === 'string' ? item.parent_id : undefined, container_id: collection.id, workspace_id: collection.workspace_id };
  });
}

/** Folder rows and breadcrumbs share one move operation and one conflict-resolution flow. */
export function useFolderMoves(collection: Resource | undefined, selected: Map<string, Resource>, onSelection: (items: Resource[]) => void) {
  const { t } = useTranslation(['lists', 'common']);
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
    onSuccess: (_result, input) => { onSelection([]); toast.success(t('moves.moved', { count: input.items.length, target: input.target.name })); },
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
      toast.error(t('moves.failed', { problem: problemMessage(error) }), { action: { label: t('moves.chooseDestination'), onClick: () => setReviewing(input.items) } });
    },
    onSettled: refresh,
  });
  const replace = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/resources/${collection!.id}/bulk`, { method: 'POST', body: { operations: [
      { action: 'delete', id: conflict!.existing!.id, version: conflict!.existing!.version },
      { action: 'update', id: conflict!.source.id, version: conflict!.source.version, update: { parent_id: conflict!.target.id } },
    ] } }),
    onSuccess: () => { setConfirmingReplace(false); setConflict(undefined); onSelection([]); toast.success(t('moves.replaced')); },
    onSettled: refresh,
  });
  const accepts = (event: DragEvent, target: Resource) => enabled && !move.isPending && (target.access ? can(target, 'write') : true) && Array.from(event.dataTransfer.types).includes(resourceDragType);
  const sourceProps = (item: Resource): HTMLAttributes<HTMLElement> => ({
    draggable: enabled && ['item', 'folder'].includes(item.kind),
    onDragStart: event => {
      if (!enabled) { event.preventDefault(); return; }
      const items = selected.has(item.id) ? [...selected.values()] : [item];
      if (items.length > 100) { event.preventDefault(); toast.error(t('moves.limit')); return; }
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
        if (items.some(item => item.id === target.id)) throw new Error(t('moves.intoItself'));
        if (items.every(item => item.parent_id === target.id)) { toast.info(t('moves.alreadyHere')); return; }
        replace.reset(); move.mutate({ items, target });
      } catch (error) { toast.error(problemMessage(error)); }
    },
  });
  const dialogs = <>
    <Dialog open={!!conflict && !renaming && !confirmingReplace} onOpenChange={open => !open && setConflict(undefined)}>
      <DialogContent><DialogHeader><DialogTitle>{t('moves.conflictTitle')}</DialogTitle><DialogDescription>{t('moves.conflictDescription', { name: conflict?.source.name, target: conflict?.target.name })}</DialogDescription></DialogHeader>
        <DialogFooter><Button onClick={() => setConflict(undefined)}>{t('common:actions.cancel')}</Button><Button disabled={!can(conflict?.source, 'write')} onClick={() => setRenaming(conflict?.source)}>{t('menu.rename')}</Button>{conflict?.existing && <Button variant="danger" onClick={() => setConfirmingReplace(true)}>{t('moves.replace')}</Button>}</DialogFooter>
      </DialogContent>
    </Dialog>
    {renaming && <RenameDialog item={renaming} onClose={() => { setRenaming(undefined); setConflict(undefined); }} />}
    <ConfirmDialog open={confirmingReplace} onOpenChange={setConfirmingReplace} title={t('moves.replaceTitle', { name: conflict?.source.name ?? '' })} description={t('moves.replaceDescription')} confirm={t('moves.replaceConfirm')} busy={replace.isPending} error={replace.error} onConfirm={() => replace.mutate()} />
    {reviewing && collection && <MoveDialog item={reviewing[0]} items={reviewing} collection={collection} onClose={() => setReviewing(undefined)} onMoved={() => setReviewing(undefined)} />}
  </>;
  return { over, sourceProps, targetProps, dialogs };
}
