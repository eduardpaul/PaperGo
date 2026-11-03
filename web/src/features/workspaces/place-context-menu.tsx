import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type ReactElement } from 'react';
import { toast } from 'sonner';
import { api } from '@/api/http';
import { keys, resourceQuery } from '@/api/queries';
import type { Resource } from '@/api/types';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu';
import { useHasScope } from '@/features/admin/common';
import { can } from '@/features/lists/access';
import { ConfirmDialog } from '@/features/settings/section';
import { useTranslation } from '@/i18n';
import { NewCollectionDialog } from './new-collection-dialog';

/** Workspace and collection shortcuts in navigation and workspace cards. */
export function PlaceContextMenu({ resource, children, onNavigate }: { resource: Resource; children: ReactElement; onNavigate?: () => void }) {
  const { t } = useTranslation(['workspaces', 'common']);
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const fresh = useQuery({ ...resourceQuery(resource.id), enabled: open });
  const current = fresh.data ?? resource;
  const manageable = can(fresh.data, 'manage');
  const writable = can(fresh.data, 'write');
  const workspace = resource.kind === 'workspace';
  const workspaceId = workspace ? resource.id : resource.workspace_id;
  const params = { workspaceId, listId: resource.id };
  const navigate = useNavigate();
  const client = useQueryClient();
  const canExport = useHasScope('organization.manage');
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/resources/${resource.id}`, { method: 'DELETE', ifMatch: current.version, query: { recursive: 'true' } }),
    onSuccess: async () => {
      setDeleting(false);
      toast.success(t('menu.collectionDeleted'));
      await client.invalidateQueries({ queryKey: keys.resources });
      void navigate({ to: '/w/$workspaceId', params: { workspaceId } });
      onNavigate?.();
    },
  });
  return <>
    <ContextMenu onOpenChange={setOpen}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={() => { void navigate({ to: workspace ? '/w/$workspaceId' : '/w/$workspaceId/l/$listId', params }); onNavigate?.(); }}>{t('common:actions.open')}</ContextMenuItem>
        {workspace && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/keywords', params }); onNavigate?.(); }}>{t('menu.keywords')}</ContextMenuItem>}
        {workspace && manageable && <>
          <ContextMenuItem onSelect={() => setCreating(true)}>{t('menu.newCollection')}</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/settings/members', params }); onNavigate?.(); }}>{t('menu.manageMembers')}</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/settings', params }); onNavigate?.(); }}>{t('menu.settings')}</ContextMenuItem>
          {canExport && <ContextMenuItem onSelect={() => { void navigate({ to: '/admin/maintenance', search: { workspace: workspaceId } }); onNavigate?.(); }}>{t('menu.exportPackage')}</ContextMenuItem>}
        </>}
        {!workspace && writable && <>
          {current.allow_folders !== false && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId', params, search: { create: 'folder' } }); onNavigate?.(); }}>{t('menu.newFolder')}</ContextMenuItem>}
          {current.kind === 'library' && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId', params, search: { create: 'upload' } }); onNavigate?.(); }}>{t('menu.upload')}</ContextMenuItem>}
        </>}
        {!workspace && manageable && <>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId/settings', params }); onNavigate?.(); }}>{t('menu.settings')}</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId/settings/permissions', params }); onNavigate?.(); }}>{t('menu.permissions')}</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem tone="danger" onSelect={() => setDeleting(true)}>{t('menu.delete')}</ContextMenuItem>
        </>}
      </ContextMenuContent>
    </ContextMenu>
    {workspace && <NewCollectionDialog key={String(creating)} workspaceId={workspaceId} open={creating} onOpenChange={setCreating} />}
    <ConfirmDialog open={deleting} onOpenChange={setDeleting} title={t('menu.deleteTitle', { name: current.name })} description={t('menu.deleteDescription')} confirm={t('common:actions.delete')} busy={remove.isPending} error={remove.error} onConfirm={() => remove.mutate()} />
  </>;
}
