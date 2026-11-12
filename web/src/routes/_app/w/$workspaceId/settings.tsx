import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { AppWindow, ArrowLeft, History, Package, RadioTower, Settings2, Users, Workflow } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { Alert } from '@/components/ui/feedback';
import { can } from '@/features/lists/access';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/settings')({ component: WorkspaceSettings });

function WorkspaceSettings() {
  const { t } = useTranslation('workspaces');
  const { workspaceId } = Route.useParams();
  const workspace = useQuery(resourceQuery(workspaceId));
  const params = { workspaceId };
  const readOnly = workspace.data && !can(workspace.data, 'manage');
  return (
    <Page className="max-w-5xl">
      <Link to="/w/$workspaceId" params={params} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground">
        <ArrowLeft className="size-3.5" /> {workspace.data?.name ?? t('workspace')}
      </Link>
      <PageHeader icon={Settings2} title={t('settings.title')} description={workspace.data?.name} />
      <SubNavLayout
        nav={
          <SubNav label={t('settings.title')}>
            <SubNavLink to="/w/$workspaceId/settings" params={params} activeOptions={{ exact: true }}>
              <Settings2 /> {t('settings.general')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/members" params={params}>
              <Users /> {t('settings.members')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/workflows" params={params}>
              <Workflow /> {t('settings.workflows')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/runs" params={params}>
              <History /> {t('settings.runs')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/apps" params={params}>
              <AppWindow /> {t('settings.apps')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/connections" params={params}>
              <RadioTower /> {t('settings.connections')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/solutions" params={params}>
              <Package /> {t('settings.solutions')}
            </SubNavLink>
          </SubNav>
        }
      >
        {readOnly && (
          <Alert tone="warning">{t('settings.readOnly')}</Alert>
        )}
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
