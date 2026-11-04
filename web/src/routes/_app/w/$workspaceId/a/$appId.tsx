import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link } from '@tanstack/react-router';
import { AppWindow } from 'lucide-react';
import { Page, PageHeader } from '@/components/page';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { QueryFeedback } from '@/features/admin/common';
import { appQuery } from '@/features/apps/api';
import { AppFrame } from '@/features/apps/app-frame';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/a/$appId')({ component: AppPage });

function AppPage() {
  const { workspaceId, appId } = Route.useParams();
  const { t } = useTranslation('apps');
  const app = useQuery(appQuery(appId));
  return (
    <Page wide className="flex min-h-[calc(100vh-3rem)] flex-col">
      <PageHeader
        icon={AppWindow}
        title={app.data?.name ?? t('page.app')}
        description={app.data?.description}
        actions={
          <div className="flex items-center gap-2">
            {app.data?.state === 'draft' && <Badge>{t('state.draft')}</Badge>}
            {app.data?.can_write && (
              <Button size="sm" asChild>
                <Link to="/w/$workspaceId/settings/apps" params={{ workspaceId }}>
                  {t('page.settings')}
                </Link>
              </Button>
            )}
          </div>
        }
      />
      <QueryFeedback query={app} />
      {app.data && <AppFrame key={`${app.data.id}:${app.data.current_version}:${app.data.version}`} app={app.data} />}
    </Page>
  );
}
