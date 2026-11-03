import { useArchiveActions } from './archive-actions';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { ReactElement } from 'react';
import { useState } from 'react';
import { toast } from 'sonner';
import { problemMessage } from '@/lib/errors';
import { batch } from '@/api/batch';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource, Workflow, WorkflowAction } from '@/api/types';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger, ContextMenuTrigger } from '@/components/ui/context-menu';
import { downloadContent } from '@/features/documents/transfer';
import { editableFile, useOpenFile, viewable } from '@/features/files/file-action';
import { useOfficeLink } from '@/features/files/office';
import { ConfirmDialog } from '@/features/settings/section';
import { AddToSmartFolder } from '@/features/smart-folders/classification';
import { api } from '@/api/http';
import { LaunchWorkflow } from '@/features/workflows/workflow-tools';
import { useTranslation } from '@/i18n';
import { can } from './access';
import { MoveDialog } from './move-dialog';
import { RenameDialog } from './rename-dialog';

// Key names, as printed on keyboards.
const enterKey = 'Enter';

/** The same resource operations used by the item panel, available on rows and cards. */
export function ItemContextMenu({ item, collection, selection = [item], onSelect, onOpen, onDetails, children }: { item: Resource; collection: Resource; selection?: Resource[]; onSelect?: () => void; onOpen: (item: Resource) => void; onDetails: (item: Resource, tab?: string) => void; children: ReactElement }) {
  const { t } = useTranslation(['lists', 'common']);
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<'rename' | 'move' | 'classify' | 'delete'>();
  const [launching, setLaunching] = useState<Workflow>();
  const client = useQueryClient();
  const current = useQuery({ ...resourceQuery(item.id), enabled: open });
  const fresh = current.data ?? item;
  const writable = can(current.data, 'write');
  const editable = can(fresh, 'write') && editableFile(fresh);
  const manageable = can(current.data, 'manage');
  const bulk = selection.length > 1;
  const items = bulk ? selection.map(item => item.id === fresh.id ? fresh : item) : [fresh];
  const definitions = useQuery({ queryKey: ['workflow-actions', item.workspace_id, items.map(item => [item.id, item.version])], queryFn: async () => (await api<{value: WorkflowAction[]}>(`/v1/workspaces/${item.workspace_id}/workflow-actions`, { method: 'POST', body: {item_ids: items.map(item => item.id)} })).data.value, enabled: open && writable && items.every(item => item.kind === 'item') });
  const choices = definitions.data ?? [];
  const archive = useArchiveActions();
  const openFile = useOpenFile();
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () => batch(items.map(item => ({ id: item.id, method: 'DELETE', url: `/v1/resources/${item.id}${item.kind === 'folder' ? '?recursive=true' : ''}`, headers: { 'If-Match': `"${item.version}"` } }))),
    onError: error => toast.error(problemMessage(error)),
    onSuccess: () => { setDialog(undefined); toast.success(t('menu.deleted')); },
    onSettled: () => client.invalidateQueries({ queryKey: keys.resources }),
  });
  return <>
    <ContextMenu onOpenChange={value => { setOpen(value); if (value) onSelect?.(); }}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        {!bulk && item.kind === 'folder' && <ContextMenuItem onSelect={() => onOpen(fresh)}>{t('common:actions.open')}</ContextMenuItem>}
        {!bulk && viewable(fresh) && <ContextMenuItem onSelect={() => openFile(fresh)}>{t('menu.openFile')}<ContextMenuShortcut>O</ContextMenuShortcut></ContextMenuItem>}
        {!bulk && editable && <ContextMenuItem onSelect={() => openFile(fresh, { edit: true })}>{t('menu.editFile')}</ContextMenuItem>}
        {!bulk && fresh.file && <OfficeMenuItem item={fresh} collection={collection} />}
        {!bulk && fresh.file && <ContextMenuItem onSelect={() => void downloadContent(fresh.id, fresh.file?.filename ?? fresh.name)}>{t('common:actions.download')}<ContextMenuShortcut>D</ContextMenuShortcut></ContextMenuItem>}
        {!bulk && <ContextMenuItem onSelect={() => onDetails(fresh)}>{t('menu.details')}<ContextMenuShortcut>{enterKey}</ContextMenuShortcut></ContextMenuItem>}
        {writable && <>
          {items.every(item => item.kind === 'item') && <>
            {items.some(item => !item.archived) && <ContextMenuItem disabled={archive.isPending || items.length > 100} onSelect={() => archive.mutate({ items, archived: true })}>{bulk ? t('menu.archiveSelected') : t('common:actions.archive')}</ContextMenuItem>}
            {items.some(item => item.archived) && <ContextMenuItem disabled={archive.isPending || items.length > 100} onSelect={() => archive.mutate({ items, archived: false })}>{bulk ? t('menu.unarchiveSelected') : t('common:actions.unarchive')}</ContextMenuItem>}
          </>}
          {!bulk && <ContextMenuItem onSelect={() => setDialog('rename')}>{t('menu.rename')}</ContextMenuItem>}
          <ContextMenuItem disabled={items.length > 100} onSelect={() => setDialog('move')}>{bulk ? t('menu.moveSelected') : t('menu.move')}</ContextMenuItem>
          {items.every(item => item.kind === 'item') && <ContextMenuSub>
            <ContextMenuSubTrigger>{bulk ? t('menu.runWorkflowItems', { count: items.length }) : t('menu.runWorkflow')} ›</ContextMenuSubTrigger>
            <ContextMenuSubContent>
              {choices.map(action => <ContextMenuItem key={action.workflow?.id ?? action.builtin?.key} disabled={!action.availability.can_run} onSelect={() => action.workflow && setLaunching(action.workflow)}>{action.workflow?.name ?? action.builtin?.name}{!action.availability.can_run && ` — ${action.availability.reason}`}</ContextMenuItem>)}
              {!choices.length && <ContextMenuItem disabled>{definitions.isPending ? t('menu.loadingWorkflows') : t('menu.noWorkflows')}</ContextMenuItem>}
            </ContextMenuSubContent>
          </ContextMenuSub>}
          {!bulk && item.kind === 'item' && <ContextMenuItem onSelect={() => setDialog('classify')}>{t('menu.classify')}</ContextMenuItem>}
        </>}
        {!bulk && manageable && <ContextMenuItem onSelect={() => onDetails(fresh, 'access')}>{t('menu.permissions')}</ContextMenuItem>}
        {writable && <><ContextMenuSeparator /><ContextMenuItem tone="danger" onSelect={() => setDialog('delete')}>{bulk ? t('menu.deleteSelected') : t('menu.delete')}</ContextMenuItem></>}
        {current.isError && <ContextMenuItem disabled>{t('menu.unavailable')}</ContextMenuItem>}
      </ContextMenuContent>
    </ContextMenu>
    {dialog === 'rename' && <RenameDialog item={fresh} onClose={() => setDialog(undefined)} />}
    {dialog === 'move' && <MoveDialog item={fresh} items={bulk ? items : undefined} collection={collection} onClose={() => setDialog(undefined)} onMoved={() => setDialog(undefined)} />}
    {dialog === 'classify' && <AddToSmartFolder item={fresh} onClose={() => setDialog(undefined)} />}
    {launching && <LaunchWorkflow workflow={launching} itemIds={items.map(item => item.id)} fixedSelection={items} onClose={() => setLaunching(undefined)} />}
    <ConfirmDialog open={dialog === 'delete'} onOpenChange={value => !value && setDialog(undefined)} title={bulk ? t('menu.deleteTitleSelected', { count: items.length }) : t('menu.deleteTitle', { name: fresh.name })} description={t('menu.deleteDescription')} confirm={t('common:actions.delete')} busy={remove.isPending} error={remove.error} onConfirm={() => remove.mutate()} />
  </>;
}

/** Open in Word and the like, for Office files in libraries shared over WebDAV. */
function OfficeMenuItem({ item, collection }: { item: Resource; collection: Resource }) {
  const { t } = useTranslation('lists');
  const link = useOfficeLink(item, collection);
  if (!link) return null;
  return <ContextMenuItem onSelect={() => window.location.assign(link.href)}>{t('menu.openIn', { app: link.app.name })}</ContextMenuItem>;
}
