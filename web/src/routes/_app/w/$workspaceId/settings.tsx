import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { ArrowLeft, History, Settings2, Users, Workflow } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { Alert } from '@/components/ui/feedback';
import { can } from '@/features/lists/access';

export const Route = createFileRoute('/_app/w/$workspaceId/settings')({ component: WorkspaceSettings });

function WorkspaceSettings() {
  const { workspaceId } = Route.useParams();
  const workspace = useQuery(resourceQuery(workspaceId));
  const params = { workspaceId };
  return (
    <Page className="max-w-5xl">
      <Link to="/w/$workspaceId" params={params} className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground">
        <ArrowLeft className="size-3.5" /> {workspace.data?.name ?? 'Workspace'}
      </Link>
      <PageHeader icon={Settings2} title="Workspace settings" description={workspace.data?.name} />
      <SubNavLayout
        nav={
          <SubNav label="Workspace settings">
            <SubNavLink to="/w/$workspaceId/settings" params={params} activeOptions={{ exact: true }}>
              <Settings2 /> General
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/members" params={params}>
              <Users /> Members
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/workflows" params={params}>
              <Workflow /> Workflows
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/runs" params={params}>
              <History /> Runs
            </SubNavLink>
          </SubNav>
        }
      >
        {workspace.data && !can(workspace.data, 'manage') && (
          <Alert tone="warning">Only workspace owners change these settings. You can look at them.</Alert>
        )}
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
