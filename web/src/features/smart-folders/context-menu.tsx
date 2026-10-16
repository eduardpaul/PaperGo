import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type ReactElement } from 'react';
import { api } from '@/api/http';
import { keys, meQuery, resourceQuery, smartFolder } from '@/api/queries';
import type { SmartFolder } from '@/api/types';
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu';
import { can } from '@/features/lists/access';
import { ConfirmDialog } from '@/features/settings/section';

export function SmartFolderContextMenu({ folder, children, onNavigate }: { folder: SmartFolder; children: ReactElement; onNavigate?: () => void }) {
  const [open, setOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const fresh = useQuery({ queryKey: ['smart-folders', folder.id], queryFn: () => smartFolder(folder.id), enabled: open });
  const current = fresh.data ?? folder;
  const me = useQuery({ ...meQuery, enabled: open });
  const workspace = useQuery({ ...resourceQuery(current.workspace_id ?? ''), enabled: open && !!current.workspace_id && !current.owner_id });
  const manageable = me.data?.scopes?.includes('list.write') && (current.owner_id ? current.owner_id === me.data.subject : can(workspace.data, 'manage'));
  const navigate = useNavigate();
  const client = useQueryClient();
  const remove = useMutation({
    meta: { silent: true },
    mutationFn: () => api(`/v1/smart-folders/${folder.id}`, { method: 'DELETE', ifMatch: current.version }),
    onSuccess: async () => { setDeleting(false); await client.invalidateQueries({ queryKey: keys.smartFolders }); void navigate({ to: '/' }); onNavigate?.(); },
  });
  return <>
    <ContextMenu onOpenChange={setOpen}><ContextMenuTrigger asChild>{children}</ContextMenuTrigger><ContextMenuContent>
      <ContextMenuItem onSelect={() => { void navigate({ to: '/f/$folderId', params: { folderId: folder.id } }); onNavigate?.(); }}>Open</ContextMenuItem>
      {manageable && <><ContextMenuItem onSelect={() => { void navigate({ to: '/f/$folderId', params: { folderId: folder.id }, search: { edit: true } }); onNavigate?.(); }}>Edit rules…</ContextMenuItem><ContextMenuSeparator /><ContextMenuItem tone="danger" onSelect={() => setDeleting(true)}>Delete…</ContextMenuItem></>}
    </ContextMenuContent></ContextMenu>
    <ConfirmDialog open={deleting} onOpenChange={setDeleting} title={`Delete “${current.name}”?`} description="Removes the saved rules. Items stay in their collections." confirm="Delete smart folder" busy={remove.isPending} error={remove.error} onConfirm={() => remove.mutate()} />
  </>;
}
