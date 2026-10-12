import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import { Briefcase, ChevronRight, House, Inbox, Plus, Search } from 'lucide-react';
import { Collapsible } from 'radix-ui';
import { useState } from 'react';
import { collectionChildren, smartFoldersQuery, workspacesQuery } from '@/api/queries';
import { Logo } from '@/components/brand/logo';
import { Skeleton } from '@/components/ui/feedback';
import { CreateSmartFolderDialog } from '@/features/smart-folders/create-dialog';
import { cn } from '@/lib/utils';

const itemClass =
  'flex h-8 items-center gap-2.5 rounded-md px-2 text-[13px] text-foreground/80 hover:bg-surface-muted hover:text-foreground [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-muted';
const activeClass = 'bg-accent-soft! text-accent! font-medium [&_svg]:text-accent!';

const navigation = [
  { to: '/', label: 'Home', icon: House, exact: true },
  { to: '/inbox', label: 'Inbox', icon: Inbox, exact: false },
  { to: '/search', label: 'Search', icon: Search, exact: false },
] as const;

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Main" className="flex h-full flex-col border-r bg-sidebar">
      <div className="flex h-14 items-center px-4">
        <Link to="/" onClick={onNavigate}>
          <Logo />
        </Link>
      </div>
      <div className="flex-1 overflow-y-auto px-2 pb-4">
        <ul className="flex flex-col gap-0.5">
          {navigation.map((entry) => (
            <li key={entry.to}>
              <Link to={entry.to} onClick={onNavigate} className={itemClass} activeProps={{ className: activeClass }} activeOptions={{ exact: entry.exact }}>
                <entry.icon />
                <span className="flex-1">{entry.label}</span>
              </Link>
            </li>
          ))}
        </ul>
        <SmartFolders onNavigate={onNavigate} />
        <Workspaces onNavigate={onNavigate} />
      </div>
    </nav>
  );
}

function SmartFolders({ onNavigate }: { onNavigate?: () => void }) {
  const navigate = useNavigate();
  const { data } = useQuery(smartFoldersQuery);
  const [open, setOpen] = useState(true);
  const [creating, setCreating] = useState(false);
  return (
    <Collapsible.Root open={open} onOpenChange={setOpen} className="mt-5">
      <div className="flex items-center px-2 pb-1">
        <Collapsible.Trigger className="flex flex-1 items-center gap-1 text-xs font-medium text-muted hover:text-foreground">
          <ChevronRight className={cn('size-3 transition-transform', open && 'rotate-90')} />
          Smart folders
        </Collapsible.Trigger>
        <button type="button" aria-label="New smart folder" className="rounded p-0.5 text-muted hover:bg-surface-muted hover:text-foreground" onClick={() => setCreating(true)}>
          <Plus className="size-3.5" />
        </button>
      </div>
      <Collapsible.Content>
        <ul className="flex flex-col gap-0.5">
          {data?.map((folder) => (
            <li key={folder.id}>
              <Link to="/f/$folderId" params={{ folderId: folder.id }} onClick={onNavigate} className={itemClass} activeProps={{ className: activeClass }}>
                <span className="truncate">{folder.name}</span>
              </Link>
            </li>
          ))}
        </ul>
      </Collapsible.Content>
      <CreateSmartFolderDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(folder) => {
          setCreating(false);
          onNavigate?.();
          void navigate({ to: '/f/$folderId', params: { folderId: folder.id } });
        }}
      />
    </Collapsible.Root>
  );
}

function Workspaces({ onNavigate }: { onNavigate?: () => void }) {
  const { data, isPending } = useQuery(workspacesQuery);
  const { workspaceId: current } = useParams({ strict: false });
  const [open, setOpen] = useState(true);
  const workspaces = data?.filter((workspace) => !workspace.personal_owner_id) ?? [];

  return (
    <Collapsible.Root open={open} onOpenChange={setOpen} className="mt-5">
      <div className="flex items-center px-2 pb-1">
        <Collapsible.Trigger className="flex flex-1 items-center gap-1 text-xs font-medium text-muted hover:text-foreground">
          <ChevronRight className={cn('size-3 transition-transform', open && 'rotate-90')} />
          Workspaces
        </Collapsible.Trigger>
        <Link to="/w" search={{ create: true }} onClick={onNavigate} aria-label="New workspace" className="rounded p-0.5 text-muted hover:bg-surface-muted hover:text-foreground">
          <Plus className="size-3.5" />
        </Link>
      </div>
      <Collapsible.Content>
        <ul className="flex flex-col gap-0.5">
          {isPending &&
            [0, 1, 2].map((i) => (
              <li key={i} className="px-2 py-1.5">
                <Skeleton className="h-4 w-3/4" />
              </li>
            ))}
          {workspaces.map((workspace) => (
            <li key={workspace.id}>
              <Link
                to="/w/$workspaceId"
                params={{ workspaceId: workspace.id }}
                onClick={onNavigate}
                className={itemClass}
                activeProps={{ className: activeClass }}
                activeOptions={{ exact: true }}
              >
                <Briefcase />
                <span className="truncate">{workspace.name}</span>
              </Link>
              {current === workspace.id && <WorkspaceLists workspaceId={workspace.id} onNavigate={onNavigate} />}
            </li>
          ))}
        </ul>
      </Collapsible.Content>
    </Collapsible.Root>
  );
}

function WorkspaceLists({ workspaceId, onNavigate }: { workspaceId: string; onNavigate?: () => void }) {
  const { data } = useQuery({
    queryKey: ['resources', workspaceId, 'collections'],
    queryFn: () => collectionChildren(workspaceId),
  });
  return (
    <ul className="mt-0.5 mb-1 ml-4 flex flex-col gap-0.5 border-l pl-2">
      {data?.map((list) => (
        <li key={list.id}>
          <Link
            to="/w/$workspaceId/l/$listId"
            params={{ workspaceId, listId: list.id }}
            onClick={onNavigate}
            className={itemClass}
            activeProps={{ className: activeClass }}
          >
            <span className="truncate">{list.name}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
