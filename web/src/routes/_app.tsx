import { createFileRoute, Outlet, redirect, useParams } from '@tanstack/react-router';
import { token } from '@/api/http';
import { authenticationMode } from '@/api/auth';
import { meQuery } from '@/api/queries';
import { AppShell } from '@/components/shell/app-shell';
import { installedApp } from '@/features/pwa/installed';
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
  // The window of an app installed on a phone shows that app alone.
  const { appId } = useParams({ strict: false });
  const alone = !!appId && appId === installedApp();
  return (
    <PreferencesProvider>
      {alone ? (
        <main id="main">
          <Outlet />
        </main>
      ) : (
        <AppShell>
          <Outlet />
        </AppShell>
      )}
    </PreferencesProvider>
  );
}
