import { useQuery } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { collectionChildren, smartFoldersQuery, workspacesQuery } from '@/api/queries';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { useSearchAvailable } from '@/lib/search';

interface Palette {
  open: () => void;
}

const PaletteContext = createContext<Palette>({ open: () => {} });

export function useCommandPalette() {
  return useContext(PaletteContext);
}

const jumps = [
  { label: 'Home', to: '/' as const },
  { label: 'Inbox', to: '/inbox' as const },
  { label: 'Search', to: '/search' as const },
  { label: 'Workspaces', to: '/w' as const },
  { label: 'Settings', to: '/settings' as const },
];

export function CommandPaletteProvider({ children }: { children: ReactNode }) {
  const [opened, setOpened] = useState(false);
  const value = useMemo(() => ({ open: () => setOpened(true) }), []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setOpened((current) => !current);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <PaletteContext.Provider value={value}>
      {children}
      <Dialog open={opened} onOpenChange={setOpened}>
        <DialogContent className="top-[15vh] overflow-hidden p-0" hideClose>
          <DialogTitle className="sr-only">Search or jump to</DialogTitle>
          <PaletteBody onClose={() => setOpened(false)} />
        </DialogContent>
      </Dialog>
    </PaletteContext.Provider>
  );
}

function PaletteBody({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const searchAvailable = useSearchAvailable();
  const workspaces = useQuery(workspacesQuery);
  const folders = useQuery(smartFoldersQuery);
  const [workspaceId, setWorkspaceId] = useState<string>();
  const lists = useQuery({
    queryKey: ['resources', workspaceId, 'collections'],
    enabled: !!workspaceId,
    queryFn: () => collectionChildren(workspaceId!),
  });

  function go(to: string) {
    onClose();
    void navigate({ href: to });
  }

  return (
    <Command>
      <CommandInput placeholder="Search or jump to…" autoFocus />
      <CommandList>
        <CommandEmpty>{searchAvailable ? 'No matches. Press enter on Search to look through items.' : 'No matches.'}</CommandEmpty>
        <CommandGroup heading="Go to">
          {jumps.filter((jump) => jump.to !== '/search' || searchAvailable).map((jump) => (
            <CommandItem key={jump.to} onSelect={() => go(jump.to)}>
              {jump.label}
            </CommandItem>
          ))}
        </CommandGroup>
        <CommandGroup heading="Workspaces">
          {workspaces.data
            ?.filter((workspace) => !workspace.personal_owner_id)
            .map((workspace) => (
              <CommandItem key={workspace.id} onSelect={() => go(`/w/${workspace.id}`)} onMouseEnter={() => setWorkspaceId(workspace.id)}>
                {workspace.name}
              </CommandItem>
            ))}
        </CommandGroup>
        {!!lists.data?.length && (
          <CommandGroup heading="Lists and libraries">
            {lists.data.map((list) => (
              <CommandItem key={list.id} onSelect={() => go(`/w/${list.workspace_id}/l/${list.id}`)}>
                {list.name}
              </CommandItem>
            ))}
          </CommandGroup>
        )}
        <CommandGroup heading="Smart folders">
          {folders.data?.map((folder) => (
            <CommandItem key={folder.id} onSelect={() => go(`/f/${folder.id}`)}>
              {folder.name}
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </Command>
  );
}
