import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { AppWindow, Settings2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { QueryFeedback } from '@/features/admin/common';
import { appQuery } from '@/features/apps/api';
import { AppFrame } from '@/features/apps/app-frame';
import { AppIcon } from '@/features/apps/icon';
import { InstallButton, useAppManifest } from '@/features/pwa/install';
import { installedApp } from '@/features/pwa/installed';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/a/$appId')({ component: AppPage });

/**
 * An app fills the screen below the top bar and is its only scroll area, so
 * it scrolls like a page on a phone and its sticky parts stay put. The
 * header is one line; on a phone the frame runs edge to edge. Installed on a
 * phone, the app has the whole window, which has no top bar.
 */
function AppPage() {
  const { workspaceId, appId } = Route.useParams();
  const { t } = useTranslation('apps');
  const app = useQuery(appQuery(appId));
  useAppManifest(app.data);
  return (
    <div className={cn('flex flex-col sm:px-6 sm:pb-6 lg:px-8', installedApp() === appId ? 'h-dvh' : 'h-[calc(100dvh-3.5rem)]')}>
      <header className="flex min-h-12 items-center gap-2 px-4 py-2 sm:px-0 sm:py-4">
        {app.data ? <AppIcon app={app.data} className="size-7" /> : <AppWindow className="size-5 shrink-0 text-accent" />}
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-base font-semibold sm:text-lg">{app.data?.name ?? t('page.app')}</h1>
          {app.data?.description && <p className="hidden truncate text-xs text-muted sm:block">{app.data.description}</p>}
        </div>
        {app.data?.state === 'draft' && <Badge>{t('state.draft')}</Badge>}
        {app.data && <InstallButton name={app.data.name} compact />}
        {app.data?.can_write && (
          <Button size="icon" variant="ghost" className="size-10" asChild>
            <Link to="/w/$workspaceId/settings/apps" params={{ workspaceId }} aria-label={t('page.settings')} title={t('page.settings')}>
              <Settings2 />
            </Link>
          </Button>
        )}
      </header>
      <QueryFeedback query={app} />
      {app.data && <AppFrame key={`${app.data.id}:${app.data.current_version}:${app.data.version}`} app={app.data} />}
    </div>
  );
}
