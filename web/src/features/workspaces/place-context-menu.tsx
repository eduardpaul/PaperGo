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
import { NewCollectionDialog } from './new-collection-dialog';

/** Workspace and collection shortcuts in navigation and workspace cards. */
export function PlaceContextMenu({ resource, children, onNavigate }: { resource: Resource; children: ReactElement; onNavigate?: () => void }) {
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const fresh = useQuery({ ...resourceQuery(resource.id), enabled: open });
  const current = fresh.data ?? resource;
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
      toast.success('Collection deleted');
      await client.invalidateQueries({ queryKey: keys.resources });
      void navigate({ to: '/w/$workspaceId', params: { workspaceId } });
      onNavigate?.();
    },
  });
  return <>
    <ContextMenu onOpenChange={setOpen}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={() => { void navigate({ to: workspace ? '/w/$workspaceId' : '/w/$workspaceId/l/$listId', params }); onNavigate?.(); }}>Open</ContextMenuItem>
        {workspace && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/keywords', params }); onNavigate?.(); }}>Keywords</ContextMenuItem>}
        {workspace && can(fresh.data, 'manage') && <>
          <ContextMenuItem onSelect={() => setCreating(true)}>New list or library…</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/settings/members', params }); onNavigate?.(); }}>Manage members</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/settings', params }); onNavigate?.(); }}>Settings</ContextMenuItem>
          {canExport && <ContextMenuItem onSelect={() => { void navigate({ to: '/admin/maintenance', search: { workspace: workspaceId } }); onNavigate?.(); }}>Export package…</ContextMenuItem>}
        </>}
        {!workspace && can(fresh.data, 'write') && <>
          {current.allow_folders !== false && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId', params, search: { create: 'folder' } }); onNavigate?.(); }}>New folder…</ContextMenuItem>}
          {current.kind === 'library' && <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId', params, search: { create: 'upload' } }); onNavigate?.(); }}>Upload…</ContextMenuItem>}
        </>}
        {!workspace && can(fresh.data, 'manage') && <>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId/settings', params }); onNavigate?.(); }}>Settings</ContextMenuItem>
          <ContextMenuItem onSelect={() => { void navigate({ to: '/w/$workspaceId/l/$listId/settings/permissions', params }); onNavigate?.(); }}>Permissions</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem tone="danger" onSelect={() => setDeleting(true)}>Delete…</ContextMenuItem>
        </>}
      </ContextMenuContent>
    </ContextMenu>
    {workspace && <NewCollectionDialog key={String(creating)} workspaceId={workspaceId} open={creating} onOpenChange={setCreating} />}
    <ConfirmDialog open={deleting} onOpenChange={setDeleting} title={`Delete “${current.name}”?`} description="The collection and all its contents move to the recycle bin." confirm="Delete" busy={remove.isPending} error={remove.error} onConfirm={() => remove.mutate()} />
  </>;
}
