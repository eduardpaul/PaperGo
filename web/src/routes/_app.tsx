import { createFileRoute, Outlet, redirect } from '@tanstack/react-router';
import { token } from '@/api/http';
import { authenticationMode } from '@/api/auth';
import { meQuery } from '@/api/queries';
import { AppShell } from '@/components/shell/app-shell';
import { useLiveEvents } from '@/lib/live';
import { PreferencesProvider } from '@/lib/preferences';

export const Route = createFileRoute('/_app')({
  beforeLoad: ({ location }) => {
    if (authenticationMode() === 'bearer' && !token())
      throw redirect({ to: '/login', search: { returnTo: location.href } });
  },
  loader: ({ context }) => context.queryClient.ensureQueryData(meQuery),
  component: SignedIn,
});

function SignedIn() {
  useLiveEvents();
  return (
    <PreferencesProvider>
      <AppShell>
        <Outlet />
      </AppShell>
    </PreferencesProvider>
  );
}
