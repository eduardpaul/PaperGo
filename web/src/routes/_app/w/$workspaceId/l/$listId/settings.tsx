import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { Activity, ArrowLeft, Columns3, LayoutList, Settings2, ShieldCheck } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { Alert } from '@/components/ui/feedback';
import { useCanManage } from '@/features/lists/access';
import { CollectionIcon } from '@/features/lists/collection-icon';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings')({ component: CollectionSettingsLayout });

/** The settings of a list or library, one page per topic. */
function CollectionSettingsLayout() {
  const params = Route.useParams();
  const collection = useQuery(resourceQuery(params.listId));
  const { canManage, checked } = useCanManage(params.listId);
  const library = collection.data?.kind === 'library';
  return (
    <Page className="max-w-5xl">
      <Link
        to="/w/$workspaceId/l/$listId"
        params={params}
        className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground"
      >
        <ArrowLeft className="size-3.5" /> {collection.data?.name ?? 'Back'}
      </Link>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {collection.data && <CollectionIcon collection={collection.data} className="size-5 text-accent" />}
            {library ? 'Library settings' : 'List settings'}
          </span>
        }
        description={collection.data?.name}
      />
      <SubNavLayout
        nav={
          <SubNav label={library ? 'Library settings' : 'List settings'}>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings" params={params} activeOptions={{ exact: true }}>
              <Settings2 /> General
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/columns" params={params}>
              <Columns3 /> Columns
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/views" params={params}>
              <LayoutList /> Views
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/permissions" params={params}>
              <ShieldCheck /> Permissions
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/activity" params={params}>
              <Activity /> Background work
            </SubNavLink>
          </SubNav>
        }
      >
        {checked && !canManage && (
          <Alert tone="warning">
            Only people who manage this {library ? 'library' : 'list'} change its settings. You can look at them.
          </Alert>
        )}
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
