import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import { LogOut, Monitor, Moon, Settings, ShieldCheck, Sun } from 'lucide-react';
import { api, clearToken } from '@/api/http';
import { authenticationMode, oidcSignIn } from '@/api/auth';
import { keys, meQuery, preferencesQuery } from '@/api/queries';
import type { Preferences } from '@/api/types';
import { Avatar } from '@/components/ui/avatar';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/menu';
import { applyTheme, storedTheme, type Theme } from '@/lib/preferences';

export function UserMenu() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: me } = useQuery(meQuery);
  const { data: preferences } = useQuery(preferencesQuery);
  const setTheme = useMutation({
    mutationFn: (theme: Theme) =>
      api<Preferences>('/v1/identity/me/preferences', {
        method: 'PATCH',
        ifMatch: preferences?.version ?? 0,
        body: { theme },
      }).then((result) => result.data),
    onMutate: (theme) => applyTheme(theme),
    onSuccess: (updated) => queryClient.setQueryData(keys.preferences, updated),
    onError: () => queryClient.invalidateQueries({ queryKey: keys.preferences }),
  });
  const theme = preferences?.theme ?? storedTheme();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="rounded-full p-0.5 hover:bg-surface-muted" aria-label="Account">
        <Avatar name={me?.display_name || me?.subject} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <div className="px-2 py-2">
          <p className="truncate text-[13px] font-medium">{me?.display_name || me?.subject}</p>
          <p className="truncate text-xs text-muted">{me?.email || me?.organization_identifier}</p>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings /> Settings
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/admin">
            <ShieldCheck /> Administration
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme.mutate(value as Theme)}>
          <DropdownMenuRadioItem value="system">
            <Monitor className="size-4 text-muted" /> System
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">
            <Sun className="size-4 text-muted" /> Light
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <Moon className="size-4 text-muted" /> Dark
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        {authenticationMode() === 'proxy' ? (
          <p className="px-2 py-2 text-xs text-muted">Sign-in is managed by your reverse proxy.</p>
        ) : (
          <DropdownMenuItem
            onSelect={() => {
              const signIn = oidcSignIn();
              queryClient.clear();
              if (signIn) {
                // Ends the provider session too, so the next sign-in asks for credentials again.
                const signedOut = () => void navigate({ to: '/login', search: { returnTo: '/' } });
                void signIn.signOut().then((redirected) => {
                  if (!redirected) signedOut();
                }, signedOut);
                return;
              }
              clearToken();
              void navigate({ to: '/login', search: { returnTo: '/' } });
            }}
          >
            <LogOut /> Sign out
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
