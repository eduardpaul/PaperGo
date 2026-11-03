import { useQuery } from '@tanstack/react-query';
import { createFileRoute, Link, Outlet } from '@tanstack/react-router';
import { Activity, ArrowLeft, Columns3, LayoutList, Plug, Settings2, ShieldCheck, Workflow } from 'lucide-react';
import { resourceQuery } from '@/api/queries';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { Alert } from '@/components/ui/feedback';
import { useAccess } from '@/features/lists/access';
import { CollectionIcon } from '@/features/lists/collection-icon';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/w/$workspaceId/l/$listId/settings')({ component: CollectionSettingsLayout });

/** The settings of a list or library, one page per topic. */
function CollectionSettingsLayout() {
  const { t } = useTranslation('workspaces');
  const params = Route.useParams();
  const collection = useQuery(resourceQuery(params.listId));
  const { canManage, loaded: checked } = useAccess(params.listId);
  const library = collection.data?.kind === 'library';
  return (
    <Page className="max-w-5xl">
      <Link
        to="/w/$workspaceId/l/$listId"
        params={params}
        className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-foreground"
      >
        <ArrowLeft className="size-3.5" /> {collection.data?.name ?? t('collection.back')}
      </Link>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            {collection.data && <CollectionIcon collection={collection.data} className="size-5 text-accent" />}
            {library ? t('collection.librarySettings') : t('collection.listSettings')}
          </span>
        }
        description={collection.data?.name}
      />
      <SubNavLayout
        nav={
          <SubNav label={library ? t('collection.librarySettings') : t('collection.listSettings')}>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings" params={params} activeOptions={{ exact: true }}>
              <Settings2 /> {t('settings.general')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/columns" params={params}>
              <Columns3 /> {t('collection.columns')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/views" params={params}>
              <LayoutList /> {t('collection.views')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/permissions" params={params}>
              <ShieldCheck /> {t('collection.permissions')}
            </SubNavLink>
            <SubNavLink to="/w/$workspaceId/settings/workflows" params={{ workspaceId: params.workspaceId }} search={{ collection: params.listId }}><Workflow /> {t('settings.workflows')}</SubNavLink>
            {!library && <SubNavLink to="/w/$workspaceId/l/$listId/settings/integrations" params={params}><Plug /> {t('collection.integrations')}</SubNavLink>}
            <SubNavLink to="/w/$workspaceId/l/$listId/settings/activity" params={params}>
              <Activity /> {t('collection.activity')}
            </SubNavLink>
          </SubNav>
        }
      >
        {checked && !canManage && (
          <Alert tone="warning">
            {library ? t('collection.readOnlyLibrary') : t('collection.readOnlyList')}
          </Alert>
        )}
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
