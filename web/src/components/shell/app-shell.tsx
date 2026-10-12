import { Menu, Search } from 'lucide-react';
import { Dialog as DialogPrimitive } from 'radix-ui';
import { useEffect, useState, type ReactNode } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/feedback';
import { CommandPaletteProvider, useCommandPalette } from './command-palette';
import { Sidebar } from './sidebar';
import { UserMenu } from './user-menu';

export function AppShell({ children }: { children: ReactNode }) {
  const [drawer, setDrawer] = useState(false);
  return (
    <CommandPaletteProvider>
      <div className="flex min-h-dvh">
        <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 lg:block">
          <Sidebar />
        </aside>
        <DialogPrimitive.Root open={drawer} onOpenChange={setDrawer}>
          <DialogPrimitive.Portal>
            <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/40 lg:hidden" />
            <DialogPrimitive.Content className="fixed inset-y-0 left-0 z-50 w-72 shadow-xl lg:hidden" aria-describedby={undefined}>
              <DialogPrimitive.Title className="sr-only">Navigation</DialogPrimitive.Title>
              <Sidebar onNavigate={() => setDrawer(false)} />
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
        <div className="flex min-w-0 flex-1 flex-col">
          <TopBar onMenu={() => setDrawer(true)} />
          <main id="main" className="flex-1">
            {children}
          </main>
        </div>
      </div>
    </CommandPaletteProvider>
  );
}

function TopBar({ onMenu }: { onMenu: () => void }) {
  const palette = useCommandPalette();
  const navigate = useNavigate();
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

  useEffect(() => {
    let prefix = false;
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return;
      if (event.key === 'g') {
        prefix = true;
        return;
      }
      if (prefix) {
        prefix = false;
        const path = event.key === 'h' ? '/' : event.key === 'i' ? '/inbox' : event.key === 's' ? '/search' : '';
        if (path) {
          event.preventDefault();
          void navigate({ to: path });
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-background/85 px-3 backdrop-blur sm:px-4">
      <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Open navigation" onClick={onMenu}>
        <Menu />
      </Button>
      <button
        type="button"
        onClick={palette.open}
        className="flex h-9 w-full max-w-md items-center gap-2 rounded-md border bg-surface px-3 text-[13px] text-muted shadow-xs hover:border-input"
      >
        <Search className="size-4" />
        <span className="flex-1 text-left">Search or jump to…</span>
        <Kbd className="hidden sm:inline">{mac ? '⌘K' : 'Ctrl K'}</Kbd>
      </button>
      <div className="ml-auto flex items-center gap-1">
        <UserMenu />
      </div>
    </header>
  );
}
