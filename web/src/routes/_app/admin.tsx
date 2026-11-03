import { createFileRoute, Outlet } from '@tanstack/react-router';
import { Building2, Download, Globe, KeyRound, FolderTree, ScrollText, ShieldCheck, ShieldHalf, UserRound, Users } from 'lucide-react';
import { Page, PageHeader } from '@/components/page';
import { SubNav, SubNavLayout, SubNavLink } from '@/components/sub-nav';
import { useHasScope } from '@/features/admin/common';
import { useTranslation } from '@/i18n';

export const Route = createFileRoute('/_app/admin')({ component: Administration });

function Administration() {
  const { t } = useTranslation('admin');
  const users = useHasScope('user.read');
  const groups = useHasScope('group.read');
  const roles = useHasScope('role.read');
  const taxonomy = useHasScope('taxonomy.read');
  const contentTypes = useHasScope('contentType.read');
  const audit = useHasScope('audit.read');
  const maintenance = useHasScope('organization.manage');
  return (
    <Page className="max-w-6xl">
      <PageHeader icon={ShieldCheck} title={t('title')} description={t('description')} />
      <SubNavLayout
        nav={
          <SubNav label={t('title')}>
            <SubNavLink to="/admin" activeOptions={{ exact: true }}>
              <Building2 /> {t('nav.organization')}
            </SubNavLink>
            {users && (
              <SubNavLink to="/admin/users">
                <UserRound /> {t('nav.users')}
              </SubNavLink>
            )}
            {groups && (
              <SubNavLink to="/admin/groups">
                <Users /> {t('nav.groups')}
              </SubNavLink>
            )}
            {roles && (
              <SubNavLink to="/admin/roles">
                <ShieldHalf /> {t('nav.roles')}
              </SubNavLink>
            )}
            {taxonomy && (
              <SubNavLink to="/admin/relationship-types">
                <FolderTree /> {t('nav.relationshipTypes')}
              </SubNavLink>
            )}
            {taxonomy && (
              <SubNavLink to="/admin/terms">
                <FolderTree /> {t('nav.terms')}
              </SubNavLink>
            )}
            {contentTypes && (
              <SubNavLink to="/admin/content-types">
                <FolderTree /> {t('nav.contentTypes')}
              </SubNavLink>
            )}
            {audit && (
              <SubNavLink to="/admin/audit">
                <ScrollText /> {t('nav.audit')}
              </SubNavLink>
            )}
            {maintenance && <SubNavLink to="/admin/tokens"><KeyRound /> {t('nav.tokens')}</SubNavLink>}
            {maintenance && (
              <SubNavLink to="/admin/trusted-hosts">
                <Globe /> {t('nav.trustedHosts')}
              </SubNavLink>
            )}
            {maintenance && (
              <SubNavLink to="/admin/maintenance">
                <Download /> {t('nav.maintenance')}
              </SubNavLink>
            )}
          </SubNav>
        }
      >
        <Outlet />
      </SubNavLayout>
    </Page>
  );
}
